package datastore

import (
	"encoding/json"
	"errors"
)

// ListStatusFields filters before projection/pagination so opening a chat never
// transfers old answered questions or completed job output to the application.
// All selectors and JSON field paths are bound parameters, not SQL fragments.
func (i *Instance) ListStatusFields(kind, parentID, status string, limit, offset int, fields []string) ([]map[string]any, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		return nil, errors.New("negative offset")
	}
	projection, args := resourceProjection(fields)
	query := `SELECT ` + projection + `::VARCHAR FROM resources WHERE kind=? AND parent_id=? AND json_extract_string(data,'$.status')=? ORDER BY created_at,id LIMIT ? OFFSET ?`
	args = append(args, kind, parentID, status, limit, offset)
	rows, err := i.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
