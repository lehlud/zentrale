package datastore

import (
	"errors"
	"strings"
)

// ResourcesByID loads related records in one bounded query, with the same field
// projection as collection queries. IDs are bound parameters, never SQL text.
func (instance *Instance) ResourcesByID(kind string, ids, fields []string) ([]map[string]any, error) {
	return instance.resourceBatch(kind, ids, fields, false)
}

// New patch messages may own several diff records. Legacy single-file records
// keep their message ID as resource ID and are returned by the same query.
func (instance *Instance) MessageFileChanges(ids, fields []string) ([]map[string]any, error) {
	return instance.resourceBatch("file_change", ids, fields, true)
}

func (instance *Instance) resourceBatch(
	kind string,
	ids, fields []string,
	byMessage bool,
) ([]map[string]any, error) {
	if instance == nil || instance.Closed() {
		return nil, errClosed
	}

	if len(ids) > 500 {
		return nil, errors.New("resource batch exceeds 500 IDs")
	}

	if len(ids) == 0 {
		return []map[string]any{}, nil
	}

	projection, arguments := resourceProjection(fields)
	arguments = append(arguments, kind)

	for _, id := range ids {
		arguments = append(arguments, id)
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	condition := "id IN (" + placeholders + ")"

	if byMessage {
		condition = "(" + condition + " OR json_extract_string(data,'$.messageId') IN (" + placeholders + "))"

		for _, id := range ids {
			arguments = append(arguments, id)
		}
	}

	return instance.queryResources("SELECT "+projection+"::VARCHAR FROM resources WHERE kind=? AND "+condition+" ORDER BY id", arguments...)
}
