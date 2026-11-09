package gitforge

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

var repositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

func (service *Service) RefreshRepositories(connectionID string) error {
	service.mu.Lock()
	defer service.mu.Unlock()

	if _, err := service.account(connectionID); err != nil {
		return err
	}

	delete(service.cache, connectionID)

	return nil
}

func (service *Service) Repositories(
	ctx context.Context,
	connectionID, search string,
	offset int,
) (domain.GitRepositoryPage, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	value, err := service.authenticated(ctx, connectionID)
	if err != nil {
		return domain.GitRepositoryPage{}, err
	}

	if offset < 0 {
		return domain.GitRepositoryPage{}, errors.New("negative repository offset")
	}

	cache, found := service.cache[connectionID]
	if !found || service.Now().Sub(cache.At) > 5*time.Minute {
		items, err := service.Client.repositories(ctx, value)
		if err != nil {
			return domain.GitRepositoryPage{}, err
		}

		cache = repositoryCache{At: service.Now(), Repositories: items}
		service.cache[connectionID] = cache
	}

	items := []domain.GitRepository{}
	search = strings.ToLower(strings.TrimSpace(search))

	for _, repository := range cache.Repositories {
		if repository.CanManageDeployKeys && strings.Contains(strings.ToLower(repository.FullName), search) {
			items = append(items, repository)
		}
	}

	start := min(offset, len(items))
	end := min(start+50, len(items))

	return domain.GitRepositoryPage{Items: items[start:end], HasMore: end < len(items), NextOffset: end}, nil
}

func (service *Service) Namespaces(ctx context.Context, connectionID string) ([]domain.GitNamespace, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	value, err := service.authenticated(ctx, connectionID)
	if err != nil {
		return nil, err
	}

	return service.Client.namespaces(ctx, value)
}

func (service *Service) CreateRepository(ctx context.Context, input domain.GitRepositoryInput) (domain.GitRepository, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	input.Name = strings.TrimSpace(input.Name)

	if !repositoryName.MatchString(input.Name) || input.Name == "." || input.Name == ".." {
		return domain.GitRepository{}, errors.New("repository name must contain 1–100 letters, digits, dots, underscores or hyphens")
	}

	if input.NamespaceID == "" {
		return domain.GitRepository{}, errors.New("select an owner or organization")
	}

	value, err := service.authenticated(ctx, input.ConnectionID)
	if err != nil {
		return domain.GitRepository{}, err
	}

	// Resolve the namespace from the current account, rather than trusting a label
	// supplied by the browser. Organization policies are enforced by the provider.
	namespaces, err := service.Client.namespaces(ctx, value)
	if err != nil {
		return domain.GitRepository{}, err
	}

	allowed := false

	for _, namespace := range namespaces {
		if namespace.ID == input.NamespaceID {
			allowed = true
			break
		}
	}

	if !allowed {
		return domain.GitRepository{}, errors.New("selected repository owner is not available to this account")
	}

	repository, err := service.Client.createRepository(ctx, value, input)
	if err == nil {
		delete(service.cache, input.ConnectionID)
	}

	return repository, err
}

type deployKey struct {
	ID        int
	PublicKey string
	Writable  bool
}

func (client *Client) deployKeys(
	ctx context.Context,
	account account,
	repository domain.GitRepository,
) ([]deployKey, error) {
	path, err := repositoryPath(account.Public.Kind, repository.ID)
	if err != nil {
		return nil, err
	}

	suffix := "/keys"

	if account.Public.Kind == "gitlab" {
		suffix = "/deploy_keys"
	}

	result := []deployKey{}
	for page := 1; page <= 100; page++ {
		var rows []map[string]any
		address := client.apiRoot(account) + path + suffix + "?per_page=100&page=" + strconv.Itoa(page)
		if err := client.request(ctx, "GET", address, account.AccessToken, nil, false, &rows); err != nil {
			return nil, err
		}

		for _, row := range rows {
			key := deployKey{ID: number(row["id"]), PublicKey: text(row["key"])}

			if account.Public.Kind == "github" {
				readOnly, valid := row["read_only"].(bool)
				key.Writable = valid && !readOnly
			} else {
				key.Writable, _ = row["can_push"].(bool)
			}

			result = append(result, key)
		}

		if len(rows) < 100 {
			return result, nil
		}
	}

	return nil, errors.New("deploy-key catalog exceeds 10000 entries")
}

func (client *Client) createDeployKey(
	ctx context.Context,
	account account,
	repository domain.GitRepository,
	publicKey string,
) (deployKey, error) {
	path, err := repositoryPath(account.Public.Kind, repository.ID)
	if err != nil {
		return deployKey{}, err
	}

	suffix := "/keys"
	body := map[string]any{
		"title":     "Zentrale – " + repository.FullName,
		"key":       publicKey,
		"read_only": false,
	}

	if account.Public.Kind == "gitlab" {
		suffix = "/deploy_keys"
		body = map[string]any{
			"title":    "Zentrale – " + repository.FullName,
			"key":      publicKey,
			"can_push": true,
		}
	}

	var response map[string]any
	if err := client.request(ctx, "POST", client.apiRoot(account)+path+suffix, account.AccessToken, body, false, &response); err != nil {
		return deployKey{}, err
	}

	key := deployKey{ID: number(response["id"]), PublicKey: text(response["key"])}

	if account.Public.Kind == "github" {
		readOnly, valid := response["read_only"].(bool)
		key.Writable = valid && !readOnly
	} else {
		key.Writable, _ = response["can_push"].(bool)
	}

	if key.ID <= 0 || !key.Writable || publicKeyIdentity(key.PublicKey) != publicKeyIdentity(publicKey) {
		return key, errors.New("provider did not confirm a writable deploy key; retry the repository connection")
	}

	return key, nil
}

func (client *Client) deleteDeployKey(
	ctx context.Context,
	account account,
	repository domain.GitRepository,
	keyID int,
) error {
	path, err := repositoryPath(account.Public.Kind, repository.ID)
	if err != nil {
		return err
	}

	suffix := "/keys/"

	if account.Public.Kind == "gitlab" {
		suffix = "/deploy_keys/"
	}

	return client.request(
		ctx,
		"DELETE",
		client.apiRoot(account)+path+suffix+strconv.Itoa(keyID),
		account.AccessToken,
		nil,
		false,
		nil,
	)
}
