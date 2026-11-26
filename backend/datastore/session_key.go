package datastore

import (
	"crypto/rand"
	"database/sql"
	"errors"
)

const sessionKeyCredential = "auth/session-signing-key"

// SessionSigningKey persists one installation-wide key in the encrypted
// credential store. Initial creation and legacy session invalidation are atomic.
func (i *Instance) SessionSigningKey() ([]byte, error) {
	key, err := i.Credential(sessionKeyCredential)
	if !errors.Is(err, sql.ErrNoRows) {
		return key, err
	}

	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	box, err := i.credentialCipher()
	if err != nil {
		return nil, err
	}
	ciphertext := box.Seal(nil, nil, key, []byte("zentrale/credential/v1/"+sessionKeyCredential))

	tx, err := i.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	inserted, err := tx.Exec(`INSERT INTO credential_records(name,ciphertext) VALUES (?,?)
		ON CONFLICT(name) DO NOTHING`, sessionKeyCredential, ciphertext)
	if err != nil {
		return nil, err
	}
	affected, err := inserted.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 1 {
		if _, err := tx.Exec("DELETE FROM user_sessions"); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return i.Credential(sessionKeyCredential)
}
