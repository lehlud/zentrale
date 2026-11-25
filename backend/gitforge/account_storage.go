package gitforge

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"github.com/lehlud/zentrale/domain"
)

const maxGitConnections = 34

// The ordinary resource retains only public snapshots. Private credentials and
// the snapshot are committed together, including when an existing connection
// first moves from its previous JSON record to encrypted storage.
type accountSnapshot struct {
	Public         domain.GitConnection `json:"public"`
	Login          *loginTransaction    `json:"login,omitempty"`
	StorageVersion int                  `json:"storageVersion"`
}

func isForge(kind string) bool {
	return kind == "gitea" || kind == "forgejo"
}

func (service *Service) loadAccount(id string, value *account) error {
	return service.loadAccountMode(id, value, false)
}

func (service *Service) accountKey(id string) string {
	if service.owner == "" {
		return id
	}
	return service.owner + "/" + id
}

func (service *Service) loadAccountMode(id string, value *account, readOnly bool) error {
	key := service.accountKey(id)
	data, err := service.Store.Credential("git_account/" + key)
	if err == nil {
		return json.Unmarshal(data, value)
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var snapshot accountSnapshot
	if err := service.Store.Load("git_account", key, &snapshot); err != nil {
		return err
	}

	if snapshot.StorageVersion != 0 {
		return errors.New("Git account credential is missing; restore the matching credential backup")
	}

	if err := service.Store.Load("git_account", key, value); err != nil {
		return err
	}

	if readOnly {
		return nil
	}
	return service.save(*value)
}

func (service *Service) save(value account) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	snapshot := accountSnapshot{Public: value.Public, StorageVersion: 1}
	if value.Login != nil {
		snapshot.Login = &loginTransaction{Public: value.Login.Public}
	}

	key := service.accountKey(value.Public.ID)
	return service.Store.SaveCredentialResource(
		"git_account/"+key, data,
		"git_account", key, service.owner, snapshot,
	)
}

func (service *Service) connectionIDs() ([]string, error) {
	rows, err := service.Store.List("git_account", &service.owner, maxGitConnections+1, 0)
	if err != nil {
		return nil, err
	}

	if len(rows) > maxGitConnections {
		return nil, errors.New("Git connection limit exceeded")
	}

	ids := []string{"github", "gitlab"}
	for _, row := range rows {
		public, _ := row["public"].(map[string]any)
		id := text(public["id"])
		if id != "" && id != "github" && id != "gitlab" {
			ids = append(ids, id)
		}
	}

	return ids, nil
}

func (service *Service) accountForConfiguration(input domain.GitConnectionConfig) (account, error) {
	if input.Kind != "github" && input.Kind != "gitlab" && !isForge(input.Kind) {
		return account{}, errors.New("unknown Git service kind")
	}

	if len(input.Name) > 100 || strings.IndexFunc(input.Name, unicode.IsControl) >= 0 {
		return account{}, errors.New("Git connection name must contain at most 100 characters without control characters")
	}

	id := input.ID
	if id == "" && !isForge(input.Kind) {
		id = input.Kind
	}

	if id != "" {
		value, err := service.account(id)
		if err == nil && value.Public.Kind != input.Kind {
			return value, errors.New("Git connection kind cannot be changed")
		}

		return value, err
	}

	ids, err := service.connectionIDs()
	if err != nil {
		return account{}, err
	}

	if len(ids) >= maxGitConnections {
		return account{}, errors.New("at most 32 additional Git instances can be configured")
	}

	return account{Public: domain.GitConnection{ID: domain.NewID(), Kind: input.Kind}}, nil
}
