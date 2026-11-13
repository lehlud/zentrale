package datastore

import (
	"encoding/json"

	"github.com/lehlud/zentrale/domain"
)

// Attachment bytes deliberately live outside resources/events and projections.
func (instance *Instance) Attachment(agentID, id string) (domain.Attachment, []byte, error) {
	var metadata string
	var data []byte
	var attachment domain.Attachment
	err := instance.readDB().QueryRow(`SELECT metadata::VARCHAR,bytes FROM attachment_blobs WHERE agent_id=? AND id=?`, agentID, id).Scan(&metadata, &data)
	if err != nil {
		return attachment, nil, err
	}
	err = json.Unmarshal([]byte(metadata), &attachment)
	return attachment, data, err
}

func (instance *Instance) SaveAttachment(attachment domain.Attachment, data []byte) error {
	metadata, err := json.Marshal(attachment)
	if err != nil {
		return err
	}
	_, err = instance.db.Exec(`INSERT INTO attachment_blobs(agent_id,project_id,id,metadata,bytes) VALUES(?,?,?,?,?)`, attachment.AgentID, attachment.ProjectID, attachment.ID, string(metadata), data)
	return err
}
