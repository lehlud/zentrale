package datastore

const unfinishedStatus = `json_extract(data,'$.status') IN ('running','waiting')`
const resumableInput = `json_extract(data,'$.status') IN ('pending','queued','active')`

// Keep the startup index small: jobs and inputs leave it when they finish.
// Require this index on recovery queries so a freshly opened database without
// planner statistics still visits only candidates, even when history is large.
const recoveryResources = `(
    (kind='job' AND ` + unfinishedStatus + `) OR
    (kind='agent' AND json_extract(data,'$.status') IN ('running','waiting','interrupted')) OR
    (kind='agent_input' AND ` + resumableInput + `) OR
    (kind='goal' AND json_extract(data,'$.status')='active') OR
    (kind='agent_stop' AND json_extract(data,'$.requested')=1)
)`

const unfinishedWorkQuery = `SELECT json_object('id',id,'status',json_extract(data,'$.status'))
    FROM resources INDEXED BY resource_recovery WHERE ` + recoveryResources + ` AND kind=? AND ` + unfinishedStatus + `
    ORDER BY created_at,id`

// UnfinishedWork returns identities for the agent and job recovery passes.
func (i *Instance) UnfinishedWork(kind string) ([]map[string]any, error) {
	return i.queryResources(unfinishedWorkQuery, kind)
}

// InterruptJob changes only a job still awaiting recovery. A retiring worker
// may have committed a final result after the startup query selected it.
func (i *Instance) InterruptJob(id string) error {
	if i == nil || i.Closed() {
		return errClosed
	}
	_, err := i.db.Exec(`UPDATE resources SET data=json_set(data,'$.status','interrupted','$.error','service restarted')
	    WHERE kind='job' AND id=? AND `+unfinishedStatus, id)
	return err
}

const agentsForRecoveryQuery = `SELECT json_object(
        'id',agent.id,'projectId',json_extract(agent.data,'$.projectId'),'status',json_extract(agent.data,'$.status'))
    FROM (
        SELECT DISTINCT CASE WHEN kind='agent_input' THEN parent_id ELSE id END AS agent_id
        FROM resources INDEXED BY resource_recovery
        WHERE ` + recoveryResources + ` AND kind IN ('agent','agent_input','goal','agent_stop')
    ) candidate
    JOIN resources agent ON agent.kind='agent' AND agent.id=candidate.agent_id
    WHERE json_extract(agent.data,'$.role') IS NOT 'approver'
    ORDER BY agent.created_at,agent.id`

// AgentsForRecovery includes persisted stop records even when their agents
// have no open input, so later notifications respect the explicit stop.
func (i *Instance) AgentsForRecovery() ([]map[string]any, error) {
	return i.queryResources(agentsForRecoveryQuery)
}

func (i *Instance) SubagentsForApproverMigration() ([]map[string]any, error) {
	return i.queryResources(`SELECT data FROM resources
	    WHERE kind='agent' AND json_extract(data,'$.role')='subagent' ORDER BY created_at,id`)
}
