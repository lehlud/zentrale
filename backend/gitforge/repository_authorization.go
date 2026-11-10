package gitforge

import (
	"context"
	"errors"
	"slices"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

// RefreshRepositoryAuthorization re-registers an existing managed GitHub key
// after workflow access is granted. GitHub can restrict an OAuth-created deploy
// key even when Git itself uses SSH. Keep its private identity so existing
// agent/editor checkouts use the updated registration without replacing files.
func (service *Service) RefreshRepositoryAuthorization(ctx context.Context, bindingID, sshURL string) error {
	if bindingID == "" {
		return nil
	}
	service.mu.Lock()
	defer service.mu.Unlock()

	var binding repositoryBinding
	if err := service.Store.Load("repository_binding", bindingID, &binding); err != nil {
		return err
	}
	if binding.SSHURL != sshURL {
		return errors.New("repository binding does not match its SSH source")
	}
	if binding.ConnectionID != "github" ||
		(slices.Contains(binding.Scopes, "workflow") && binding.State != "reauthorizing") {
		return nil
	}

	account, err := service.account(binding.ConnectionID)
	if err != nil {
		return err
	}
	// Ordinary SSH access stays available while an old account awaits consent.
	// No provider request is needed for an already current repository key.
	if !slices.Contains(account.Scopes, "workflow") || account.AccessToken == "" {
		return nil
	}
	account, err = service.authenticated(ctx, binding.ConnectionID)
	if err != nil {
		return err
	}
	if !slices.Contains(account.Scopes, "workflow") {
		return errors.New("GitHub workflow permission was not granted; sign in again and approve workflow access")
	}
	if account.Public.BaseURL != binding.BaseURL {
		return errors.New("reconnect the original Git service before updating repository authorization")
	}
	return service.reauthorizeBinding(ctx, &binding, account)
}

func (service *Service) reauthorizeBinding(ctx context.Context, binding *repositoryBinding, account account) error {
	privateKey, err := service.Store.PrivateKey(binding.KeyID)
	if err != nil {
		return err
	}
	publicKey, err := remote.PublicKey(privateKey)
	if err != nil {
		return err
	}
	repository := domain.GitRepository{ID: binding.RepositoryID, FullName: binding.FullName}
	keys, err := service.Client.deployKeys(ctx, account, repository)
	if err != nil {
		return err
	}

	if binding.State != "reauthorizing" {
		// Remember the old registration before deleting it. A retry can then
		// recognize a successful replacement whose HTTP response was lost.
		binding.DeployKeyID = 0
		for _, key := range keys {
			if publicKeyIdentity(key.PublicKey) == publicKeyIdentity(publicKey) {
				binding.DeployKeyID = key.ID
				break
			}
		}
		binding.State = "reauthorizing"
		binding.Scopes = append([]string(nil), account.Scopes...)
		if err := service.Store.Save("repository_binding", binding.ID, "", binding); err != nil {
			return err
		}
	}

	registered := false
	for _, key := range keys {
		if publicKeyIdentity(key.PublicKey) != publicKeyIdentity(publicKey) {
			continue
		}
		if key.ID != binding.DeployKeyID && key.Writable {
			binding.DeployKeyID, registered = key.ID, true
			break
		}
		if err := service.Client.deleteDeployKey(ctx, account, repository, key.ID); err != nil {
			return err
		}
	}
	if !registered {
		key, err := service.Client.createDeployKey(ctx, account, repository, publicKey)
		if err != nil {
			return err
		}
		binding.DeployKeyID = key.ID
	}
	binding.State = "active"
	return service.Store.Save("repository_binding", binding.ID, "", binding)
}
