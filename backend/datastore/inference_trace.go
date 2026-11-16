package datastore

import "encoding/json"

func (i *Instance) RecentInferenceTraces(agentID string, limit, offset int) ([]map[string]any, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}
	limit = max(1, min(100, limit))
	offset = max(0, offset)
	rows, err := i.readDB().Query(`SELECT CAST(data AS VARCHAR) FROM resources WHERE kind='inference_trace' AND parent_id=? ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, agentID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(encoded), &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}
