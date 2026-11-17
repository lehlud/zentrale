package datastore

import (
	"database/sql"
	"errors"
)

// MachineUse checks role-change blockers without loading conversation/job
// histories or issuing a query for each project. No remote IO is involved.
func (i *Instance) MachineUse(id string) (string, error) {
	if i.Closed() {
		return "", errClosed
	}
	return machineUse(i.readDB().QueryRow, id)
}

func machineUse(query func(string, ...any) *sql.Row, id string) (string, error) {
	var use string
	err := query(`SELECT CASE
 WHEN EXISTS(SELECT 1 FROM resources r, json_each(r.data,'$.machineIds') m
   WHERE r.kind='project' AND m.value=?) THEN 'project'
 WHEN EXISTS(SELECT 1 FROM resources WHERE kind='job'
   AND json_extract(data,'$.machineId')=? AND json_extract(data,'$.status') IN ('running','stopping')) THEN 'job'
 WHEN EXISTS(SELECT 1 FROM resources WHERE kind='editor_attachment'
   AND json_extract(data,'$.machineId')=?) THEN 'editor'
 WHEN EXISTS(SELECT 1 FROM resources WHERE kind='terminal_attachment'
   AND json_extract(data,'$.machineId')=?
   AND COALESCE(julianday(json_extract(data,'$.expiresAt')),julianday('now')+1)>julianday('now')) THEN 'terminal'
 ELSE '' END`, id, id, id, id).Scan(&use)
	return use, err
}

// The caller holds the resource/key write transaction, so a role change cannot
// overtake the job/terminal records that make the machine busy.
func checkJumpOnlyRole(tx *sql.Tx, id string) error {
	use, err := machineUse(tx.QueryRow, id)
	if err != nil {
		return err
	}
	switch use {
	case "project":
		return errors.New("machine is assigned to a project; choose another project target before making it jump host only")
	case "job":
		return errors.New("stop running jobs before making their machine jump host only")
	case "editor", "terminal":
		return errors.New("close editors and terminals before making their machine jump host only")
	}
	return nil
}
