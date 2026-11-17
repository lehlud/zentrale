package datastore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Labels are public resource metadata, independent of private key material and
// purpose ownership. Existing databases need no key-table migration.
func (i *Instance) RenameSSHKey(id int, name string) error {
	if i.Closed() {
		return errClosed
	}
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 200 || strings.ContainsFunc(name, unicode.IsControl) {
		return errors.New("SSH key name must be at most 200 characters without control characters")
	}
	tx, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM private_ssh_keys WHERE id=?)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	keyID := strconv.Itoa(id)
	if name == "" {
		_, err = tx.Exec(`DELETE FROM resources WHERE kind='ssh_key_name' AND id=?`, keyID)
	} else {
		data, _ := json.Marshal(map[string]string{"name": name})
		_, err = tx.Exec(`INSERT INTO resources(kind,id,data) VALUES('ssh_key_name',?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`, keyID, string(data))
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (i *Instance) SSHKeyNames() (map[int]string, error) {
	if i.Closed() {
		return nil, errClosed
	}
	rows, err := i.readDB().Query(`SELECT id,json_extract(data,'$.name') FROM resources WHERE kind='ssh_key_name'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := make(map[int]string)
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		number, err := strconv.Atoi(id)
		if err != nil {
			return nil, err
		}
		names[number] = name
	}
	return names, rows.Err()
}
