package datastore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/lehlud/zentrale/domain"
)

func (i *Instance) MessageHistory(ctx context.Context, agentID string) ([]domain.Message, error) {
	return i.readMessageHistory(ctx, agentID, "", "")
}

// ReviewHistory returns a suffix starting at an event boundary. The latest
// review and all its inspections remain intact. The offset keeps context
// boundaries in the same coordinates as the full audit history.
func (i *Instance) ReviewHistory(ctx context.Context, agentID string, groups int) ([]domain.Message, int, error) {
	if i == nil || i.Closed() {
		return nil, 0, errClosed
	}
	db := i.readDB()
	var timestamp, id string
	err := db.QueryRowContext(ctx, `
		SELECT CAST(created_at AS TEXT), id FROM resources
		WHERE kind='message' AND parent_id=? AND data->>'role'='event'
		ORDER BY created_at DESC, id DESC LIMIT 1 OFFSET ?`, agentID, groups-1).Scan(&timestamp, &id)
	if errors.Is(err, sql.ErrNoRows) {
		messages, err := i.MessageHistory(ctx, agentID)
		return messages, 0, err
	}
	if err != nil {
		return nil, 0, err
	}

	var offset int
	err = db.QueryRowContext(ctx, `
		SELECT count(*) FROM resources
		WHERE kind='message' AND parent_id=? AND (created_at, id)<(?, ?)`,
		agentID, timestamp, id).Scan(&offset)
	if err != nil {
		return nil, 0, err
	}
	messages, err := i.readMessageHistory(ctx, agentID, timestamp, id)
	return messages, offset, err
}

func (i *Instance) readMessageHistory(ctx context.Context, agentID, timestamp, id string) ([]domain.Message, error) {
	if i == nil || i.Closed() {
		return nil, errClosed
	}
	query := `SELECT data FROM resources WHERE kind='message' AND parent_id=?`
	args := []any{agentID}
	if id != "" {
		query += ` AND (created_at, id)>=(?, ?)`
		args = append(args, timestamp, id)
	}
	rows, err := i.readDB().QueryContext(ctx, query+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []domain.Message
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var message domain.Message
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}
