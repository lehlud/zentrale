package datastore

import (
	"errors"

	"github.com/lehlud/zentrale/domain"
)

// UserProjects pages only projects visible to this account. Filtering before
// pagination keeps other users' projects out of both the rows and their count.
func (i *Instance) UserProjects(userID string, limit, offset int, fields []string) ([]map[string]any, error) {
	if userID == "" || offset < 0 {
		return nil, errors.New("invalid project listing")
	}

	if limit <= 0 || limit > 500 {
		limit = 100
	}

	projection, args := resourceProjection(fields)
	query := `SELECT ` + projection + ` FROM resources
		WHERE kind='project' AND (
			coalesce(nullif(json_extract(data, '$.ownerId'), ''), ?) = ?
			OR EXISTS(SELECT 1 FROM json_each(resources.data, '$.memberIds') WHERE value=?)
		) ORDER BY created_at,id LIMIT ? OFFSET ?`
	args = append(args, domain.InitialUserID, userID, userID, limit, offset)
	return i.queryResources(query, args...)
}

func (i *Instance) ShortChats(userID string, limit, offset int, fields []string) ([]map[string]any, error) {
	if userID == "" || offset < 0 {
		return nil, errors.New("invalid short chat listing")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	projection, args := resourceProjection(fields)
	query := `SELECT ` + projection + ` FROM resources WHERE kind='agent' AND parent_id=''
		AND json_extract(data, '$.chatOwnerId')=? AND json_extract(data, '$.role')='main'
		ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`
	return i.queryResources(query, append(args, userID, limit, offset)...)
}

// RemoveProject removes only application records, not repository files, shared
// machines/providers or private SSH keys. All dependent records are committed in
// the same transaction as the project so a database failure leaves no half-delete.
func (instance *Instance) RemoveProject(id string) error {
	if instance == nil || instance.Closed() {
		return errClosed
	}
	if id == "" {
		return errors.New("project id is required")
	}
	transaction, err := instance.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err := queueVMRemovals(transaction, "json_extract(data,'$.projectId')=?", id); err != nil {
		return err
	}
	rows, err := transaction.Query(`SELECT id FROM resources WHERE kind='agent'
		AND (parent_id=? OR json_extract(data,'$.projectId')=?)`, id, id)
	if err != nil {
		return err
	}
	var agents []string
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			rows.Close()
			return err
		}
		agents = append(agents, agentID)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := removeAgents(transaction, agents); err != nil {
		return err
	}
	if _, err := transaction.Exec(`DELETE FROM attachment_blobs WHERE project_id=?`, id); err != nil {
		return err
	}
	if _, err := transaction.Exec(`DELETE FROM resources WHERE kind<>'usage' AND
		((kind='project' AND id=?) OR parent_id=? OR json_extract(data,'$.projectId')=?)`, id, id, id); err != nil {
		return err
	}
	return transaction.Commit()
}
