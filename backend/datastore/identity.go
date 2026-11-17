package datastore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// InstallationIdentity survives service/host restarts and is shared by all
// connectors using this database. A different installation gets its own daemon.
func (i *Instance) InstallationIdentity() (string, string, error) {
	if i == nil || i.Closed() {
		return "", "", errClosed
	}
	i.identityOnce.Do(func() { i.identityID, i.identityToken, i.identityError = i.initializeInstallationIdentity() })
	return i.identityID, i.identityToken, i.identityError
}

func (i *Instance) initializeInstallationIdentity() (string, string, error) {
	box, err := i.credentialCipher()
	if err != nil {
		return "", "", err
	}
	idBytes, secretBytes := make([]byte, 16), make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return "", "", err
	}
	if _, err := rand.Read(secretBytes); err != nil {
		return "", "", err
	}
	id, secret := hex.EncodeToString(idBytes), hex.EncodeToString(secretBytes)
	const name = "installation/daemon-token"
	ciphertext := box.Seal(nil, nil, []byte(secret), []byte("zentrale/credential/v1/"+name))
	tx, err := i.db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO settings(name,value) VALUES ('installation-id',?) ON CONFLICT(name) DO NOTHING", id); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec("INSERT INTO credential_records(name,ciphertext) VALUES (?,?) ON CONFLICT(name) DO NOTHING", name, ciphertext); err != nil {
		return "", "", err
	}
	if err := tx.QueryRow("SELECT value FROM settings WHERE name='installation-id'").Scan(&id); err != nil {
		return "", "", err
	}
	if err := tx.QueryRow("SELECT ciphertext FROM credential_records WHERE name=?", name).Scan(&ciphertext); err != nil {
		return "", "", err
	}
	plaintext, err := box.Open(nil, nil, ciphertext, []byte("zentrale/credential/v1/"+name))
	if err != nil {
		return "", "", errors.New("installation daemon identity cannot be decrypted")
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return id, string(plaintext), nil
}
