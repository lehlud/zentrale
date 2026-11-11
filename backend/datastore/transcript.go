package datastore

import "encoding/json"

// TranscriptBefore pages messages and terminal goal snapshots together. Goals
// are UI history entries only: they never enter the model's message context.
// The union also covers existing installations without a history migration.
func (instance *Instance) TranscriptBefore(agentID string, limit int, beforeID string) ([]map[string]any, error) {
	if instance == nil || instance.Closed() {
		return nil, errClosed
	}
	if limit < 1 || limit > 100 {
		limit = 100
	}
	query := `WITH transcript AS (
		SELECT id, created_at AS position, data::VARCHAR AS encoded, false AS goal
		FROM resources WHERE kind='message' AND parent_id=?
		UNION ALL
		SELECT 'goal:' || id, (data->>'updatedAt')::TIMESTAMPTZ AS position, data::VARCHAR AS encoded, true AS goal
		FROM resources WHERE kind='goal_history' AND parent_id=?
		AND (data->>'status') IN ('complete','cancelled','superseded')
	)`
	args := []any{agentID, agentID}
	if beforeID == "" {
		query += ` SELECT id,encoded,goal FROM transcript ORDER BY position DESC,id DESC LIMIT ?`
	} else {
		query += `, anchor AS (SELECT id,position FROM transcript WHERE id=?)
		SELECT t.id,t.encoded,t.goal FROM transcript t, anchor a
		WHERE t.position<a.position OR (t.position=a.position AND t.id<a.id)
		ORDER BY t.position DESC,t.id DESC LIMIT ?`
		args = append(args, beforeID)
	}
	args = append(args, limit)
	rows, err := instance.db.Query(query, args...)
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
