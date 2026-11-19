package datastore

// PruneTerminalAttachments removes expired public leases and their private
// input receipts in one short local transaction. It never performs remote IO.
func (i *Instance) PruneTerminalAttachments() error {
	tx, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	expired := `SELECT id FROM resources WHERE kind='terminal_attachment'
	 AND julianday(json_extract(data,'$.expiresAt'))<=julianday('now')`
	if _, err := tx.Exec(`DELETE FROM credential_records WHERE name IN (SELECT 'terminal-runtime/'||id FROM resources WHERE id IN (` + expired + `) AND kind='terminal_attachment')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM resources WHERE kind='terminal_attachment' AND id IN (` + expired + `)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (i *Instance) HasTerminalAttachment(projectID, machineID string) (bool, error) {
	var active bool
	err := i.readDB().QueryRow(`SELECT EXISTS(SELECT 1 FROM resources WHERE kind='terminal_attachment'
	 AND parent_id=? AND (?='' OR json_extract(data,'$.machineId')=?)
	 AND julianday(json_extract(data,'$.expiresAt'))>julianday('now'))`, projectID, machineID, machineID).Scan(&active)
	return active, err
}
