package gitforge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type repositoryBinding struct {
	ID           string `json:"id"`
	ConnectionID string `json:"connectionId"`
	BaseURL      string `json:"baseUrl"`
	RepositoryID string `json:"repositoryId"`
	Identity     string `json:"identity"`
	FullName     string `json:"fullName"`
	SSHURL       string `json:"sshUrl"`

	KeyID       int    `json:"keyId"`
	DeployKeyID int    `json:"deployKeyId"`
	State       string `json:"state"`
}

// RepositoryHTTPURLs describes the same selected repository through its web
// origin. Git translates these URLs to the binding's SSH transport, including
// self-hosted forges whose web and SSH hosts or ports differ.
func (service *Service) RepositoryHTTPURLs(bindingID, sshURL string) ([]string, error) {
	if bindingID == "" {
		return nil, nil
	}
	var binding repositoryBinding
	if err := service.Store.Load("repository_binding", bindingID, &binding); err != nil {
		return nil, err
	}
	if binding.SSHURL != sshURL {
		return nil, errors.New("repository binding does not match its SSH source")
	}
	return []string{strings.TrimRight(binding.BaseURL, "/") + "/" + binding.FullName}, nil
}

func bindingID(account account, repository domain.GitRepository) string {
	digest := sha256.Sum256([]byte(account.Public.Kind + "\n" + account.Public.BaseURL + "\n" + repository.Identity))
	return hex.EncodeToString(digest[:])
}

func rootSourceID(projectID string) string {
	digest := sha256.Sum256([]byte(projectID))
	return "repo-" + hex.EncodeToString(digest[:16])
}

func publicKeyIdentity(value string) string {
	parts := strings.Fields(value)
	if len(parts) < 2 {
		return ""
	}

	return parts[0] + " " + parts[1]
}

func (service *Service) allSources() ([]domain.Source, error) {
	result := []domain.Source{}
	for offset := 0; ; offset += 500 {
		rows, err := service.Store.List("source", nil, 500, offset)
		if err != nil {
			return nil, err
		}

		for _, row := range rows {
			var source domain.Source
			if err := decodeValue(row, &source); err != nil {
				return nil, err
			}

			result = append(result, source)
		}

		if len(rows) < 500 {
			return result, nil
		}
	}
}

func (service *Service) ensureBinding(
	ctx context.Context,
	account account,
	reference string,
) (repositoryBinding, error) {
	repository, err := service.Client.repository(ctx, account, reference)
	if err != nil {
		return repositoryBinding{}, err
	}

	if repository.Identity == "" ||
		repository.Identity == "0" ||
		repository.SSHURL == "" ||
		!repository.CanManageDeployKeys {
		return repositoryBinding{}, errors.New("repository requires permission to manage deploy keys")
	}

	binding := repositoryBinding{
		ID:           bindingID(account, repository),
		ConnectionID: account.Public.ID,
		BaseURL:      account.Public.BaseURL,
		RepositoryID: repository.ID,
		Identity:     repository.Identity,
		FullName:     repository.FullName,
		SSHURL:       repository.SSHURL,
		State:        "pending",
	}

	var previous repositoryBinding
	err = service.Store.Load("repository_binding", binding.ID, &previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return binding, err
	}

	if err == nil {
		binding.KeyID, binding.DeployKeyID = previous.KeyID, previous.DeployKeyID
	}

	remoteKeys, err := service.Client.deployKeys(ctx, account, repository)
	if err != nil {
		return binding, err
	}

	if binding.KeyID == 0 {
		key, err := service.Store.GenerateSSHKey()
		if err != nil {
			return binding, err
		}

		binding.KeyID = key.ID
		claim := datastore.SSHKeyClaim{KeyID: key.ID, Purpose: "repository", PurposeID: binding.ID}
		if err := service.Store.SaveWithSSHKeys("repository_binding", binding.ID, "", binding, claim); err != nil {
			_ = service.Store.DropPrivSSHKey(key.ID)
			return binding, err
		}
	}

	privateKey, err := service.Store.PrivateKey(binding.KeyID)
	if err != nil {
		return binding, err
	}

	publicKey, err := remote.PublicKey(privateKey)
	if err != nil {
		return binding, err
	}

	writable := false

	for _, key := range remoteKeys {
		if publicKeyIdentity(key.PublicKey) != publicKeyIdentity(publicKey) {
			continue
		}

		if key.Writable {
			binding.DeployKeyID, writable = key.ID, true
			break
		}

		// Both providers support deleting a key owned by this connection. GitHub
		// has no update endpoint; replace the same key to enable write access.
		if err := service.Client.deleteDeployKey(ctx, account, repository, key.ID); err != nil {
			return binding, err
		}
	}

	if !writable {
		key, err := service.Client.createDeployKey(ctx, account, repository, publicKey)
		if err != nil {
			return binding, err
		}

		binding.DeployKeyID = key.ID
	}

	binding.State = "active"

	return binding, service.Store.Save("repository_binding", binding.ID, "", binding)
}

func (service *Service) SaveProject(ctx context.Context, project domain.Project) (domain.Project, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	if project.RepositoryID == "" && project.GitConnectionID == "" {
		project.RepositoryName = ""
		return project, service.Store.SaveProjectRepository(project, nil)
	}

	var previous domain.Project
	if err := service.Store.Load("project", project.ID, &previous); err == nil &&
		previous.RepositoryID == project.RepositoryID &&
		previous.GitConnectionID == project.GitConnectionID {
		var source domain.Source
		if err := service.Store.Load("source", rootSourceID(project.ID), &source); err != nil {
			return project, err
		}

		// Editing a project's name or machines does not require a new Git login.
		// The existing repository key and its source permissions stay intact.
		project.RepositoryName = source.RepositoryName

		return project, service.Store.SaveProjectRepository(project, &source)
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return project, err
	}

	account, err := service.authenticated(ctx, project.GitConnectionID)
	if err != nil {
		return project, err
	}

	binding, err := service.ensureBinding(ctx, account, project.RepositoryID)
	if err != nil {
		return project, err
	}

	project.RepositoryID, project.RepositoryName = binding.RepositoryID, binding.FullName
	source := domain.Source{
		ID:             rootSourceID(project.ID),
		ProjectID:      project.ID,
		Name:           "repository",
		URL:            binding.SSHURL,
		KeyID:          binding.KeyID,
		BindingID:      binding.ID,
		RepositoryName: binding.FullName,
		ProjectRoot:    true,
	}

	return project, service.Store.SaveProjectRepository(project, &source)
}

func (service *Service) AttachSource(ctx context.Context, projectID, connectionID, repositoryID string) (domain.Source, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	var project domain.Project
	if err := service.Store.Load("project", projectID, &project); err != nil {
		return domain.Source{}, err
	}

	account, err := service.authenticated(ctx, connectionID)
	if err != nil {
		return domain.Source{}, err
	}

	binding, err := service.ensureBinding(ctx, account, repositoryID)
	if err != nil {
		return domain.Source{}, err
	}

	sources, err := service.allSources()
	if err != nil {
		return domain.Source{}, err
	}

	name := strings.TrimSuffix(strings.TrimPrefix(binding.SSHURL[strings.LastIndex(binding.SSHURL, "/")+1:], "/"), ".git")
	if !remote.ValidID(name) {
		name = "repository-" + binding.ID[:8]
	}

	for _, source := range sources {
		if source.ProjectID != projectID {
			continue
		}

		if source.BindingID == binding.ID {
			return source, nil
		}

		if source.Name == name {
			name += "-" + binding.ID[:8]
		}
	}

	source := domain.Source{
		ID:             rand.Text(),
		ProjectID:      projectID,
		Name:           name,
		URL:            binding.SSHURL,
		KeyID:          binding.KeyID,
		BindingID:      binding.ID,
		RepositoryName: binding.FullName,
	}

	claim := datastore.SSHKeyClaim{KeyID: binding.KeyID, Purpose: "repository", PurposeID: binding.ID}

	return source, service.Store.SaveWithSSHKeys("source", source.ID, projectID, source, claim)
}

func (service *Service) RevokeKey(ctx context.Context, keyID int) error {
	service.mu.Lock()
	defer service.mu.Unlock()

	usages, err := service.Store.SSHKeyUsages()
	if err != nil {
		return err
	}

	var id string

	for _, usage := range usages[keyID] {
		if usage.Kind != "repository" || usage.ProjectID != "" {
			return errors.New("SSH key is still used by a project or machine")
		}

		id = usage.ID
	}

	if id == "" {
		return errors.New("SSH key is not a managed repository key")
	}

	var binding repositoryBinding
	if err := service.Store.Load("repository_binding", id, &binding); err != nil {
		return err
	}

	account, err := service.authenticated(ctx, binding.ConnectionID)
	if err != nil {
		return err
	}

	if binding.BaseURL != account.Public.BaseURL {
		return errors.New("reconnect the original Git service before revoking this key")
	}

	privateKey, err := service.Store.PrivateKey(keyID)
	if err != nil {
		return err
	}

	publicKey, err := remote.PublicKey(privateKey)
	if err != nil {
		return err
	}

	repository := domain.GitRepository{ID: binding.RepositoryID}
	keys, err := service.Client.deployKeys(ctx, account, repository)
	if err != nil {
		return err
	}

	for _, key := range keys {
		if publicKeyIdentity(key.PublicKey) == publicKeyIdentity(publicKey) {
			if err := service.Client.deleteDeployKey(ctx, account, repository, key.ID); err != nil {
				return err
			}
		}
	}

	if err := service.Store.Remove("repository_binding", id); err != nil {
		return err
	}

	return service.Store.DropPrivSSHKey(keyID)
}
