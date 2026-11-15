package datastore

import "encoding/json"

// queryResources decodes a single JSON column from the read pool. Callers own
// filtering, ordering, bounds and projection; this boundary owns result-set
// cleanup and never returns a partially decoded collection on failure.
func (instance *Instance) queryResources(statement string, arguments ...any) ([]map[string]any, error) {
	if instance == nil || instance.Closed() {
		return nil, errClosed
	}

	rows, err := instance.readDB().Query(statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]map[string]any, 0)
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}

		var record map[string]any
		if err := json.Unmarshal([]byte(encoded), &record); err != nil {
			return nil, err
		}

		result = append(result, record)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}
