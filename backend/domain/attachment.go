package domain

import "net/url"

// DeliveredAttachmentPrefix is reserved for server-generated short-chat files.
const DeliveredAttachmentPrefix = "delivered-"

const DeliverFileTool = "deliver_file"

// Attachment is immutable, scope-bound metadata. File bytes never belong in
// messages, events, receipts, GraphQL or runtime snapshots.
// New uploads have no VM placement until the agent stages its request copy.
type Attachment struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentId"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	MimeType  string `json:"mimeType"`
	Size      int64  `json:"size"`
	Path      string `json:"path"`
	MachineID string `json:"machineId"`
}

// AttachmentImage exists only on an outgoing provider request copy.
type AttachmentImage struct {
	MimeType string
	Data     []byte
}

type FileDelivery struct {
	Attachment  Attachment `json:"attachment"`
	DownloadURL string     `json:"downloadUrl"`
}

func (attachment Attachment) Delivery() FileDelivery {
	return FileDelivery{
		Attachment: attachment,
		DownloadURL: "/attachments/" + url.PathEscape(attachment.AgentID) + "/" +
			url.PathEscape(attachment.ID) + "?download=1",
	}
}
