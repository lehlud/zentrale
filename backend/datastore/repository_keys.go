package datastore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/lehlud/zentrale/domain"
)

const checkoutHistoryKind = "repository_checkout_history"

// PreviousCheckoutOrigins are repository choices explicitly replaced in the
// project settings. They are only used to preserve and retire the old checkout.
func (instance *Instance) PreviousCheckoutOrigins(projectID string) ([]string, error) {
	var origins []string
	err := instance.Load(checkoutHistoryKind, projectID, &origins)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return origins, err
}

func rememberPreviousCheckout(tx *sql.Tx, projectID string, source *domain.Source) error {
	var origin string
	err := tx.QueryRow(`SELECT json_extract(data,'$.url') FROM resources
		WHERE kind='source' AND parent_id=? AND json_extract(data,'$.projectRoot')=1`, projectID).Scan(&origin)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (origin == "" || (source != nil && source.URL == origin))) {
		return nil
	}
	if err != nil {
		return err
	}
	var encoded string
	var history []string
	err = tx.QueryRow(`SELECT data FROM resources WHERE kind=? AND id=?`, checkoutHistoryKind, projectID).Scan(&encoded)
	if err == nil {
		if err := json.Unmarshal([]byte(encoded), &history); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if slices.Contains(history, origin) {
		return nil
	}
	data, err := json.Marshal(append(history, origin))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO resources(kind,id,parent_id,data,created_at) VALUES (?,?,?,?,?)
		ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`, checkoutHistoryKind, projectID, projectID, string(data), resourceCreatedAt())
	return err
}

// SaveProjectRepository switches the root source and project selection as one
// change. A failed remote key registration never replaces the previous choice.
func (instance *Instance) SaveProjectRepository(project domain.Project, source *domain.Source) error {
	tx, err := instance.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := rememberPreviousCheckout(tx, project.ID, source); err != nil {
		return err
	}

	if source != nil && source.BindingID == "" {
		if err := claimSSHKeys(tx, SSHKeyClaim{KeyID: source.KeyID, Purpose: "source", PurposeID: source.ID}); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(
		`DELETE FROM resources WHERE kind='source' AND parent_id=? AND json_extract(data,'$.projectRoot')=1`,
		project.ID,
	); err != nil {
		return err
	}

	if source != nil {
		if source.BindingID != "" {
			var keyID int
			if err := tx.QueryRow(
				`SELECT CAST(json_extract(data,'$.keyId') AS INTEGER) FROM resources WHERE kind='repository_binding' AND id=?`,
				source.BindingID,
			).Scan(&keyID); err != nil {
				return err
			}

			if keyID != source.KeyID {
				return errors.New("repository key changed; retry")
			}
		}

		encoded, err := json.Marshal(source)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(
			`INSERT INTO resources(kind,id,parent_id,data,created_at) VALUES ('source',?,?,?,?)`,
			source.ID,
			project.ID,
			string(encoded),
			resourceCreatedAt(),
		); err != nil {
			return err
		}
	}

	encoded, err := json.Marshal(project)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(
		`INSERT INTO resources(kind,id,parent_id,data,created_at) VALUES ('project',?,'',?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`,
		project.ID,
		string(encoded),
		resourceCreatedAt(),
	); err != nil {
		return err
	}

	return tx.Commit()
}
