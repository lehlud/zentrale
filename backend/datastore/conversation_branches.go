package datastore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

var errBranchMessageUnavailable = errors.New("choose a user message from this conversation")

func (instance *Instance) ConversationBranches(ctx context.Context, source domain.Agent) ([]domain.Agent, error) {
	if instance == nil || instance.Closed() {
		return nil, errClosed
	}

	rootID := source.BranchRootID
	if rootID == "" {
		rootID = source.ID
	}
	rows, err := instance.readDB().QueryContext(ctx, `SELECT data FROM resources
		WHERE kind='agent' AND parent_id=? AND COALESCE(data->>'chatOwnerId','')=?
		AND data->>'projectId'=? AND data->>'role'='main' AND data->>'parentId'=''
		AND (id=? OR data->>'branchRootId'=?) ORDER BY created_at,id`,
		source.ProjectID, source.ChatOwnerID, source.ProjectID, rootID, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	branches := []domain.Agent{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var branch domain.Agent
		if err := json.Unmarshal(data, &branch); err != nil {
			return nil, err
		}
		branches = append(branches, branch)
	}

	return branches, rows.Err()
}

type branchMessage struct {
	message   domain.Message
	createdAt string // Preserve the exact storage ordering even for imported messages.
}

func branchMessages(tx *sql.Tx, sourceID, messageID string) ([]branchMessage, error) {
	rows, err := tx.Query(`SELECT data,CAST(created_at AS TEXT) FROM resources
		WHERE kind='message' AND parent_id=? AND (created_at,id)<=(
			SELECT created_at,id FROM resources WHERE kind='message' AND parent_id=? AND id=?
		) ORDER BY created_at,id`, sourceID, sourceID, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []branchMessage
	for rows.Next() {
		var data []byte
		var row branchMessage
		if err := rows.Scan(&data, &row.createdAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &row.message); err != nil {
			return nil, err
		}
		if row.message.AgentID != sourceID {
			return nil, errBranchMessageUnavailable
		}
		messages = append(messages, row)
	}

	return messages, rows.Err()
}

// CreateConversationBranch commits a scoped snapshot, a fresh reviewer, and an
// optional accepted input together. It never writes to the source conversation.
func (instance *Instance) CreateConversationBranch(ctx context.Context, source, branch, approver domain.Agent, edit *domain.AgentInput) (domain.Agent, error) {
	if instance == nil || instance.Closed() {
		return domain.Agent{}, errClosed
	}

	tx, err := instance.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Agent{}, err
	}
	defer tx.Rollback()

	messages, err := branchMessages(tx, source.ID, branch.BranchMessageID)
	if err != nil {
		return domain.Agent{}, err
	}
	if len(messages) == 0 {
		return domain.Agent{}, errBranchMessageUnavailable
	}
	point := messages[len(messages)-1].message
	if point.ID != branch.BranchMessageID || point.Role != "user" {
		return domain.Agent{}, errBranchMessageUnavailable
	}
	branch.BranchMessageLineageID = point.Lineage()

	for _, record := range []domain.Agent{branch, approver} {
		if err := insertBranchResource(tx, "agent", record.ID, record.ProjectID, resourceCreatedAt(), record); err != nil {
			return domain.Agent{}, err
		}
	}

	attachments := map[string]domain.Attachment{}
	if edit != nil {
		edited := *edit
		edited.LineageID = point.Lineage()
		edited.Attachments, err = copyBranchAttachments(tx, source, branch.ID, point.Attachments, attachments)
		if err != nil {
			return domain.Agent{}, err
		}
		if strings.TrimSpace(edited.Text) == "" && len(edited.Attachments) == 0 {
			return domain.Agent{}, errors.New("edited instruction must contain text or an attachment")
		}
		for _, attachment := range edited.Attachments {
			edited.AttachmentIDs = append(edited.AttachmentIDs, attachment.ID)
		}
		*edit = edited
		messages = messages[:len(messages)-1]
	}

	ids := make(map[string]string, len(messages))
	for index, row := range messages {
		message := row.message
		message.Attachments, err = copyBranchAttachments(tx, source, branch.ID, message.Attachments, attachments)
		if err != nil {
			return domain.Agent{}, err
		}
		// Prefix positions break timestamp ties without depending on random IDs.
		id := fmt.Sprintf("%s-%08x", branch.ID, index)
		ids[message.ID] = id
		message.LineageID = message.Lineage()
		message.ID, message.AgentID = id, branch.ID
		if err := retargetBranchDelivery(&message); err != nil {
			return domain.Agent{}, err
		}
		if err := insertBranchResource(tx, "message", id, branch.ID, row.createdAt, message); err != nil {
			return domain.Agent{}, err
		}
		if message.Role == "user" {
			if err := insertBranchInstruction(tx, branch, message, row.createdAt); err != nil {
				return domain.Agent{}, err
			}
		}
	}
	if err := copyBranchTranscriptDetails(tx, source.ID, branch.ID, ids); err != nil {
		return domain.Agent{}, err
	}
	if edit != nil {
		if err := insertBranchResource(tx, "agent_input", edit.ID, branch.ID, resourceCreatedAt(), edit); err != nil {
			return domain.Agent{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.Agent{}, err
	}

	return branch, nil
}

// The attachment copy is independent of the source. Keep the tool result URL
// consistent with the attached metadata so historical delivery still works
// after deleting the original conversation.
func retargetBranchDelivery(message *domain.Message) error {
	if message.Role != "tool" || message.ToolName != domain.DeliverFileTool || len(message.Attachments) != 1 {
		return nil
	}
	attachment := message.Attachments[0]
	if !strings.HasPrefix(attachment.ID, domain.DeliveredAttachmentPrefix) {
		return nil
	}

	content, err := json.Marshal(struct {
		Result domain.FileDelivery `json:"result"`
	}{Result: attachment.Delivery()})
	if err != nil {
		return err
	}
	message.Content = string(content)
	return nil
}

func insertBranchResource(tx *sql.Tx, kind, id, parentID, createdAt string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO resources(kind,id,parent_id,data,created_at) VALUES(?,?,?,?,?)`, kind, id, parentID, string(data), createdAt)
	return err
}

func insertBranchInstruction(tx *sql.Tx, branch domain.Agent, message domain.Message, createdAt string) error {
	scopeID := branch.ProjectID
	if branch.ChatOwnerID != "" {
		scopeID = branch.ID
	}
	instruction := struct {
		domain.Message
		RecipientAgentID string `json:"recipientAgentId"`
		Origin           string `json:"origin"`
		Inherited        bool   `json:"inherited"`
	}{message, branch.ID, "user", true}

	return insertBranchResource(tx, "instruction", message.ID, scopeID, createdAt, instruction)
}

func copyBranchAttachments(tx *sql.Tx, source domain.Agent, branchID string, references []domain.Attachment, copied map[string]domain.Attachment) ([]domain.Attachment, error) {
	var attachments []domain.Attachment
	for _, ref := range references {
		if ref.AgentID != source.ID || ref.ProjectID != source.ProjectID {
			return nil, errors.New("attachment belongs to another conversation")
		}
		if attachment, ok := copied[ref.ID]; ok {
			attachments = append(attachments, attachment)
			continue
		}

		var raw []byte
		var size int64
		if err := tx.QueryRow(`SELECT metadata,length(bytes) FROM attachment_blobs WHERE agent_id=? AND project_id=? AND id=?`, source.ID, source.ProjectID, ref.ID).Scan(&raw, &size); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errors.New("attachment is no longer available in the source conversation")
			}
			return nil, err
		}
		var attachment domain.Attachment
		if err := json.Unmarshal(raw, &attachment); err != nil {
			return nil, err
		}
		if attachment.AgentID != source.ID || attachment.ProjectID != source.ProjectID || attachment.ID != ref.ID || size != attachment.Size {
			return nil, errors.New("attachment scope or size mismatch")
		}
		// IDs are already scoped by agent. Keep IDs in historical text valid,
		// but do not inherit a staged path or the old compute target.
		attachment.AgentID, attachment.Path, attachment.MachineID = branchID, "", ""
		metadata, err := json.Marshal(attachment)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO attachment_blobs(agent_id,project_id,id,metadata,bytes)
			SELECT ?,project_id,id,?,bytes FROM attachment_blobs WHERE agent_id=? AND project_id=? AND id=?`,
			branchID, string(metadata), source.ID, source.ProjectID, ref.ID); err != nil {
			return nil, err
		}
		copied[ref.ID] = attachment
		attachments = append(attachments, attachment)
	}

	return attachments, nil
}

// These records decorate historical messages. They are not jobs, approvals,
// or executable actions. Copy rather than fall back to looking up another
// agent's resources: deletion of the source must not strip a fork's history.
func copyBranchTranscriptDetails(tx *sql.Tx, sourceID, branchID string, ids map[string]string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(`SELECT kind,id,data,CAST(created_at AS TEXT) FROM resources
		WHERE kind IN ('file_change','tool_activity') AND parent_id=? ORDER BY kind,id`, sourceID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type detail struct {
		kind, id, createdAt string
		value               map[string]json.RawMessage
	}
	var copies []detail
	fileCounts := map[string]int{}

	for rows.Next() {
		var kind, oldID, createdAt string
		var data []byte
		if err := rows.Scan(&kind, &oldID, &data, &createdAt); err != nil {
			return err
		}

		var value map[string]json.RawMessage
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}

		var agentID, messageID string
		_ = json.Unmarshal(value["agentId"], &agentID)
		_ = json.Unmarshal(value["messageId"], &messageID)
		if messageID == "" || kind == "tool_activity" {
			messageID = oldID
		}
		newMessageID := ids[messageID]
		if agentID != sourceID || newMessageID == "" {
			continue
		}

		id := newMessageID
		if kind == "file_change" {
			count := fileCounts[id]
			fileCounts[id]++
			if count > 0 {
				id = fmt.Sprintf("%s-%03d", id, count)
			}
			value["messageId"], _ = json.Marshal(newMessageID)
		}

		value["id"], _ = json.Marshal(id)
		value["agentId"], _ = json.Marshal(branchID)
		copies = append(copies, detail{kind: kind, id: id, createdAt: createdAt, value: value})
	}

	if err := rows.Err(); err != nil {
		return err
	}

	for _, copy := range copies {
		if err := insertBranchResource(tx, copy.kind, copy.id, branchID, copy.createdAt, copy.value); err != nil {
			return err
		}
	}
	return nil
}
