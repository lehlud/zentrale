package datastore

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/lehlud/zentrale/domain"
)

type usageAttribution struct {
	UserID    string `json:"userId"`
	ProjectID string `json:"projectId"`
}

// SaveUsage stores ownership alongside the cost in the same transaction. Keep
// attribution out of the agent's Usage API and per-request events.
func (instance *Instance) SaveUsage(usage domain.Usage, agent domain.Agent) error {
	if instance == nil || instance.Closed() {
		return errClosed
	}

	if usage.ID == "" || usage.AgentID != agent.ID {
		return errors.New("invalid usage record")
	}

	tx, err := instance.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	attribution := usageAttribution{ProjectID: agent.ProjectID}
	if agent.ProjectID == "" {
		attribution.UserID = agent.ChatOwnerID
	} else {
		var project domain.Project
		err := tx.QueryRow(`SELECT coalesce(data->>'ownerId', '') FROM resources WHERE kind='project' AND id=?`, agent.ProjectID).Scan(&project.OwnerID)
		if err == nil {
			attribution.UserID = project.Owner()
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}

	data, err := json.Marshal(struct {
		domain.Usage
		Attribution usageAttribution `json:"attribution"`
	}{Usage: usage, Attribution: attribution})
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`INSERT INTO resources(kind, id, parent_id, data, created_at) VALUES ('usage', ?, ?, ?, ?)`,
		usage.ID, usage.AgentID, string(data), resourceCreatedAt()); err != nil {
		return err
	}
	return tx.Commit()
}

// Explicit attribution is authoritative even if its IDs are empty. Legacy
// project usage can use Project.Owner() semantics only when both the agent
// and its project still exist; ownership transfers are not supported.
const attributionSelect = `SELECT usage.id, usage.created_at, usage.data,
	CASE
		WHEN json_type(usage.data, '$.attribution') IS NOT NULL THEN coalesce(usage.data->>'$.attribution.userId', '')
		WHEN project.id IS NOT NULL THEN coalesce(nullif(project.data->>'ownerId', ''), :initial_user)
		WHEN coalesce(agent.data->>'projectId', '') = '' THEN coalesce(agent.data->>'chatOwnerId', '')
		ELSE ''
	END AS user_id,
	CASE
		WHEN json_type(usage.data, '$.attribution') IS NOT NULL THEN coalesce(usage.data->>'$.attribution.projectId', '')
		ELSE coalesce(project.id, '')
	END AS project_id`

const attributionJoins = ` LEFT JOIN resources AS agent ON agent.kind='agent' AND agent.id=usage.parent_id
		AND json_type(usage.data, '$.attribution') IS NULL
	LEFT JOIN resources AS project ON project.kind='project' AND project.id=(agent.data->>'projectId')`

// detachAgentUsage freezes any provable legacy attribution before deletion.
// Retain accounting data but not links to deleted agents or traces.
func detachAgentUsage(tx *sql.Tx, in string, agentIDs []any) error {
	args := append(append([]any{}, agentIDs...), sql.Named("initial_user", (domain.Project{}).Owner()))
	_, err := tx.Exec(`WITH ending_usage AS (
		SELECT * FROM resources WHERE kind='usage' AND parent_id IN `+in+`
	), detached AS MATERIALIZED (`+attributionSelect+` FROM ending_usage AS usage`+attributionJoins+`)
		UPDATE resources AS usage SET parent_id='', data=json_remove(
			json_set(usage.data, '$.attribution', json_object('userId', detached.user_id, 'projectId', detached.project_id)),
			'$.agentId', '$.traceId')
		FROM detached WHERE usage.kind='usage' AND usage.id=detached.id`, args...)
	return err
}
