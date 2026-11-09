package domain

import "time"

// LiveResponse is transient display state, never conversation or provider input.
// Snapshots replace a draft; deltas append to the previous revision of that draft.
type LiveResponse struct {
	ID      string `json:"id"`
	AgentID string `json:"agentId"`

	Text  string `json:"text"`
	Delta string `json:"delta,omitempty"`

	Revision uint64 `json:"revision"`
	Snapshot bool   `json:"snapshot"`

	Status string `json:"status"`

	CreatedAt time.Time `json:"createdAt"`
}
