package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

const MaxAttachmentBytes = 25 * 1024 * 1024

//go:embed scripts/attachment.py
var attachmentScript string

var safeAttachmentCharacters = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func attachmentFilename(name string) (string, error) {
	if name == "" || len(name) > 255 || strings.ContainsAny(name, "/\\\x00\r\n") || name == "." || name == ".." {
		return "", errors.New("invalid attachment filename")
	}
	filename := safeAttachmentCharacters.ReplaceAllString(name, "_")
	if len(filename) > 200 {
		filename = filename[:200]
	}
	if strings.HasPrefix(filename, ".") {
		filename = "file-" + filename
	}
	return filename, nil
}

// UserAttachmentTarget applies the same main-agent API boundary as submitInput.
func (engine *Engine) UserAttachmentTarget(agentID string) (domain.Agent, error) {
	var record domain.Agent
	if !inputIDPattern.MatchString(agentID) {
		return record, errors.New("invalid agent ID")
	}
	if err := engine.Store.Load("agent", agentID, &record); err != nil {
		return record, err
	}
	if record.Role != "main" || record.ParentID != "" {
		return record, errors.New("send attachments to the main agent")
	}
	var project domain.Project
	if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
		return record, err
	}
	return record, nil
}

func rasterMIME(data []byte) string {
	mime := http.DetectContentType(data)
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return mime
	}
	return ""
}

// UploadAttachment consumes a bounded stream, never a caller-chosen path.
func (engine *Engine) UploadAttachment(ctx context.Context, agentID, id, name, _ string, source io.Reader) (domain.Attachment, error) {
	var result domain.Attachment
	if !inputIDPattern.MatchString(id) {
		return result, errors.New("invalid attachment ID")
	}
	filename, err := attachmentFilename(name)
	if err != nil {
		return result, err
	}
	record, err := engine.UserAttachmentTarget(agentID)
	if err != nil {
		return result, err
	}
	select {
	case engine.attachmentGate <- struct{}{}:
		defer func() { <-engine.attachmentGate }()
	case <-ctx.Done():
		return result, ctx.Err()
	case <-engine.ctx.Done():
		return result, engine.ctx.Err()
	}
	data, err := io.ReadAll(io.LimitReader(source, MaxAttachmentBytes+1))
	if err != nil {
		return result, err
	}
	if len(data) > MaxAttachmentBytes {
		return result, errors.New("attachment exceeds 25 MiB")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	previous, old, err := engine.Store.Attachment(agentID, id)
	if err == nil {
		if previous.ProjectID != record.ProjectID || previous.Name != name || !bytes.Equal(old, data) {
			return result, errors.New("attachment ID conflict")
		}
		return previous, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	result = domain.Attachment{ID: id, AgentID: agentID, ProjectID: record.ProjectID, Name: name, MimeType: http.DetectContentType(data), Size: int64(len(data))}
	if len(data) == 0 {
		result.MimeType = "application/octet-stream"
	}
	result, err = engine.stageAttachment(ctx, record, result, filename, data)
	if err != nil {
		return result, err
	}
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	if current, err := engine.UserAttachmentTarget(agentID); err != nil || current.ProjectID != record.ProjectID {
		return result, errors.New("attachment scope no longer exists")
	}
	return result, engine.Store.SaveAttachment(result, data)
}

func (engine *Engine) stageAttachment(ctx context.Context, record domain.Agent, attachment domain.Attachment, filename string, data []byte) (domain.Attachment, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	stop := context.AfterFunc(engine.ctx, cancel)
	defer stop()
	releaseEnvironment := engine.useEnvironment(record.ID)
	defer releaseEnvironment()
	connection, machine, project, release, err := engine.connectForJob(ctx, record, &executionRequest{MachineID: attachment.MachineID})
	if err != nil {
		return attachment, err
	}
	defer release()
	defer connection.Close()
	temporary := agentTemporaryDirectory(record.ID)
	if err := prepareRemoteWorkspace(ctx, connection, project.Directory, temporary); err != nil {
		return attachment, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	baseCommand := "python3 -I -c " + remote.Quote(attachmentScript) + " " + remote.Quote(temporary) + " " + remote.Quote(attachment.ID) + " " + remote.Quote(filename) + " " + fmt.Sprint(len(data)) + " " + remote.Quote(digest)
	execution := remote.Execution{AgentID: record.ID, ProjectID: project.ID, Workspace: project.Directory, TemporaryDirectory: temporary, Command: baseCommand + " check " + remote.Quote(attachment.Name), Tooling: true}
	checkCommand, level, err := engine.sandboxWithRecovery(ctx, connection, machine, execution, false)
	if err != nil {
		return attachment, err
	}
	if err := engine.rememberExecutionSandbox(machine, level, false); err != nil {
		return attachment, err
	}
	if level == remote.SandboxDisabled {
		return attachment, errors.New("attachments require an isolated agent sandbox")
	}
	state, err := remote.Run(ctx, connection.Client, checkCommand)
	if err != nil {
		return attachment, fmt.Errorf("check attachment: %w", err)
	}
	if strings.TrimSpace(state) != "present" {
		execution.Command = baseCommand + " upload " + remote.Quote(attachment.Name)
		command, _, err := engine.sandboxWithRecovery(ctx, connection, machine, execution, false)
		if err != nil {
			return attachment, err
		}
		if _, err := remote.RunPipe(ctx, connection.Client, command, bytes.NewReader(data)); err != nil {
			return attachment, fmt.Errorf("stage attachment: %w", err)
		}
	}
	attachment.Path = path.Join(temporary, "attachments", attachment.ID, filename)
	attachment.MachineID = machine.ID
	return attachment, nil
}

func (engine *Engine) AttachmentImage(agentID, id string) (domain.Attachment, []byte, error) {
	record, err := engine.UserAttachmentTarget(agentID)
	if err != nil {
		return domain.Attachment{}, nil, err
	}
	attachment, data, err := engine.Store.Attachment(agentID, id)
	if err != nil {
		return attachment, nil, err
	}
	if attachment.AgentID != agentID || attachment.ID != id || attachment.ProjectID != record.ProjectID || rasterMIME(data) == "" || rasterMIME(data) != attachment.MimeType {
		return attachment, nil, errors.New("not a raster image in this scope")
	}
	return attachment, data, nil
}

// Hydrate a private outgoing request copy, never persisted history.
func (engine *Engine) attachmentRequestMessages(messages []domain.Message) []domain.Message {
	result := append([]domain.Message(nil), messages...)
	for i := range result {
		message := &result[i]
		if message.Role != "user" || len(message.Attachments) == 0 {
			continue
		}
		message.Images = nil
		for _, metadata := range message.Attachments {
			attachment, data, err := engine.Store.Attachment(message.AgentID, metadata.ID)
			if err != nil || attachment != metadata || metadata.AgentID != message.AgentID {
				continue
			}
			if rasterMIME(data) != "" && rasterMIME(data) == metadata.MimeType {
				message.Images = append(message.Images, domain.AttachmentImage{MimeType: metadata.MimeType, Data: data})
			}
		}
	}
	return result
}

// Durable bytes restore missing guest /tmp files after a restart/reset without
// ever replacing changed files, resetting a VM, or widening host isolation.
func (engine *Engine) ensureMessageAttachments(ctx context.Context, agentID string, messages []domain.Message) error {
	var current domain.Agent
	if err := engine.Store.Load("agent", agentID, &current); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		for _, metadata := range message.Attachments {
			key := metadata.AgentID + ":" + metadata.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			var owner domain.Agent
			if err := engine.Store.Load("agent", metadata.AgentID, &owner); err != nil {
				return err
			}
			if owner.ProjectID != current.ProjectID || owner.ProjectID != metadata.ProjectID || owner.ID != message.AgentID {
				return errors.New("attachment message scope mismatch")
			}
			attachment, data, err := engine.Store.Attachment(owner.ID, metadata.ID)
			if err != nil {
				return err
			}
			if attachment != metadata {
				return errors.New("attachment metadata mismatch")
			}
			filename, err := attachmentFilename(metadata.Name)
			if err != nil {
				return err
			}
			if _, err := engine.stageAttachment(ctx, owner, metadata, filename, data); err != nil {
				return err
			}
		}
	}
	return nil
}

func (engine *Engine) inputAttachments(record domain.Agent, ids []string) ([]domain.Attachment, error) {
	if len(ids) > 10 {
		return nil, errors.New("at most 10 attachments per message")
	}
	attachments := make([]domain.Attachment, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if !inputIDPattern.MatchString(id) || seen[id] {
			return nil, errors.New("invalid or duplicate attachment ID")
		}
		seen[id] = true
		attachment, data, err := engine.Store.Attachment(record.ID, id)
		if err != nil {
			return nil, errors.New("attachment not found in agent scope")
		}
		if attachment.AgentID != record.ID || attachment.ProjectID != record.ProjectID || attachment.Size != int64(len(data)) || attachment.Size > MaxAttachmentBytes {
			return nil, errors.New("attachment scope or size mismatch")
		}
		attachments = append(attachments, attachment)
	}
	return attachments, validateAttachmentSizes(attachments)
}

func validateAttachmentSizes(attachments []domain.Attachment) error {
	if len(attachments) > 10 {
		return errors.New("at most 10 attachments per message")
	}
	var total int64
	for _, attachment := range attachments {
		if attachment.Size < 0 || attachment.Size > MaxAttachmentBytes {
			return errors.New("attachment exceeds 25 MiB")
		}
		total += attachment.Size
	}
	if total > 100*1024*1024 {
		return errors.New("attachments exceed 100 MiB per message")
	}
	return nil
}
