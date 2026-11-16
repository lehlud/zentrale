//go:build duckdb_import

package datastore

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestImportDuckDBPreservesDataCredentialsAndWatermarks(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.duckdb")
	target := filepath.Join(directory, "target.sqlite")
	old, err := sql.Open("duckdb", source)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	_, err = old.Exec(`
 CREATE SEQUENCE private_ssh_key_ids START 80;
 CREATE SEQUENCE event_ids START 100;
 CREATE TABLE private_ssh_keys(id INTEGER DEFAULT nextval('private_ssh_key_ids'),x509_data BLOB,purpose VARCHAR,purpose_id VARCHAR);
 CREATE TABLE known_ssh_keys(x509_data BLOB);
 CREATE TABLE settings(name VARCHAR,value VARCHAR);
 CREATE TABLE credential_records(name VARCHAR,ciphertext BLOB);
 CREATE TABLE sessions(token_hash VARCHAR,expires_at TIMESTAMPTZ);
 CREATE TABLE attachment_blobs(agent_id VARCHAR,project_id VARCHAR,id VARCHAR,metadata JSON,bytes BLOB);
 CREATE TABLE resources(kind VARCHAR,id VARCHAR,parent_id VARCHAR,data JSON,created_at TIMESTAMPTZ);
 CREATE TABLE events(id BIGINT DEFAULT nextval('event_ids'),type VARCHAR,agent_id VARCHAR,data VARCHAR,created_at TIMESTAMPTZ);
 INSERT INTO private_ssh_keys(x509_data,purpose,purpose_id) VALUES ('temporary','','');
 DELETE FROM private_ssh_keys;
 INSERT INTO private_ssh_keys VALUES (7,'retained','machine','host');
 INSERT INTO events(type,agent_id,data,created_at) VALUES ('temporary','','{}',now());
 DELETE FROM events;
 INSERT INTO events VALUES (5,'test','agent','{}',now());
 INSERT INTO known_ssh_keys VALUES ('trusted');
 INSERT INTO settings VALUES ('identity','retained');
 INSERT INTO sessions VALUES ('session','2030-01-01T03:00:00+03:00');
 INSERT INTO attachment_blobs VALUES ('agent','project','file','{"id":"file","name":"é.txt"}','exact binary');
 INSERT INTO resources VALUES ('agent','agent','project','{"id":"agent","enabled":true,"items":[1,null,"é"]}','2026-01-01T03:04:05.123456+03:00');`)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{37}, 32)
	block, _ := aes.NewCipher(key)
	box, _ := cipher.NewGCMWithRandomNonce(block)
	ciphertext := box.Seal(nil, nil, []byte("private credential"), []byte("zentrale/credential/v1/account"))
	if _, err := old.Exec("INSERT INTO credential_records VALUES (?,?)", "account", ciphertext); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source+".credentials.key", key, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := ImportDuckDB(context.Background(), source, target)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"resources", "credential_records", "events", "private_ssh_keys", "attachment_blobs", "sessions", "settings", "known_ssh_keys"} {
		if counts[table] != 1 {
			t.Fatal(table, counts)
		}
	}
	after, err := os.ReadFile(source)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("source changed", err)
	}
	store, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	plaintext, err := store.Credential("account")
	if err != nil || string(plaintext) != "private credential" {
		t.Fatal("credential lost", err)
	}
	var resource map[string]any
	if err := store.Load("agent", "agent", &resource); err != nil || resource["enabled"] != true {
		t.Fatal(resource, err)
	}
	var blob []byte
	if err := store.db.QueryRow("SELECT bytes FROM attachment_blobs").Scan(&blob); err != nil || string(blob) != "exact binary" {
		t.Fatal(blob, err)
	}
	var instant time.Time
	if err := store.db.QueryRow("SELECT created_at FROM resources WHERE kind='agent'").Scan(&instant); err != nil || instant.UTC().Format(time.RFC3339Nano) != "2026-01-01T00:04:05.123456Z" {
		t.Fatal(instant, err)
	}
	event, err := store.AppendEvent("new", "", "{}")
	if err != nil || event.ID != 101 {
		t.Fatal("event watermark lost", event, err)
	}
	newKey, err := store.GenerateSSHKey()
	if err != nil || newKey.ID <= 80 {
		t.Fatal("SSH key sequence lost", newKey, err)
	}
	if _, err := ImportDuckDB(context.Background(), source, target); err == nil {
		t.Fatal("overwrote existing database")
	}
	if err := os.Remove(source + ".credentials.key"); err != nil {
		t.Fatal(err)
	}
	missingKeyTarget := filepath.Join(directory, "missing-key.sqlite")
	if _, err := ImportDuckDB(context.Background(), source, missingKeyTarget); err == nil {
		t.Fatal("lost original credential key silently")
	}
	if _, err := os.Stat(missingKeyTarget); !os.IsNotExist(err) {
		t.Fatal("published incomplete import", err)
	}
}
