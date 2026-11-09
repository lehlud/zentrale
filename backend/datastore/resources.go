package datastore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

var ErrClosed = errClosed

// Resource queries select only the requested collection, are bounded, and never
// perform a query per nested GraphQL field. Parent indexes serve agent histories.
func (i *Instance) Save(kind, id, parent string, value any) error {
	if i == nil || i.Closed() {
		return errClosed
	}
	if kind == "" || id == "" {
		return errors.New("resource kind and id are required")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = i.db.Exec(`INSERT INTO resources(kind,id,parent_id,data) VALUES (?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data,parent_id=excluded.parent_id`, kind, id, parent, string(data))
	return err
}
func (i *Instance) Load(kind, id string, value any) error {
	if i == nil || i.Closed() {
		return errClosed
	}
	var data string
	if err := i.db.QueryRow(`SELECT data::VARCHAR FROM resources WHERE kind=? AND id=?`, kind, id).Scan(&data); err != nil {
		return err
	}
	return json.Unmarshal([]byte(data), value)
}
func (i *Instance) List(kind string, parent *string, limit, offset int) ([]map[string]any, error) {
	return i.ListFields(kind, parent, limit, offset, nil)
}

func (i *Instance) ListFields(kind string, parent *string, limit, offset int, fields []string) ([]map[string]any, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		return nil, errors.New("negative offset")
	}
	projection, args := resourceProjection(fields)
	q := `SELECT ` + projection + `::VARCHAR FROM resources WHERE kind=?`
	args = append(args, kind)
	if parent != nil {
		q += ` AND parent_id=?`
		args = append(args, *parent)
	}
	q += ` ORDER BY created_at,id LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := i.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(data), &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func (i *Instance) Remove(kind, id string) error {
	if i == nil || i.Closed() {
		return errClosed
	}
	_, err := i.db.Exec(`DELETE FROM resources WHERE kind=? AND id=?`, kind, id)
	return err
}
func (i *Instance) Setting(name string) (string, error) {
	if i == nil || i.Closed() {
		return "", errClosed
	}
	var v string
	err := i.db.QueryRow(`SELECT value FROM settings WHERE name=?`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
func (i *Instance) SetSetting(name, value string) error {
	if i == nil || i.Closed() {
		return errClosed
	}
	_, err := i.db.Exec(`INSERT INTO settings VALUES (?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value`, name, value)
	return err
}
func (i *Instance) InitializePassword(hash string) (bool, error) {
	return i.initializeSetup(hash, "")
}

// InitializeSetup commits both onboarding choices together. A second setup
// attempt cannot overwrite the first user's password or commit identity.
func (i *Instance) InitializeSetup(hash, commitIdentity string) (bool, error) {
	return i.initializeSetup(hash, commitIdentity)
}

func (i *Instance) initializeSetup(hash, commitIdentity string) (bool, error) {
	if i == nil || i.Closed() {
		return false, errClosed
	}
	transaction, err := i.db.Begin()
	if err != nil {
		return false, err
	}
	defer transaction.Rollback()
	var name string
	err = transaction.QueryRow(`INSERT INTO settings VALUES ('login_argon2id',?) ON CONFLICT DO NOTHING RETURNING name`, hash).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if commitIdentity != "" {
		if _, err := transaction.Exec(`INSERT INTO settings VALUES ('commit_identity',?) ON CONFLICT(name) DO UPDATE SET value=excluded.value`, commitIdentity); err != nil {
			return false, err
		}
	}
	return true, transaction.Commit()
}
func (i *Instance) CreateSession(hash string, expires time.Time) error {
	if i == nil || i.Closed() {
		return errClosed
	}
	if _, err := i.db.Exec(`DELETE FROM sessions WHERE expires_at < now()`); err != nil {
		return err
	}
	_, err := i.db.Exec(`INSERT INTO sessions VALUES (?,?)`, hash, expires)
	return err
}
func (i *Instance) SessionValid(hash string) (bool, error) {
	if i == nil || i.Closed() {
		return false, errClosed
	}
	var ok bool
	err := i.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE token_hash=? AND expires_at>now())`, hash).Scan(&ok)
	return ok, err
}
func (i *Instance) DeleteSession(hash string) error {
	if i == nil || i.Closed() {
		return errClosed
	}
	_, err := i.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, hash)
	return err
}
func (i *Instance) AppendEvent(kind, agentID, data string) (domain.Event, error) {
	var e domain.Event
	if i == nil || i.Closed() {
		return e, errClosed
	}
	err := i.db.QueryRow(`INSERT INTO events(type,agent_id,data) VALUES (?,?,?) RETURNING id,type,agent_id,data,created_at`, kind, agentID, data).Scan(&e.ID, &e.Type, &e.AgentID, &e.Data, &e.CreatedAt)
	return e, err
}
func (i *Instance) Events(after int64, limit int) ([]domain.Event, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := i.db.Query(`SELECT id,type,agent_id,data,created_at FROM events WHERE id>? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Event, 0)
	for rows.Next() {
		var e domain.Event
		if err := rows.Scan(&e.ID, &e.Type, &e.AgentID, &e.Data, &e.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
func (i *Instance) PrivateKey(id int) ([]byte, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}
	var data []byte
	err := i.db.QueryRow(`SELECT x509_data FROM private_ssh_keys WHERE id=?`, id).Scan(&data)
	return data, err
}
func (i *Instance) TrustedKeys() ([][]byte, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}
	rows, err := i.db.Query(`SELECT x509_data FROM known_ssh_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([][]byte, 0)
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, fmt.Errorf("read host key: %w", err)
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

// ReplaceCollection publishes a complete model catalog in one transaction so
// readers never observe a half-refreshed list.
func (i *Instance) ReplaceCollection(kind, parent string, values map[string]any) error {
	if i == nil || i.Closed() {
		return errClosed
	}
	transaction, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec(`DELETE FROM resources WHERE kind=? AND parent_id=?`, kind, parent); err != nil {
		return err
	}
	for id, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if _, err := transaction.Exec(`INSERT INTO resources(kind,id,parent_id,data) VALUES (?,?,?,?)`, kind, id, parent, string(encoded)); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

// Patch updates a resource atomically without replacing unrelated account fields.
func (i *Instance) Patch(kind, id string, fields map[string]any) error {
	if i == nil || i.Closed() {
		return errClosed
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	result, err := i.db.Exec(`UPDATE resources SET data=json_merge_patch(data,?::JSON) WHERE kind=? AND id=?`, string(encoded), kind, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (i *Instance) SearchModels(providerID, search string, limit, offset int) ([]map[string]any, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		return nil, errors.New("negative offset")
	}
	pattern := "%" + strings.ToLower(search) + "%"
	rows, err := i.db.Query(`SELECT data::VARCHAR FROM resources
  WHERE kind='model' AND parent_id=?
  AND (lower(json_extract_string(data,'$.name')) LIKE ? OR lower(json_extract_string(data,'$.id')) LIKE ?)
  ORDER BY json_extract_string(data,'$.name'),id LIMIT ? OFFSET ?`, providerID, pattern, pattern, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var model map[string]any
		if err := json.Unmarshal([]byte(encoded), &model); err != nil {
			return nil, err
		}
		result = append(result, model)
	}
	return result, rows.Err()
}

// Both JSON keys and paths are bound parameters, even though GraphQL validates
// field names first. No client-provided text becomes an SQL identifier.
func resourceProjection(fields []string) (string, []any) {
	if fields == nil {
		return "data", nil
	}
	if len(fields) == 0 {
		return "json_object()", nil
	}
	expressions := make([]string, 0, len(fields))
	arguments := make([]any, 0, len(fields)*2)
	for _, field := range fields {
		expressions = append(expressions, "?, json_extract(data, ?)")
		arguments = append(arguments, field, "$."+field)
	}
	return "json_object(" + strings.Join(expressions, ",") + ")", arguments
}
