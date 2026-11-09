package domain

import "time"

// A conversation has one current goal. Replaced terminal goals remain in history.
// Agent-authored objectives never grant permissions beyond original user input.
type Goal struct {
	ID              string    `json:"id"`
	AgentID         string    `json:"agentId"`
	Objective       string    `json:"objective"`
	Origin          string    `json:"origin"`
	Status          string    `json:"status"`
	RequestedStatus string    `json:"requestedStatus"`
	RequestReason   string    `json:"requestReason"`
	SourceInputIDs  []string  `json:"sourceInputIds"`
	Revision        int       `json:"revision"`
	Continuations   int       `json:"continuations"`
	BlockedAttempts int       `json:"blockedAttempts"`
	Blocker         string    `json:"blocker"`
	Reason          string    `json:"reason"`
	Evidence        string    `json:"evidence"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type GoalReview struct {
	ID         string    `json:"id"`
	AgentID    string    `json:"agentId"`
	GoalID     string    `json:"goalId"`
	ApproverID string    `json:"approverId"`
	MessageID  string    `json:"messageId"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason"`
	Evidence   string    `json:"evidence"`
	NextStep   string    `json:"nextStep"`
	Blocker    string    `json:"blocker"`
	CreatedAt  time.Time `json:"createdAt"`
}
