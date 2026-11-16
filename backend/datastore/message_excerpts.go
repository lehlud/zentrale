package datastore

import (
	"errors"
	"strings"
)

// MessageExcerpts limits both rows and projected text in SQL. Opaque provider
// wire and large tool outputs never enter Go merely to list or search a thread.
// The cursor is local to this agent, so another thread cannot shift its window.
func (instance *Instance) MessageExcerpts(agentID, beforeID, search string, limit, characters int) ([]map[string]any, error) {
	if limit < 1 || limit > 51 || characters < 1 || characters > 2000 || len(search) > 500 {
		return nil, errors.New("invalid message excerpt bounds")
	}

	statement := `SELECT json_object(
		'id', id, 'agentId', parent_id, 'role', data->>'role',
		'content', substr(coalesce(data->>'content',''),1, ?),
		'contentCharacters', length(coalesce(data->>'content','')),
		'hasCalls', json(CASE WHEN length(coalesce(data->>'wire','')) > 0 THEN 'true' ELSE 'false' END),
		'callId', data->>'callId', 'toolName', data->>'toolName',
		'createdAt', created_at)
		FROM resources WHERE kind='message' AND parent_id=?`
	arguments := []any{characters, agentID}

	if beforeID != "" {
		statement += ` AND EXISTS (
			SELECT 1 FROM resources anchor
			WHERE anchor.kind='message' AND anchor.parent_id=? AND anchor.id=?
			AND (resources.created_at < anchor.created_at
			OR (resources.created_at = anchor.created_at AND resources.id < anchor.id)))`
		arguments = append(arguments, agentID, beforeID)
	}

	if strings.TrimSpace(search) != "" {
		// Provider wire may contain private reasoning. Even a match/no-match
		// oracle would expose it; search only visible content and tool names.
		statement += ` AND instr(lower(coalesce(data->>'content','') || coalesce(data->>'toolName','')), lower(?)) > 0`
		arguments = append(arguments, search)
	}

	statement += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	arguments = append(arguments, limit)

	return instance.queryResources(statement, arguments...)
}
