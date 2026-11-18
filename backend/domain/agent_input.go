package domain

import "time"

// AgentInput is the durable receipt for an instruction or question answer.
// Queued messages enter model and approval history only when their turn starts;
// question answers commit their receipt and already-delivered context together.
type AgentInput struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentId"`
	ProjectID string `json:"projectId"`

	Text          string       `json:"text"`
	Mode          string       `json:"mode"`
	Status        string       `json:"status"`
	AttachmentIDs []string     `json:"attachmentIds"`
	Attachments   []Attachment `json:"attachments"`

	CreatedAt   time.Time `json:"createdAt"`
	DeliveredAt time.Time `json:"deliveredAt"`
}

type AgentRunState struct {
	AgentID           string     `json:"agentId"`
	Phase             string     `json:"phase"`
	Error             string     `json:"error"`
	HostKey           SSHHostKey `json:"hostKey"`
	RepositoryStep    string     `json:"repositoryStep"`
	RepositoryPercent int        `json:"repositoryPercent"`
}
