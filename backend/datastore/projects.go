package datastore

import "errors"

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
	rows, err := transaction.Query(`SELECT id FROM resources WHERE kind='agent'
		AND (parent_id=? OR json_extract_string(data,'$.projectId')=?)`, id, id)
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
	if _, err := transaction.Exec(`DELETE FROM resources WHERE
		(kind='project' AND id=?) OR parent_id=? OR json_extract_string(data,'$.projectId')=?`, id, id, id); err != nil {
		return err
	}
	return transaction.Commit()
}
