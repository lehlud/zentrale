package datastore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Credentials are deliberately separate from queryable JSON resources. Their
// encryption key is a private adjacent file, never another database setting.
func (i *Instance) SetCredential(name string, plaintext []byte) error {
	if i == nil || i.Closed() {
		return errClosed
	}

	box, err := i.credentialCipher()
	if err != nil {
		return err
	}

	ciphertext := box.Seal(nil, nil, plaintext, []byte("zentrale/credential/v1/"+name))
	_, err = i.db.Exec(`INSERT INTO credential_records VALUES (?,?)
		ON CONFLICT(name) DO UPDATE SET ciphertext=excluded.ciphertext`, name, ciphertext)
	return err
}

// SaveCredentialResource commits a private credential and its explicit public
// projection together. Readers cannot observe new public state with old tokens,
// and migrations replace a formerly private JSON resource in the same commit.
func (i *Instance) SaveCredentialResource(name string, plaintext []byte, kind, id, parent string, public any) error {
	if i == nil || i.Closed() {
		return errClosed
	}

	if name == "" || kind == "" || id == "" {
		return errors.New("credential name, resource kind and id are required")
	}

	data, err := json.Marshal(public)
	if err != nil {
		return err
	}

	box, err := i.credentialCipher()
	if err != nil {
		return err
	}

	ciphertext := box.Seal(nil, nil, plaintext, []byte("zentrale/credential/v1/"+name))
	tx, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO credential_records VALUES (?,?)
		ON CONFLICT(name) DO UPDATE SET ciphertext=excluded.ciphertext`, name, ciphertext); err != nil {
		return err
	}

	if _, err := tx.Exec(`INSERT INTO resources(kind,id,parent_id,data,created_at) VALUES (?,?,?,?,?)
		ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data,parent_id=excluded.parent_id`,
		kind, id, parent, string(data), resourceCreatedAt()); err != nil {
		return err
	}

	return tx.Commit()
}

func (i *Instance) Credential(name string) ([]byte, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}

	var ciphertext []byte
	if err := i.readDB().QueryRow("SELECT ciphertext FROM credential_records WHERE name=?", name).Scan(&ciphertext); err != nil {
		return nil, err
	}

	box, err := i.credentialCipher()
	if err != nil {
		return nil, err
	}

	plaintext, err := box.Open(nil, nil, ciphertext, []byte("zentrale/credential/v1/"+name))
	if err != nil {
		return nil, errors.New("credential cannot be decrypted; restore its original credential key")
	}

	return plaintext, nil
}

func (i *Instance) DeleteCredential(name string) error {
	if i == nil || i.Closed() {
		return errClosed
	}

	_, err := i.db.Exec("DELETE FROM credential_records WHERE name=?", name)
	return err
}

// Only key initialization is serialized. Database reads/writes and encryption
// do not hold a process-wide credential lock. A random-nonce AEAD authenticates the record
// name as well as its bytes, so moving ciphertext between names cannot work.
func (i *Instance) credentialCipher() (cipher.AEAD, error) {
	i.credentialMu.Lock()
	defer i.credentialMu.Unlock()
	if i.credentialKey == nil {
		key, err := i.loadCredentialKey()
		if err != nil {
			return nil, err
		}

		i.credentialKey = key
	}

	block, err := aes.NewCipher(i.credentialKey)
	if err != nil {
		return nil, err
	}

	return cipher.NewGCMWithRandomNonce(block)
}

func (i *Instance) loadCredentialKey() ([]byte, error) {
	if i.path == "" || i.path == ":memory:" {
		key := make([]byte, 32)
		_, err := rand.Read(key)
		return key, err
	}

	path, err := filepath.EvalSymlinks(i.path)
	if err != nil {
		return nil, err
	}
	path += ".credentials.key"

	key, err := readCredentialKey(path)
	if !errors.Is(err, os.ErrNotExist) {
		return key, err
	}

	var count int
	if err := i.readDB().QueryRow("SELECT count(*) FROM credential_records").Scan(&count); err != nil {
		return nil, err
	}

	if count != 0 {
		return nil, errors.New("credential key is missing; restore the original .credentials.key file alongside the database")
	}

	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}

	// Publish a complete, fsynced key without overwriting a concurrently created
	// key or following a symlink. A crash cannot publish a truncated key file.
	staging, err := os.CreateTemp(filepath.Dir(path), ".zentrale-credential-key-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(staging.Name())
	defer staging.Close()

	if _, err := staging.Write(key); err != nil {
		return nil, err
	}

	if err := staging.Sync(); err != nil {
		return nil, err
	}

	if err := staging.Close(); err != nil {
		return nil, err
	}

	if err := os.Link(staging.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}

	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer directory.Close()

	if err := directory.Sync(); err != nil {
		return nil, err
	}

	return readCredentialKey(path)
}

func readCredentialKey(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != 32 {
		return nil, errors.New("credential key must be a private regular 32-byte file (mode 0600)")
	}

	key, err := io.ReadAll(io.LimitReader(file, 33))
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid credential key file")
	}

	return key, nil
}
