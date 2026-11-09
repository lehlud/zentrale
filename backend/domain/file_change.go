package domain

import "time"

// FileChange belongs to the UI transcript, separately from provider messages.
// Diffs must not silently add previous file contents to an agent's read access.
type FileChange struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentId"`
	MessageID string `json:"messageId"`

	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Patch string `json:"patch"`

	Added   int `json:"added"`
	Removed int `json:"removed"`

	Truncated bool   `json:"truncated"`
	Notice    string `json:"notice"`

	CreatedAt time.Time `json:"createdAt"`
}
