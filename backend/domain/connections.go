package domain

import "time"

// SSHHostKey is the identity observed during the actual SSH handshake. Empty
// fields mean that the connection did not require a trust decision.
type SSHHostKey struct {
	Host        string `json:"host"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"publicKey"`
}

type MachineConnectionCheck struct {
	Connected bool          `json:"connected"`
	Error     string        `json:"error"`
	HostKey   SSHHostKey    `json:"hostKey"`
	Sandbox   SandboxStatus `json:"sandbox"`
}

// SandboxStatus is the last observed selection, separate from configuration.
// An empty Level means detection failed; it never means execution without isolation.
type SandboxStatus struct {
	MachineID string    `json:"machineId"`
	Mode      string    `json:"mode"`
	Level     string    `json:"level"`
	CheckedAt time.Time `json:"checkedAt"`
	Error     string    `json:"error"`
}

type EditorSession struct {
	Directory string `json:"directory"`

	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	MachineID   string `json:"machineId"`
	MachineName string `json:"machineName"`

	URL string `json:"url"`

	AgentID string `json:"agentId"`
	Sandbox string `json:"sandbox"`
}

type EditorStartResult struct {
	Session EditorSession `json:"session"`
	Error   string        `json:"error"`
	HostKey SSHHostKey    `json:"hostKey"`
}
