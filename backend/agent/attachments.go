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
	"slices"
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

// UploadAttachment persists a bounded stream on Zentrale without contacting the
// VM. The agent stages referenced uploads when preparing a model request.
func (engine *Engine) UploadAttachment(ctx context.Context, agentID, id, name, _ string, source io.Reader) (domain.Attachment, error) {
	var result domain.Attachment

	if !inputIDPattern.MatchString(id) {
		return result, errors.New("invalid attachment ID")
	}

	if _, err := attachmentFilename(name); err != nil {
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

	if err := ctx.Err(); err != nil {
		return result, err
	}

	data, err := io.ReadAll(io.LimitReader(source, MaxAttachmentBytes+1))
	if err != nil {
		return result, err
	}

	if len(data) > MaxAttachmentBytes {
		return result, errors.New("attachment exceeds 25 MiB")
	}

	// Revalidate while serializing with agent/project deletion. Neither the
	// body read nor any VM work belongs under this lock.
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	if err := ctx.Err(); err != nil {
		return result, err
	}

	if err := engine.ctx.Err(); err != nil {
		return result, err
	}

	if engine.agentRemoving(agentID) {
		return result, errAgentRemoving
	}

	if current, err := engine.UserAttachmentTarget(agentID); err != nil || current.ProjectID != record.ProjectID {
		return result, errors.New("attachment scope no longer exists")
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

	result = domain.Attachment{
		ID:        id,
		AgentID:   agentID,
		ProjectID: record.ProjectID,
		Name:      name,
		MimeType:  http.DetectContentType(data),
		Size:      int64(len(data)),
	}
	if len(data) == 0 {
		result.MimeType = "application/octet-stream"
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
	connection, machine, project, err := engine.connect(ctx, record)
	if err != nil {
		return attachment, err
	}
	defer connection.Close()
	temporary, err := engine.temporaryDirectory(record.ID)
	if err != nil {
		return attachment, err
	}

	if err := prepareRemoteWorkspace(ctx, connection, project.Directory, temporary); err != nil {
		return attachment, err
	}

	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	baseCommand := strings.Join([]string{
		"python3", "-I", "-c", remote.Quote(attachmentScript),
		`"$TMPDIR"`,
		remote.Quote(attachment.ID),
		remote.Quote(filename),
		fmt.Sprint(len(data)),
		remote.Quote(digest),
	}, " ")

	execution := remote.Execution{
		AgentID:            record.ID,
		ProjectID:          project.ID,
		Workspace:          project.Directory,
		TemporaryDirectory: temporary,
		Tooling:            true,
	}
	var level string
	for attempt := 0; ; attempt++ {
		execution.Command = baseCommand + " check " + remote.Quote(attachment.Name)
		var state string
		state, level, err = engine.runAttachmentOperation(ctx, connection, machine, execution, nil)
		if err != nil {
			err = fmt.Errorf("check attachment: %w", err)
		} else if strings.TrimSpace(state) != "present" {
			execution.Command = baseCommand + " upload " + remote.Quote(attachment.Name)
			_, level, err = engine.runAttachmentOperation(ctx, connection, machine, execution, bytes.NewReader(data))
			if err != nil {
				err = fmt.Errorf("stage attachment: %w", err)
			}
		}
		if err == nil {
			break
		}
		var interrupted *remote.VMInvocationInterrupted
		if attempt > 0 || !errors.As(err, &interrupted) {
			return attachment, err
		}
		// Stage a fresh check after the old invocation has exited. The check
		// also recognizes a complete upload whose acknowledgement was lost.
		select {
		case <-ctx.Done():
			return attachment, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}

	paths := remote.PathsForExecution(machine, level, project.Directory, temporary)
	attachment.Path = path.Join(paths.TemporaryDirectory, "attachments", attachment.ID, filename)
	attachment.MachineID = machine.ID
	return attachment, nil
}

func (engine *Engine) runAttachmentOperation(ctx context.Context, connection *remote.Connection, machine domain.Machine, execution remote.Execution, input io.Reader) (string, string, error) {
	command, level, err := engine.sandboxWithRecovery(ctx, connection, machine, execution, false)
	if err != nil {
		return "", level, err
	}
	if input == nil {
		if err := engine.rememberExecutionSandbox(machine, level, false); err != nil {
			return "", level, err
		}
	}
	if level == remote.SandboxDisabled {
		return "", level, errors.New("attachments require an isolated agent sandbox")
	}
	output, err := remote.RunPipe(ctx, connection, command, input)
	if level == remote.SandboxMicroVM && machine.MicroVMCommand == "" {
		if interrupted := remote.ParseVMInvocationFailure(output, err); interrupted != nil {
			return output, level, interrupted
		}
	}
	return output, level, err
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

func (engine *Engine) prepareTurnAttachments(ctx context.Context, agentID string, messages []domain.Message) ([]domain.Message, error) {
	var prepared []domain.Message
	waiting := false
	err := retryConnectionUntilReady(ctx, time.Second, time.Minute, func(attempt context.Context) error {
		if waiting {
			// The normal environment check can recover an unresponsive VM after
			// its jobs finish. The attachment operation itself cannot stop a VM.
			if err := engine.requireConnection(attempt, agentID); err != nil {
				return err
			}
		}
		var err error
		prepared, err = engine.prepareAttachmentRequestMessages(attempt, agentID, messages)
		return err
	}, func(failure error) {
		waiting = true
		engine.setRunState(agentID, "retrying_connection", failure)
		engine.releaseConnection(agentID)
	})
	if waiting && err == nil {
		engine.setRunState(agentID, "model", nil)
	}
	return prepared, err
}

// Materialize attachments in the current environment and populate VM locations
// only in the outgoing request. Stored upload metadata remains immutable.
func (engine *Engine) prepareAttachmentRequestMessages(ctx context.Context, agentID string, messages []domain.Message) ([]domain.Message, error) {
	if !slices.ContainsFunc(messages, func(message domain.Message) bool {
		return message.Role == "user" && len(message.Attachments) > 0
	}) {
		return messages, nil
	}

	var current domain.Agent
	if err := engine.Store.Load("agent", agentID, &current); err != nil {
		return nil, err
	}
	type preparedAttachment struct {
		metadata domain.Attachment
		image    domain.AttachmentImage
	}
	prepared := make(map[domain.Attachment]preparedAttachment)
	result := slices.Clone(messages)
	for index := range result {
		message := &result[index]
		if message.Role != "user" || len(message.Attachments) == 0 {
			continue
		}
		message.Attachments = slices.Clone(message.Attachments)
		message.Images = nil
		for i, metadata := range message.Attachments {
			if metadata.AgentID != message.AgentID || metadata.ProjectID != current.ProjectID {
				return nil, errors.New("attachment message scope mismatch")
			}

			restored, ok := prepared[metadata]
			if !ok {
				attachment, data, err := engine.restoreMessageAttachment(ctx, current, metadata)
				if err != nil {
					return nil, fmt.Errorf("restore attachment %q: %w", metadata.Name, err)
				}
				restored.metadata = attachment
				if mime := rasterMIME(data); mime != "" && mime == metadata.MimeType {
					restored.image = domain.AttachmentImage{MimeType: mime, Data: data}
				}
				prepared[metadata] = restored
			}
			message.Attachments[i] = restored.metadata
			if restored.image.Data != nil {
				message.Images = append(message.Images, restored.image)
			}
		}
	}
	return result, nil
}

func (engine *Engine) restoreMessageAttachment(ctx context.Context, current domain.Agent, metadata domain.Attachment) (domain.Attachment, []byte, error) {
	var owner domain.Agent
	if err := engine.Store.Load("agent", metadata.AgentID, &owner); err != nil {
		return metadata, nil, err
	}
	if owner.ProjectID != metadata.ProjectID || owner.ID != metadata.AgentID {
		return metadata, nil, errors.New("attachment owner scope mismatch")
	}
	attachment, data, err := engine.Store.Attachment(owner.ID, metadata.ID)
	if err != nil {
		return metadata, nil, err
	}
	if attachment != metadata {
		return metadata, nil, errors.New("attachment metadata mismatch")
	}
	filename, err := attachmentFilename(metadata.Name)
	if err != nil {
		return metadata, nil, err
	}
	restored, err := engine.stageAttachment(ctx, current, attachment, filename, data)
	return restored, data, err
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
