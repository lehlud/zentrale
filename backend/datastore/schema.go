package datastore

import (
	"database/sql"
	"fmt"
)

// One unreleased application, one current schema. There are no historical
// upgrade or downgrade paths. Opening an existing current database is idempotent;
// an incompatible development schema fails without changing or deleting data.
const schemaSQL = `
CREATE SEQUENCE IF NOT EXISTS private_ssh_key_ids START 1;
CREATE TABLE IF NOT EXISTS private_ssh_keys (
    id INTEGER PRIMARY KEY DEFAULT nextval('private_ssh_key_ids'),
    x509_data BLOB,
    purpose VARCHAR NOT NULL DEFAULT '',
    purpose_id VARCHAR NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS known_ssh_keys (x509_data BLOB PRIMARY KEY);
CREATE TABLE IF NOT EXISTS settings (name VARCHAR PRIMARY KEY, value VARCHAR NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (token_hash VARCHAR PRIMARY KEY, expires_at TIMESTAMPTZ NOT NULL);
CREATE TABLE IF NOT EXISTS resources (
    kind VARCHAR NOT NULL,
    id VARCHAR NOT NULL,
    parent_id VARCHAR NOT NULL DEFAULT '',
    data JSON NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(kind, id)
);
CREATE INDEX IF NOT EXISTS resource_parent ON resources(kind, parent_id);
CREATE SEQUENCE IF NOT EXISTS event_ids START 1;
CREATE TABLE IF NOT EXISTS events (
    id BIGINT PRIMARY KEY DEFAULT nextval('event_ids'),
    type VARCHAR NOT NULL,
    agent_id VARCHAR NOT NULL DEFAULT '',
    data VARCHAR NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

func initializeSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin schema initialization: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schemaSQL); err != nil {
		return fmt.Errorf("initialize current schema: %w", err)
	}
	// CREATE IF NOT EXISTS does not validate pre-existing column layouts.
	for _, query := range []string{
		"SELECT id,x509_data,purpose,purpose_id FROM private_ssh_keys LIMIT 0",
		"SELECT x509_data FROM known_ssh_keys LIMIT 0",
		"SELECT name,value FROM settings LIMIT 0",
		"SELECT token_hash,expires_at FROM sessions LIMIT 0",
		"SELECT kind,id,parent_id,data,created_at FROM resources LIMIT 0",
		"SELECT id,type,agent_id,data,created_at FROM events LIMIT 0",
	} {
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("incompatible development database schema; no data was changed: %w", err)
		}
	}
	return tx.Commit()
}
