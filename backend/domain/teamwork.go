package domain

import "time"

// Teamwork coordinates declared work, not permissions or filesystem locks.
// One atomic board belongs to one public main conversation.
type Teamwork struct {
	ID         string     `json:"id"`
	AgentID    string     `json:"agentId"`
	ProjectID  string     `json:"projectId"`
	Objective  string     `json:"objective"`
	Pattern    string     `json:"pattern"`
	MaxWorkers int        `json:"maxWorkers"`
	Status     string     `json:"status"`
	Revision   int        `json:"revision"`
	Tasks      []TeamTask `json:"tasks"`
	Evidence   string     `json:"evidence"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

type TeamTask struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Objective  string   `json:"objective"`
	Acceptance string   `json:"acceptance"`
	Kind       string   `json:"kind"`
	DependsOn  []string `json:"dependsOn"`
	WritePaths []string `json:"writePaths"`
	OwnerID    string   `json:"ownerId"`
	Status     string   `json:"status"`
	Result     string   `json:"result"`
	Evidence   string   `json:"evidence"`
}
