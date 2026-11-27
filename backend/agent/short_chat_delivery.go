package agent

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

var errShortChatFileNotFound = errors.New("regular file not found in this chat's code workspace")

func shortChatDeliveryPath(filename string) (string, error) {
	filename = strings.TrimPrefix(filename, "./")
	if filename == "" || len(filename) > 4096 || path.IsAbs(filename) ||
		strings.ContainsAny(filename, "\x00\r\n\\") {
		return "", errShortChatFileNotFound
	}

	for component := range strings.SplitSeq(filename, "/") {
		if component == "" || component == "." || component == ".." {
			return "", errShortChatFileNotFound
		}
	}

	if _, err := attachmentFilename(path.Base(filename)); err != nil {
		return "", errShortChatFileNotFound
	}

	return filename, nil
}

// Read from the exact existing synthetic workspace on its pinned host. This
// is neither a guest invocation nor permission to prepare a new workspace.
func (engine *Engine) deliverShortChatFile(ctx context.Context, chat domain.Agent, filename string) (domain.FileDelivery, error) {
	var delivery domain.FileDelivery
	filename, err := shortChatDeliveryPath(filename)
	if err != nil {
		return delivery, err
	}

	if err := engine.verifyShortChatVMOwner(chat); err != nil {
		return delivery, err
	}

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	stop := context.AfterFunc(engine.ctx, cancel)
	defer stop()

	scope, chatDirectory := engine.shortChatWorkspace(chat)
	entry, _ := engine.vmOperations.LoadOrStore(scope, &vmOperation{gate: make(chan struct{}, 1)})
	operation := entry.(*vmOperation)
	if err := operation.Lock(ctx); err != nil {
		return delivery, err
	}
	defer operation.Unlock()

	if err := engine.verifyShortChatVMOwner(chat); err != nil {
		return delivery, err
	}

	settings, err := engine.ShortChatSettings()
	if err != nil || !settings.enabled() {
		return delivery, errors.New("short-chat execution is disabled")
	}

	placement, pinned, err := engine.loadShortChatPlacement(chat.ChatOwnerID, settings.MachineID, settings.PoolID)
	if err != nil {
		return delivery, err
	}
	if !pinned {
		return delivery, errShortChatFileNotFound
	}

	machine, err := engine.Remote.Connector.Machine(placement.MemberID)
	if err != nil {
		return delivery, errors.New("the existing short-chat compute target is unavailable")
	}
	if _, err := remote.ShortChatMachine(machine); err != nil {
		return delivery, err
	}

	connection, err := engine.Remote.Connector.Dial(ctx, placement.MemberID)
	if err != nil {
		return delivery, errors.New("could not reach the existing short-chat compute target")
	}
	defer connection.Close()

	workspace, err := remote.ShortChatWorkspacePath(connection, scope)
	if err != nil {
		return delivery, err
	}

	// Include the chat directory in the descriptor-relative traversal; even
	// if it has been replaced by a symlink, the reader rejects the entire path.
	command := workspaceFileCommand(workspace, chatDirectory+"/"+filename, MaxAttachmentBytes)
	err = streamWorkspaceFile(ctx, connection, command, MaxAttachmentBytes, func(size int64, stream io.Reader) error {
		id := domain.DeliveredAttachmentPrefix + NewID()
		attachment, err := engine.uploadAttachment(ctx, chat.ID, id, path.Base(filename), size, stream)
		if err != nil {
			return err
		}
		delivery = attachment.Delivery()
		return nil
	})

	switch {
	case err == nil, errors.Is(err, ErrWorkspaceFileTooLarge):
		return delivery, err
	case errors.Is(err, ErrWorkspaceFileNotFound):
		return delivery, errShortChatFileNotFound
	case ctx.Err() != nil:
		return delivery, ctx.Err()
	default:
		// Host diagnostics may contain absolute paths, which the tool must
		// never expose as workspace paths or URLs.
		return delivery, errors.New("could not read and store the generated file")
	}
}
