package datastore

import (
	"encoding/json"
	"errors"

	"github.com/lehlud/zentrale/domain"
)

// SaveProjectRepository switches the root source and project selection as one
// change. A failed remote key registration never replaces the previous choice.
func (instance *Instance) SaveProjectRepository(project domain.Project, source *domain.Source) error {
	tx, err := instance.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`DELETE FROM resources WHERE kind='source' AND parent_id=? AND json_extract_string(data,'$.projectRoot')='true'`,
		project.ID,
	); err != nil {
		return err
	}

	if source != nil {
		var keyID int
		if err := tx.QueryRow(
			`SELECT CAST(json_extract_string(data,'$.keyId') AS INTEGER) FROM resources WHERE kind='repository_binding' AND id=?`,
			source.BindingID,
		).Scan(&keyID); err != nil {
			return err
		}

		if keyID != source.KeyID {
			return errors.New("repository key changed; retry")
		}

		encoded, err := json.Marshal(source)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(
			`INSERT INTO resources(kind,id,parent_id,data) VALUES ('source',?,?,?)`,
			source.ID,
			project.ID,
			string(encoded),
		); err != nil {
			return err
		}
	}

	encoded, err := json.Marshal(project)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(
		`INSERT INTO resources(kind,id,parent_id,data) VALUES ('project',?,'',?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`,
		project.ID,
		string(encoded),
	); err != nil {
		return err
	}

	return tx.Commit()
}
