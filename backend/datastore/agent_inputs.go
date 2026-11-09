package datastore

import (
	"encoding/json"

	"github.com/lehlud/zentrale/domain"
)

type ResourceWrite struct {
	Kind   string
	ID     string
	Parent string

	Value any
}

// SaveResources commits a receipt, its message and instruction together. A
// crash must never acknowledge an input without preserving its exact contents.
func (instance *Instance) SaveResources(writes []ResourceWrite) error {
	if instance == nil || instance.Closed() {
		return errClosed
	}

	tx, err := instance.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, write := range writes {
		data, err := json.Marshal(write.Value)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(
			`INSERT INTO resources(kind,id,parent_id,data) VALUES (?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data,parent_id=excluded.parent_id`,
			write.Kind,
			write.ID,
			write.Parent,
			string(data),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (instance *Instance) AgentInputs(agentID string) ([]domain.AgentInput, error) {
	if instance == nil || instance.Closed() {
		return nil, errClosed
	}

	rows, err := instance.db.Query(
		`SELECT data::VARCHAR FROM resources WHERE kind='agent_input' AND parent_id=? AND json_extract_string(data,'$.status') NOT IN ('completed','cancelled') ORDER BY created_at,id`,
		agentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	inputs := []domain.AgentInput{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}

		var input domain.AgentInput
		if err := json.Unmarshal([]byte(data), &input); err != nil {
			return nil, err
		}

		inputs = append(inputs, input)
	}

	return inputs, rows.Err()
}
