package datastore

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentSchemaReopensWithoutMigrationsOrDataChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.duckdb")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting("example", "retained"); err != nil {
		t.Fatal(err)
	}
	var migrations int
	if err := store.db.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_name='__migrations'").Scan(&migrations); err != nil || migrations != 0 {
		t.Fatal("historical migrations were initialized", migrations, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if value, err := store.Setting("example"); err != nil || value != "retained" {
		t.Fatal(value, err)
	}
	if _, err := store.PrivateKey(key.ID); err != nil {
		t.Fatal("reopen lost key", err)
	}
	next, err := store.GenerateSSHKey()
	if err != nil || next.ID <= key.ID {
		t.Fatal("reopen reset the key sequence", next.ID, err)
	}
}

func TestIncompatibleDevelopmentSchemaFailsAtomically(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE private_ssh_keys(id INTEGER); INSERT INTO private_ssh_keys VALUES (42)"); err != nil {
		t.Fatal(err)
	}
	if err := initializeSchema(db); err == nil || !strings.Contains(err.Error(), "incompatible development database") {
		t.Fatal(err)
	}
	var retained, created int
	if err := db.QueryRow("SELECT id FROM private_ssh_keys").Scan(&retained); err != nil || retained != 42 {
		t.Fatal("failed initialization changed data", retained, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_name='resources'").Scan(&created); err != nil || created != 0 {
		t.Fatal("failed initialization left partial tables", created, err)
	}
}
