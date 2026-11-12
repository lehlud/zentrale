package datastore

import "database/sql"

func (instance *Instance) RemoveAgents(ids []string) error {
	if instance == nil || instance.Closed() {
		return errClosed
	}

	transaction, err := instance.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()

	if err := removeAgents(transaction, ids); err != nil {
		return err
	}
	return transaction.Commit()
}

func removeAgents(transaction *sql.Tx, ids []string) error {
	for _, id := range ids {
		if _, err := transaction.Exec(`DELETE FROM attachment_blobs WHERE agent_id=?`, id); err != nil {
			return err
		}
		if _, err := transaction.Exec(`DELETE FROM resources WHERE
            (kind='agent' AND id=?) OR parent_id=? OR json_extract_string(data,'$.agentId')=?`, id, id, id); err != nil {
			return err
		}

		if _, err := transaction.Exec(`DELETE FROM events WHERE agent_id=?`, id); err != nil {
			return err
		}
	}

	return nil
}
