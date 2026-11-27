package agent

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

const shortChatVMKind = "short_chat_vm_placement"

type shortChatVMPlacement struct {
	TargetMachineID string    `json:"targetMachineId"`
	TargetPoolID    string    `json:"targetPoolId"`
	MemberID        string    `json:"memberId"`
	UsedAt          time.Time `json:"usedAt"`
	Stopped         bool      `json:"stopped"`
}

const shortChatVMIdleTime = 3 * time.Minute

// RunShortChatCommand provisions only when asked to execute a command. The
// target IDs must come from administrator settings, never from tool arguments.
func (engine *Engine) RunShortChatCommand(
	ctx context.Context,
	chat domain.Agent,
	machineID, poolID, command string,
	attachmentIDs ...string,
) (string, error) {
	if (machineID == "") == (poolID == "") {
		return "", errors.New("short-chat execution requires one configured machine or pool")
	}
	if strings.TrimSpace(command) == "" || len(command) > 16<<10 || strings.ContainsRune(command, '\x00') {
		return "", errors.New("a short-chat command must be non-empty and at most 16 KiB")
	}
	if err := engine.verifyShortChatVMOwner(chat); err != nil {
		return "", err
	}
	attachments, err := engine.inputAttachments(chat, attachmentIDs)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	userScope, chatDirectory := engine.shortChatWorkspace(chat)
	entry, _ := engine.vmOperations.LoadOrStore(userScope, &vmOperation{gate: make(chan struct{}, 1)})
	operation := entry.(*vmOperation)
	if err := operation.Lock(ctx); err != nil {
		return "", err
	}
	defer operation.Unlock()

	if err := engine.verifyShortChatVMOwner(chat); err != nil {
		return "", err
	}
	placement, pinned, err := engine.loadShortChatPlacement(chat.ChatOwnerID, machineID, poolID)
	if err != nil {
		return "", err
	}

	// This value is routing-only: never resolve sources, Git credentials, or
	// storage from a real project.
	target := domain.Project{ManagedWorkspace: true}
	if machineID != "" {
		target.MachineIDs = []string{machineID}
	} else {
		target.PoolIDs = []string{poolID}
	}
	connection, machine, err := engine.Remote.ConnectForJob(ctx, target, userScope, placement.MemberID, false, nil)
	if err != nil {
		return "", shortChatVMFailure(ctx)
	}
	defer engine.Remote.Release(userScope)
	defer connection.Close()

	if _, err := remote.ShortChatMachine(machine); err != nil {
		return "", err
	}
	if !pinned {
		placement.MemberID = machine.ID
	}
	placement.UsedAt, placement.Stopped = time.Now().UTC(), false
	if err := engine.Store.Save(shortChatVMKind, chat.ChatOwnerID, "", placement); err != nil {
		return "", errors.New("could not save short-chat VM placement")
	}
	defer func() {
		placement.UsedAt = time.Now().UTC()
		_ = engine.Store.Save(shortChatVMKind, chat.ChatOwnerID, "", placement)
	}()
	if err := engine.verifyShortChatVMOwner(chat); err != nil {
		return "", err
	}

	var input io.Reader
	if len(attachments) != 0 {
		reader, writer := io.Pipe()
		defer reader.Close()
		input = reader
		go func() {
			_ = writer.CloseWithError(engine.writeChatAttachments(writer, chat.ID, attachments))
		}()
	}
	output, err := remote.RunShortChatVM(ctx, connection, machine, userScope, chatDirectory, command, input)
	if err != nil {
		return "", shortChatVMFailure(ctx)
	}
	return output, nil
}

func (engine *Engine) writeChatAttachments(output io.Writer, chatID string, attachments []domain.Attachment) error {
	archive := tar.NewWriter(output)
	for _, attachment := range attachments {
		name, err := attachmentFilename(attachment.Name)
		if err != nil || !inputIDPattern.MatchString(attachment.ID) {
			return errors.New("invalid chat attachment name or ID")
		}
		_, data, err := engine.Store.Attachment(chatID, attachment.ID)
		if err != nil || int64(len(data)) != attachment.Size {
			return errors.New("chat attachment is unavailable")
		}

		header := tar.Header{Name: attachment.ID + "/" + name, Mode: 0600, Size: attachment.Size}
		if err := archive.WriteHeader(&header); err != nil {
			return err
		}
		if _, err := archive.Write(data); err != nil {
			return err
		}
	}
	return archive.Close()
}

func (engine *Engine) reconcileShortChatVMs(ctx context.Context) {
	rows, err := engine.listAll(shortChatVMKind, nil)
	if err != nil {
		return
	}

	for _, row := range rows {
		ownerID := stringArg(row, "id")
		scope := "shortchat-" + shortChatDigest(engine.installationID, ownerID)[:56]
		entry, _ := engine.vmOperations.LoadOrStore(scope, &vmOperation{gate: make(chan struct{}, 1)})
		operation := entry.(*vmOperation)
		select {
		case operation.gate <- struct{}{}:
			engine.stopIdleShortChatVM(ctx, ownerID, scope)
			operation.Unlock()
		case <-ctx.Done():
			return
		default:
			// A command owns this user's VM; never interrupt it.
		}
	}
}

func (engine *Engine) stopIdleShortChatVM(ctx context.Context, ownerID, scope string) {
	var placement shortChatVMPlacement
	if engine.Store.Load(shortChatVMKind, ownerID, &placement) != nil || placement.Stopped {
		return
	}
	if placement.UsedAt.IsZero() {
		placement.UsedAt = time.Now().UTC()
		_ = engine.Store.Save(shortChatVMKind, ownerID, "", placement)
		return
	}
	if time.Since(placement.UsedAt) < shortChatVMIdleTime {
		return
	}

	machine, err := engine.Remote.Connector.Machine(placement.MemberID)
	if err != nil {
		return
	}
	machine, err = remote.ShortChatMachine(machine)
	if err != nil {
		return
	}

	stop, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	connection, err := engine.Remote.Connector.Dial(stop, placement.MemberID)
	if err != nil {
		return
	}
	defer connection.Close()
	if err := remote.StopShortChatVM(stop, connection, machine, scope); err == nil {
		placement.Stopped = true
		_ = engine.Store.Save(shortChatVMKind, ownerID, "", placement)
	}
}

func (engine *Engine) verifyShortChatVMOwner(chat domain.Agent) error {
	var stored domain.Agent
	if chat.ChatOwnerID == "" || chat.ProjectID != "" ||
		engine.Store.Load("agent", chat.ID, &stored) != nil ||
		stored.ChatOwnerID != chat.ChatOwnerID || stored.ProjectID != "" ||
		stored.Role != "main" || stored.ParentID != "" {
		return errors.New("short chat is unavailable")
	}
	return engine.activeChatOwner(stored)
}

func (engine *Engine) shortChatWorkspace(chat domain.Agent) (string, string) {
	// The runner accepts at most 80 characters, including its project- prefix.
	scope := "shortchat-" + shortChatDigest(engine.installationID, chat.ChatOwnerID)[:56]
	return scope, "chat-" + shortChatDigest(scope, chat.ID)
}

func (engine *Engine) loadShortChatPlacement(ownerID, machineID, poolID string) (shortChatVMPlacement, bool, error) {
	var placement shortChatVMPlacement
	err := engine.Store.Load(shortChatVMKind, ownerID, &placement)
	if errors.Is(err, sql.ErrNoRows) {
		return shortChatVMPlacement{TargetMachineID: machineID, TargetPoolID: poolID}, false, nil
	}
	if err != nil {
		return placement, false, errors.New("could not load short-chat VM placement")
	}
	if placement.MemberID == "" || placement.TargetMachineID != machineID || placement.TargetPoolID != poolID {
		// Never silently create a second VM or abandon the existing disk.
		return placement, false, errors.New("short-chat VM is pinned to a different target; explicit migration is required")
	}
	return placement, true, nil
}

func shortChatDigest(scope, value string) string {
	digest := sha256.Sum256([]byte(scope + "\x00" + value))
	return hex.EncodeToString(digest[:])
}

func shortChatVMFailure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("short-chat MicroVM execution failed")
}
