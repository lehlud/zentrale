package datastore

import "encoding/json"

// TranscriptBefore pages messages and terminal goal snapshots together. Goals
// are UI history entries only: they never enter the model's message context.
// The union also covers existing installations without a history migration.
func (instance *Instance) TranscriptBefore(agentID string, limit int, beforeID string) ([]map[string]any, error) {
	return instance.TranscriptBeforeFields(agentID, limit, beforeID, nil)
}

// TranscriptBeforeFields selects the page using only IDs and positions, then
// projects the selected messages in SQL. Large wire payloads must not enter Go
// just because a conversation requests its compact UI transcript.
func (instance *Instance) TranscriptBeforeFields(agentID string, limit int, beforeID string, fields []string) ([]map[string]any, error) {
	if instance == nil || instance.Closed() {
		return nil, errClosed
	}
	if limit < 1 || limit > 100 {
		limit = 100
	}
	query := `WITH transcript AS (
		SELECT id, id AS resource_id, 'message' AS kind, created_at AS position, false AS goal
		FROM resources WHERE kind='message' AND parent_id=?
		UNION ALL
		SELECT 'goal:' || id, id AS resource_id, 'goal_history' AS kind, (data->>'updatedAt')::TIMESTAMPTZ AS position, true AS goal
		FROM resources WHERE kind='goal_history' AND parent_id=?
		AND (data->>'status') IN ('complete','cancelled','superseded')
	)`
	args := []any{agentID, agentID}
	if beforeID == "" {
		query += `, page AS MATERIALIZED (SELECT * FROM transcript ORDER BY position DESC,id DESC LIMIT ?)`
	} else {
		query += `, anchor AS (SELECT id,position FROM transcript WHERE id=?)
		, page AS MATERIALIZED (SELECT t.* FROM transcript t, anchor a
		WHERE t.position<a.position OR (t.position=a.position AND t.id<a.id)
		ORDER BY t.position DESC,t.id DESC LIMIT ?)`
		args = append(args, beforeID)
	}
	args = append(args, limit)
	projection, projectionArgs := resourceProjection(fields)
	// Goal snapshots retain their full serialized content as a synthetic message.
	query += ` SELECT p.id, CASE WHEN p.goal THEN r.data::VARCHAR ELSE (` + projection + `)::VARCHAR END, p.goal
		FROM page p JOIN resources r ON r.kind=p.kind AND r.id=p.resource_id
		ORDER BY p.position DESC,p.id DESC`
	args = append(args, projectionArgs...)
	rows, err := instance.readDB().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id, encoded string
		var goal bool
		if err := rows.Scan(&id, &encoded, &goal); err != nil {
			return nil, err
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(encoded), &value); err != nil {
			return nil, err
		}
		if goal {
			value = map[string]any{
				"id": id, "agentId": agentID, "role": "goal", "content": encoded,
				"createdAt": value["updatedAt"], "wire": "", "callId": "", "toolName": "",
			}
		}
		result = append(result, value)
	}
	return result, rows.Err()
}
