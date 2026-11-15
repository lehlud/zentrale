package datastore

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialIsEncryptedSeparateAndNameBound(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	secret := []byte("hf_privateCredentialNeverAResource")
	if err := store.SetCredential("huggingface", secret); err != nil {
		t.Fatal(err)
	}

	value, err := store.Credential("huggingface")
	if err != nil || !bytes.Equal(value, secret) {
		t.Fatalf("credential round trip: %q %v", value, err)
	}

	var ciphertext []byte
	if err := store.db.QueryRow("SELECT ciphertext FROM credential_records WHERE name='huggingface'").Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(ciphertext, secret) {
		t.Fatal("plaintext reached the credential table")
	}

	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM resources").Scan(&count); err != nil || count != 0 {
		t.Fatal("credential became a queryable JSON resource", count, err)
	}

	if _, err := store.db.Exec("INSERT INTO credential_records VALUES ('other', ?)", ciphertext); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Credential("other"); err == nil {
		t.Fatal("ciphertext was transferable between record names")
	}

	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := store.db.Exec("UPDATE credential_records SET ciphertext=? WHERE name='huggingface'", ciphertext); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Credential("huggingface"); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}

	if err := store.DeleteCredential("huggingface"); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Credential("huggingface"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted credential remains available", err)
	}
}

func TestCredentialAndPublicProjectionCommitTogether(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	secret := []byte("private-oauth-refresh-token")
	if err := store.SaveCredentialResource("git/account", secret, "git_account", "account", "", map[string]any{"connected": true}); err != nil {
		t.Fatal(err)
	}

	var public map[string]any
	if err := store.Load("git_account", "account", &public); err != nil || public["connected"] != true {
		t.Fatal("public projection missing", public, err)
	}

	encoded, _ := json.Marshal(public)
	if bytes.Contains(encoded, secret) {
		t.Fatal("credential appeared in public JSON")
	}

	private, err := store.Credential("git/account")
	if err != nil || !bytes.Equal(private, secret) {
		t.Fatal("private credential missing", err)
	}

	// Force the second SQL statement to fail in this isolated test database.
	// The credential insert must roll back with the failed public write.
	if _, err := store.db.Exec("DROP TABLE resources"); err != nil {
		t.Fatal(err)
	}

	if err := store.SaveCredentialResource("git/failed", secret, "git_account", "failed", "", public); err == nil {
		t.Fatal("failed resource write reported success")
	}

	if _, err := store.Credential("git/failed"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed transaction retained its credential", err)
	}
}

func TestCredentialKeyPersistsAndMissingKeyFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account.duckdb")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetCredential("huggingface", []byte("secret")); err != nil {
		store.Close()
		t.Fatal(err)
	}

	keyPath := path + ".credentials.key"
	info, err := os.Stat(keyPath)
	if err != nil || info.Mode().Perm() != 0600 || info.Size() != 32 {
		store.Close()
		t.Fatal("credential key is not private", info, err)
	}

	store.Close()
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}

	value, err := store.Credential("huggingface")
	if err != nil || string(value) != "secret" {
		store.Close()
		t.Fatal("credential was lost after reopening", err)
	}

	store.Close()
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}

	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	if _, err := store.Credential("huggingface"); err == nil {
		t.Fatal("missing key did not fail closed")
	}

	if err := store.SetCredential("another", []byte("secret")); err == nil {
		t.Fatal("missing key was silently replaced")
	}

	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("replacement key was created", err)
	}
}

func TestCredentialKeyRejectsSymlinkAndPublicPermissions(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{42}, 32), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := readCredentialKey(path); err == nil {
		t.Fatal("world-readable key was accepted")
	}

	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(directory, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}

	if _, err := readCredentialKey(link); err == nil {
		t.Fatal("symlink key was accepted")
	}
}
