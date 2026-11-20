package datastore

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

// WriteAgentRun publishes status and phase together. A retiring engine may
// finish after the replacement has claimed the same agent during a rollout.
func (instance *Instance) WriteAgentRun(owner, status, failure string, state domain.AgentRunState, begin bool) (bool, error) {
	if instance == nil || instance.Closed() {
		return false, errClosed
	}
	phase, err := json.Marshal(state)
	if err != nil {
		return false, err
	}
	tx, err := instance.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	update := `UPDATE resources SET data=json_set(data,'$.status',?,'$.error',?) WHERE kind='agent' AND id=?`
	args := []any{status, failure, state.AgentID}
	if !begin {
		if owner == "" {
			update += ` AND json_extract(data,'$.status') IN ('running','waiting')`
		} else {
			update += ` AND EXISTS (SELECT 1 FROM resources worker
				WHERE worker.kind='agent_worker' AND worker.id=resources.id AND worker.data=json_quote(?))`
			args = append(args, owner)
		}
	}
	result, err := tx.Exec(update, args...)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		if err == nil && begin {
			err = sql.ErrNoRows
		}
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO resources(kind,id,parent_id,data,created_at) VALUES ('agent_run',?,?,?,?)
		ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`,
		state.AgentID, state.AgentID, string(phase), resourceCreatedAt()); err != nil {
		return false, err
	}
	// Ownership belongs to the local worker, independent of the agent record's
	// settings and history updates. An empty owner is reserved for recovery.
	if begin {
		_, err = tx.Exec(`INSERT INTO resources(kind,id,parent_id,data,created_at) VALUES ('agent_worker',?,?,json_quote(?),?)
			ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`,
			state.AgentID, state.AgentID, owner, resourceCreatedAt())
	} else {
		_, err = tx.Exec(`DELETE FROM resources WHERE kind='agent_worker' AND id=?`, state.AgentID)
	}
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

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

// ProjectMicroVMs returns each VM receipt under its storage key. Older
// receipts may have an agent parent, and the JSON does not contain the key.
func (instance *Instance) ProjectMicroVMs(projectID string) (map[string]json.RawMessage, error) {
	if instance == nil || instance.Closed() {
		return nil, errClosed
	}
	rows, err := instance.readDB().Query(`SELECT id,data FROM resources WHERE kind='microvm'
	 AND json_extract(data,'$.projectId')=?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make(map[string]json.RawMessage)
	for rows.Next() {
		var id, data string
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		records[id] = json.RawMessage(data)
	}
	return records, rows.Err()
}

func (instance *Instance) PendingVMCleanup() ([]map[string]any, error) {
	if instance.Closed() {
		return nil, errClosed
	}
	return instance.queryResources(`SELECT data FROM resources WHERE kind='vm_cleanup'
	 AND (json_extract(data,'$.retryAt') IS NULL OR julianday(json_extract(data,'$.retryAt'))<=julianday('now'))
	 ORDER BY created_at,id LIMIT 100`)
}
