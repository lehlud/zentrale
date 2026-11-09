// Package gitforge connects user-owned GitHub/GitLab OAuth apps and repositories.
package gitforge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/lehlud/zentrale/domain"
)

type Client struct {
	HTTP          *http.Client
	GitHubAuthURL string
	GitHubAPIURL  string

	hostKeyMu sync.Mutex
	hostKeys  []ssh.PublicKey
	hostKeyAt time.Time
}

func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) > 5 || request.URL.Host != via[0].URL.Host || request.URL.Scheme != via[0].URL.Scheme {
					return http.ErrUseLastResponse
				}

				return nil
			},
		},
		GitHubAuthURL: "https://github.com",
		GitHubAPIURL:  "https://api.github.com",
	}
}

type APIError struct {
	Status  int
	Message string
}

func (failure *APIError) Error() string {
	return fmt.Sprintf("Git provider returned HTTP %d: %s", failure.Status, failure.Message)
}

func (client *Client) request(
	ctx context.Context,
	method, address, token string,
	body any,
	form bool,
	result any,
) error {
	var data []byte
	var err error
	contentType := "application/json"

	if form {
		data = []byte(body.(url.Values).Encode())
		contentType = "application/x-www-form-urlencoded"
	} else if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}

	request, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(data))
	if err != nil {
		return err
	}

	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", contentType)

	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	data, err = io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil {
		return err
	}

	if len(data) > 4*1024*1024 {
		return errors.New("Git provider response exceeds 4 MiB")
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if result != nil {
			_ = json.Unmarshal(data, result)
		}

		var failure map[string]any
		_ = json.Unmarshal(data, &failure)
		message := text(failure["message"])
		if message == "" {
			message = text(failure["error_description"])
		}

		if message == "" {
			message = http.StatusText(response.StatusCode)
		}

		if len(message) > 500 {
			message = message[:500]
		}

		if token != "" {
			message = strings.ReplaceAll(message, token, "[redacted]")
		}

		if values, ok := body.(url.Values); ok {
			for _, name := range []string{"client_secret", "device_code", "refresh_token"} {
				if secret := values.Get(name); secret != "" {
					message = strings.ReplaceAll(message, secret, "[redacted]")
				}
			}
		}

		return &APIError{Status: response.StatusCode, Message: message}
	}

	if result == nil || len(data) == 0 {
		return nil
	}

	if err := json.Unmarshal(data, result); err != nil {
		return errors.New("Git provider returned invalid JSON")
	}

	return nil
}

func text(value any) string {
	result, _ := value.(string)
	return result
}

func number(value any) int {
	result, _ := value.(float64)
	return int(result)
}

func object(value any) map[string]any {
	result, _ := value.(map[string]any)
	if result == nil {
		return map[string]any{}
	}

	return result
}

func (client *Client) authRoot(account account) string {
	if account.Public.Kind == "github" {
		return strings.TrimRight(client.GitHubAuthURL, "/")
	}

	return account.Public.BaseURL
}

func (client *Client) apiRoot(account account) string {
	if account.Public.Kind == "github" {
		return strings.TrimRight(client.GitHubAPIURL, "/")
	}

	return account.Public.BaseURL + "/api/v4"
}

func (client *Client) tokenURL(account account) string {
	if account.Public.Kind == "github" {
		return client.authRoot(account) + "/login/oauth/access_token"
	}

	return client.authRoot(account) + "/oauth/token"
}

func (client *Client) oauth(ctx context.Context, account account, values url.Values) (map[string]any, error) {
	values.Set("client_id", account.Public.ClientID)

	if account.Secret != "" {
		values.Set("client_secret", account.Secret)
	}

	var result map[string]any
	err := client.request(ctx, http.MethodPost, client.tokenURL(account), "", values, true, &result)
	// Device-flow pending/denied responses can use HTTP 400; their OAuth error
	// code must be interpreted rather than being mistaken for a transport error.
	var failure *APIError
	if errors.As(err, &failure) && failure.Status == 400 && text(result["error"]) != "" {
		return result, nil
	}

	return result, err
}

func normalizeRepository(kind string, data map[string]any) domain.GitRepository {
	repository := domain.GitRepository{Name: text(data["name"]), Identity: strconv.Itoa(number(data["id"]))}
	permissions := object(data["permissions"])

	if kind == "github" {
		repository.ID, repository.FullName = text(data["full_name"]), text(data["full_name"])
		repository.SSHURL, repository.WebURL = text(data["ssh_url"]), text(data["html_url"])
		repository.Private, _ = data["private"].(bool)
		repository.CanManageDeployKeys, _ = permissions["admin"].(bool)
	} else {
		repository.ID, repository.FullName = repository.Identity, text(data["path_with_namespace"])
		repository.SSHURL, repository.WebURL = text(data["ssh_url_to_repo"]), text(data["web_url"])
		repository.Private = text(data["visibility"]) != "public"
		repository.CanManageDeployKeys = number(object(permissions["project_access"])["access_level"]) >= 40 ||
			number(object(permissions["group_access"])["access_level"]) >= 40
	}

	return repository
}

func repositoryPath(kind, reference string) (string, error) {
	if kind == "gitlab" {
		id, err := strconv.ParseInt(reference, 10, 64)
		if err != nil || id <= 0 {
			return "", errors.New("invalid GitLab repository identifier")
		}

		return "/projects/" + reference, nil
	}

	parts := strings.Split(reference, "/")
	if len(parts) != 2 ||
		parts[0] == "" ||
		parts[1] == "" ||
		parts[0] == "." ||
		parts[0] == ".." ||
		parts[1] == "." ||
		parts[1] == ".." {
		return "", errors.New("invalid GitHub repository identifier")
	}

	return "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]), nil
}

func (client *Client) repository(
	ctx context.Context,
	account account,
	reference string,
) (domain.GitRepository, error) {
	path, err := repositoryPath(account.Public.Kind, reference)
	if err != nil {
		return domain.GitRepository{}, err
	}

	var data map[string]any
	err = client.request(ctx, http.MethodGet, client.apiRoot(account)+path, account.AccessToken, nil, false, &data)

	return normalizeRepository(account.Public.Kind, data), err
}

func (client *Client) repositories(ctx context.Context, account account) ([]domain.GitRepository, error) {
	result := []domain.GitRepository{}
	for page := 1; page <= 100; page++ {
		path := fmt.Sprintf("/user/repos?per_page=100&page=%d&affiliation=owner,organization_member&sort=updated", page)

		if account.Public.Kind == "gitlab" {
			path = fmt.Sprintf(
				"/projects?membership=true&min_access_level=40&per_page=100&page=%d&order_by=last_activity_at",
				page,
			)
		}

		var rows []map[string]any
		if err := client.request(ctx, http.MethodGet, client.apiRoot(account)+path, account.AccessToken, nil, false, &rows); err != nil {
			return nil, err
		}

		for _, row := range rows {
			repository := normalizeRepository(account.Public.Kind, row)

			if account.Public.Kind == "gitlab" {
				repository.CanManageDeployKeys = true
			}

			result = append(result, repository)
		}

		if len(rows) < 100 {
			return result, nil
		}
	}

	return nil, errors.New("repository catalog exceeds 10000 entries")
}

func (client *Client) namespaces(ctx context.Context, account account) ([]domain.GitNamespace, error) {
	result := []domain.GitNamespace{}

	if account.Public.Kind == "github" {
		result = append(result, domain.GitNamespace{
			ID:       account.Public.UserName,
			Name:     account.Public.UserName,
			Personal: true,
		})
	}

	for page := 1; page <= 100; page++ {
		path := fmt.Sprintf("/user/orgs?per_page=100&page=%d", page)

		if account.Public.Kind == "gitlab" {
			path = fmt.Sprintf("/namespaces?per_page=100&page=%d", page)
		}

		var rows []map[string]any
		if err := client.request(ctx, http.MethodGet, client.apiRoot(account)+path, account.AccessToken, nil, false, &rows); err != nil {
			return nil, err
		}

		for _, row := range rows {
			if account.Public.Kind == "github" {
				name := text(row["login"])
				result = append(result, domain.GitNamespace{ID: name, Name: name})
			} else {
				result = append(
					result,
					domain.GitNamespace{
						ID:       strconv.Itoa(number(row["id"])),
						Name:     text(row["full_path"]),
						Personal: text(row["kind"]) == "user",
					},
				)
			}
		}

		if len(rows) < 100 {
			return result, nil
		}
	}

	return nil, errors.New("namespace catalog exceeds 10000 entries")
}

func (client *Client) createRepository(
	ctx context.Context,
	account account,
	input domain.GitRepositoryInput,
) (domain.GitRepository, error) {
	path := "/user/repos"
	body := map[string]any{
		"name":        input.Name,
		"description": input.Description,
		"private":     input.Private,
		"auto_init":   false,
	}

	if account.Public.Kind == "github" && input.NamespaceID != account.Public.UserName {
		path = "/orgs/" + url.PathEscape(input.NamespaceID) + "/repos"
	}

	if account.Public.Kind == "gitlab" {
		id, err := strconv.Atoi(input.NamespaceID)
		if err != nil || id <= 0 {
			return domain.GitRepository{}, errors.New("select a GitLab namespace")
		}

		visibility := "public"

		if input.Private {
			visibility = "private"
		}

		path, body = "/projects", map[string]any{
			"name":                   input.Name,
			"path":                   input.Name,
			"description":            input.Description,
			"namespace_id":           id,
			"visibility":             visibility,
			"initialize_with_readme": false,
		}
	}

	var data map[string]any
	err := client.request(ctx, http.MethodPost, client.apiRoot(account)+path, account.AccessToken, body, false, &data)

	return normalizeRepository(account.Public.Kind, data), err
}

func decodeValue(value any, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, target)
}
