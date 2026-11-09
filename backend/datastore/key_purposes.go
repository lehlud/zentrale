package datastore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/lehlud/zentrale/domain"
)

type SSHKeyClaim struct {
	KeyID              int
	Purpose, PurposeID string
}

type SSHKeyUsage struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId"`
}

// Collect references in one snapshot, including pending deploy-key registrations.
// A binding remains a use until the remote deploy key has actually been revoked.
func keyUsages(tx *sql.Tx) (map[int][]SSHKeyUsage, error) {
	rows, err := tx.Query(`SELECT kind, id, data::VARCHAR FROM resources WHERE kind IN ('machine','pool','source','repository_binding') ORDER BY kind,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	owners := map[int]map[string]SSHKeyUsage{}
	add := func(keyID int, usage SSHKeyUsage) {
		if keyID <= 0 {
			return
		}

		if owners[keyID] == nil {
			owners[keyID] = map[string]SSHKeyUsage{}
		}

		owners[keyID][usage.Kind+":"+usage.ID+":"+usage.ProjectID] = usage
	}

	for rows.Next() {
		var kind, id, data string
		if err := rows.Scan(&kind, &id, &data); err != nil {
			return nil, err
		}

		switch kind {
		case "machine":
			var machine domain.Machine
			if err := json.Unmarshal([]byte(data), &machine); err != nil {
				return nil, err
			}

			add(machine.KeyID, SSHKeyUsage{Kind: "machine", ID: id, Name: machine.Name})
		case "pool":
			var pool domain.Pool
			if err := json.Unmarshal([]byte(data), &pool); err != nil {
				return nil, err
			}

			if pool.KeyID > 0 {
				add(pool.KeyID, SSHKeyUsage{Kind: "pool", ID: id, Name: pool.Name})
			}

			for _, machine := range pool.Machines {
				if machine.KeyID == pool.KeyID {
					continue
				}

				add(
					machine.KeyID,
					SSHKeyUsage{
						Kind: "machine",
						ID:   id + "/" + machine.ID,
						Name: pool.Name + " / " + machine.Name,
					},
				)
			}
		case "source":
			var source domain.Source
			if err := json.Unmarshal([]byte(data), &source); err != nil {
				return nil, err
			}

			purpose, owner := "source", id

			if source.BindingID != "" {
				purpose, owner = "repository", source.BindingID
			}

			name := source.Name

			if source.RepositoryName != "" {
				name = source.RepositoryName
			}

			add(source.KeyID, SSHKeyUsage{Kind: purpose, ID: owner, Name: name, ProjectID: source.ProjectID})
		case "repository_binding":
			var binding struct {
				KeyID     int    `json:"keyId"`
				ProjectID string `json:"projectId"`
				FullName  string `json:"fullName"`
			}
			if err := json.Unmarshal([]byte(data), &binding); err != nil {
				return nil, err
			}

			add(
				binding.KeyID,
				SSHKeyUsage{
					Kind:      "repository",
					ID:        id,
					Name:      binding.FullName,
					ProjectID: binding.ProjectID,
				},
			)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := map[int][]SSHKeyUsage{}
	for id, uses := range owners {
		for _, usage := range uses {
			result[id] = append(result[id], usage)
		}

		sort.Slice(result[id], func(first, second int) bool {
			a, b := result[id][first], result[id][second]

			return a.Kind+":"+a.ID+":"+a.ProjectID < b.Kind+":"+b.ID+":"+b.ProjectID
		})
	}

	return result, nil
}

func (instance *Instance) SSHKeyUsages() (map[int][]SSHKeyUsage, error) {
	if instance == nil || instance.Closed() {
		return nil, errClosed
	}

	tx, err := instance.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	usages, err := keyUsages(tx)
	if err != nil {
		return nil, err
	}

	return usages, tx.Commit()
}

// Claim and save together: concurrent allocations cannot share a key, and key
// deletion cannot race a newly saved reference. A key has exactly one purpose.
func (instance *Instance) SaveWithSSHKeys(kind, id, parent string, value any, claims ...SSHKeyClaim) error {
	if instance == nil || instance.Closed() {
		return errClosed
	}

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	tx, err := instance.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	usages, err := keyUsages(tx)
	if err != nil {
		return err
	}

	claimed := map[int]SSHKeyClaim{}
	for _, claim := range claims {
		if previous, found := claimed[claim.KeyID]; found && previous != claim {
			return errors.New("SSH key cannot have two purposes")
		}

		claimed[claim.KeyID] = claim
		var purpose, owner string
		if err := tx.QueryRow(`SELECT purpose,purpose_id FROM private_ssh_keys WHERE id=?`, claim.KeyID).Scan(&purpose, &owner); err != nil {
			return fmt.Errorf("unknown SSH key: %w", err)
		}

		if len(usages[claim.KeyID]) > 0 &&
			purpose != "" &&
			(purpose != claim.Purpose ||
				owner != claim.PurposeID) {
			return errors.New("SSH key is assigned to another purpose")
		}

		for _, usage := range usages[claim.KeyID] {
			if usage.Kind != claim.Purpose || usage.ID != claim.PurposeID {
				return errors.New("SSH key is already used for another purpose")
			}
		}

		if _, err := tx.Exec(
			`UPDATE private_ssh_keys SET purpose=?,purpose_id=? WHERE id=?`,
			claim.Purpose,
			claim.PurposeID,
			claim.KeyID,
		); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(
		`INSERT INTO resources(kind,id,parent_id,data) VALUES (?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data,parent_id=excluded.parent_id`,
		kind,
		id,
		parent,
		string(data),
	); err != nil {
		return err
	}

	return tx.Commit()
}

func (instance *Instance) dropUnusedSSHKey(id int) error {
	if instance == nil || instance.Closed() {
		return errClosed
	}

	tx, err := instance.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	usages, err := keyUsages(tx)
	if err != nil {
		return err
	}

	if len(usages[id]) > 0 {
		return errors.New("SSH key is still referenced")
	}

	if _, err := tx.Exec(`DELETE FROM private_ssh_keys WHERE id=?`, id); err != nil {
		return err
	}

	return tx.Commit()
}
