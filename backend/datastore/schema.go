package datastore

import (
	"database/sql"
	"fmt"
)

// One unreleased application, one current schema. There are no historical
// upgrade or downgrade paths. Opening an existing current database is idempotent;
// an incompatible development schema fails without changing or deleting data.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS private_ssh_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    x509_data BLOB,
    purpose VARCHAR NOT NULL DEFAULT '',
    purpose_id VARCHAR NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS known_ssh_keys (x509_data BLOB PRIMARY KEY);
CREATE TABLE IF NOT EXISTS settings (name VARCHAR PRIMARY KEY, value VARCHAR NOT NULL);
CREATE TABLE IF NOT EXISTS credential_records (name VARCHAR PRIMARY KEY, ciphertext BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (token_hash VARCHAR PRIMARY KEY, expires_at TIMESTAMP NOT NULL);
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN ('admin','user')),
    disabled BOOLEAN NOT NULL DEFAULT 0, can_create_projects BOOLEAN NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0 CHECK(monthly_token_limit >= 0),
    monthly_cost_limit REAL NOT NULL DEFAULT 0 CHECK(monthly_cost_limit >= 0),
    password_hash TEXT NOT NULL DEFAULT '', oidc_issuer TEXT NOT NULL DEFAULT '', oidc_subject TEXT NOT NULL DEFAULT '',
    CHECK((password_hash <> '' AND oidc_issuer = '' AND oidc_subject = '') OR
          (password_hash = '' AND oidc_issuer <> '' AND oidc_subject <> ''))
);
CREATE UNIQUE INDEX IF NOT EXISTS users_oidc_identity ON users(oidc_issuer,oidc_subject) WHERE oidc_issuer <> '';
CREATE TABLE IF NOT EXISTS user_sessions (
    token_hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS user_sessions_account ON user_sessions(user_id);
CREATE TABLE IF NOT EXISTS attachment_blobs (
    agent_id VARCHAR NOT NULL, project_id VARCHAR NOT NULL, id VARCHAR NOT NULL,
    metadata TEXT NOT NULL CHECK(json_valid(metadata)), bytes BLOB NOT NULL,
    PRIMARY KEY(agent_id,id)
);
CREATE TABLE IF NOT EXISTS short_chat_documents (
    id TEXT PRIMARY KEY, name TEXT NOT NULL, bytes BLOB NOT NULL,
    size INTEGER NOT NULL, created_at TIMESTAMP NOT NULL
);
CREATE VIRTUAL TABLE IF NOT EXISTS short_chat_reference_sections USING fts4(
    document_id, section, page, text, title,
    notindexed=document_id, notindexed=section, notindexed=page,
    tokenize=unicode61 "remove_diacritics=2", prefix="3"
);
CREATE TABLE IF NOT EXISTS resources (
    kind VARCHAR NOT NULL,
    id VARCHAR NOT NULL,
    parent_id VARCHAR NOT NULL DEFAULT '',
    data TEXT NOT NULL CHECK(json_valid(data)),
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00','now')),
    PRIMARY KEY(kind, id)
);
CREATE INDEX IF NOT EXISTS resource_parent ON resources(kind,parent_id,created_at,id);
CREATE INDEX IF NOT EXISTS resource_owner ON resources(parent_id,kind);
CREATE INDEX IF NOT EXISTS resource_agent ON resources(json_extract(data,'$.agentId'));
CREATE INDEX IF NOT EXISTS resource_project ON resources(json_extract(data,'$.projectId'));
CREATE INDEX IF NOT EXISTS resource_recovery ON resources(kind,parent_id,created_at,id) WHERE ` + recoveryResources + `;
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type VARCHAR NOT NULL,
    agent_id VARCHAR NOT NULL DEFAULT '',
    data VARCHAR NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00','now'))
);
CREATE INDEX IF NOT EXISTS event_owner ON events(agent_id,id);
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
		"SELECT name,ciphertext FROM credential_records LIMIT 0",
		"SELECT token_hash,expires_at FROM sessions LIMIT 0",
		"SELECT " + accountColumns + " FROM users LIMIT 0",
		"SELECT token_hash,user_id,expires_at FROM user_sessions LIMIT 0",
		"SELECT kind,id,parent_id,data,created_at FROM resources LIMIT 0",
		"SELECT id,type,agent_id,data,created_at FROM events LIMIT 0",
		"SELECT agent_id,project_id,id,metadata,bytes FROM attachment_blobs LIMIT 0",
		"SELECT id,name,bytes,size,created_at FROM short_chat_documents LIMIT 0",
	} {
		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("incompatible development database schema; no data was changed: %w", err)
		}
	}

	return tx.Commit()
}
