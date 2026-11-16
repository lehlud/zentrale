package datastore

import (
	"database/sql"
	"strings"
)

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
	// One pass per table and bounded parameter batches, independent of family size.
	for start := 0; start < len(ids); start += 200 {
		batch := ids[start:min(start+200, len(ids))]
		args := make([]any, len(batch))
		for n, id := range batch {
			args[n] = id
		}
		in := "(" + strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",") + ")"
		if err := queueVMRemovals(transaction, "json_extract(data,'$.agentId') IN "+in, args...); err != nil {
			return err
		}
		if _, err := transaction.Exec("DELETE FROM attachment_blobs WHERE agent_id IN "+in, args...); err != nil {
			return err
		}
		all := append(append(append([]any{}, args...), args...), args...)
		if _, err := transaction.Exec("DELETE FROM resources WHERE (kind='agent' AND id IN "+in+") OR parent_id IN "+in+" OR json_extract(data,'$.agentId') IN "+in, all...); err != nil {
			return err
		}
		if _, err := transaction.Exec("DELETE FROM events WHERE agent_id IN "+in, args...); err != nil {
			return err
		}
	}

	return nil
}

// The outbox and logical deletion commit together. Cleanup survives restarts;
// its ownership fields deliberately do not refer to the deleted resource rows.
func queueVMRemovals(tx *sql.Tx, condition string, args ...any) error {
	_, err := tx.Exec(`INSERT INTO resources(kind,id,parent_id,data)
	 SELECT 'vm_cleanup',id,'',json_object('id',id,'ownerId',json_extract(data,'$.agentId'),
	 'targetProjectId',json_extract(data,'$.projectId'),'machineId',json_extract(data,'$.machineId'),
	 'targetConfiguration',(SELECT target.data FROM resources target WHERE
	  (target.kind='machine' AND target.id=json_extract(vm.data,'$.machineId')) OR
	  (target.kind='pool' AND target.id=substr(json_extract(vm.data,'$.machineId'),1,instr(json_extract(vm.data,'$.machineId'),'/')-1))),
	 'workspace',json_extract(data,'$.workspace')) FROM resources vm WHERE kind='microvm' AND `+condition+`
	 ON CONFLICT(kind,id) DO NOTHING`, args...)
	return err
}

func (instance *Instance) PendingVMCleanup() ([]map[string]any, error) {
	if instance.Closed() {
		return nil, errClosed
	}
	return instance.queryResources(`SELECT data FROM resources WHERE kind='vm_cleanup'
	 AND (json_extract(data,'$.retryAt') IS NULL OR julianday(json_extract(data,'$.retryAt'))<=julianday('now'))
	 ORDER BY created_at,id LIMIT 100`)
}
