package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestShortChatDeliverFileUsesOnlyPinnedWorkspace(t *testing.T) {
	hostWorkspace := t.TempDir()
	shell := shellPeerHandler("/bin/sh", os.Environ())
	var ownerRoot atomic.Value
	var reads atomic.Int32
	engine, projectAgent, project := vmExecutionTestEngine(t, nil, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		root, _ := ownerRoot.Load().(string)
		if root == "" || !strings.HasPrefix(command, "exec /bin/sh -c 'exec python3 -I -u -c ") || !strings.Contains(command, root) {
			t.Error("delivery attempted an operation other than the bounded reader on the pinned owner workspace")
			return 1
		}

		reads.Add(1)
		// Substitute the fixture workspace without altering the remote shell
		// wrapper or the helper's descriptor-relative file operation.
		return shell(strings.Replace(command, root, hostWorkspace, 1), input, stdout, stderr)
	})
	owner := addTestChatUser(t, engine, "delivery-owner")
	otherOwner := addTestChatUser(t, engine, "delivery-outsider")
	chat := createTestShortChat(t, engine, owner)
	sibling := createTestShortChat(t, engine, owner)
	scope, chatDirectory := engine.shortChatWorkspace(chat)
	_, siblingDirectory := engine.shortChatWorkspace(sibling)
	ownerRoot.Store("/tmp/zentrale-" + engine.installationID + "-workspace-" + scope)
	ctx := t.Context()

	if _, err := engine.deliverShortChatFile(ctx, chat, "files/result.txt"); err == nil {
		t.Fatal("delivery was allowed while short-chat execution was disabled")
	}
	settings := ShortChatSettings{MachineID: project.MachineIDs[0]}
	if err := engine.Store.SetSetting("short_chat_execution", JSON(settings)); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.deliverShortChatFile(ctx, chat, "files/result.txt"); !errors.Is(err, errShortChatFileNotFound) || reads.Load() != 0 {
		t.Fatalf("new chat did not fail without remote work: reads=%d err=%v", reads.Load(), err)
	}

	placement := shortChatVMPlacement{TargetMachineID: settings.MachineID, MemberID: settings.MachineID, Stopped: true}
	if err := engine.Store.Save(shortChatVMKind, owner, "", placement); err != nil {
		t.Fatal(err)
	}
	forged := chat
	forged.ChatOwnerID = otherOwner
	for _, record := range []domain.Agent{projectAgent, forged} {
		if _, err := engine.deliverShortChatFile(ctx, record, "files/result.txt"); err == nil {
			t.Fatal("unrelated agent or forged owner was accepted")
		}
	}
	for _, name := range []string{"", "/etc/hosts", "../result.txt", "files/../result.txt", "files//result.txt", "files\\result.txt"} {
		if _, err := engine.deliverShortChatFile(ctx, chat, name); !errors.Is(err, errShortChatFileNotFound) {
			t.Fatalf("invalid path %q: %v", name, err)
		}
	}
	if reads.Load() != 0 {
		t.Fatalf("invalid scopes or paths reached the host: %d", reads.Load())
	}

	chatRoot := filepath.Join(hostWorkspace, chatDirectory)
	siblingRoot := filepath.Join(hostWorkspace, siblingDirectory)
	if err := os.MkdirAll(filepath.Join(chatRoot, "files"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(chatRoot, siblingRoot); err != nil {
		t.Fatal(err)
	}
	const content = "generated in this chat"
	filename := "résultat.txt"
	resultPath := filepath.Join(chatRoot, "files", filename)
	if err := os.WriteFile(resultPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(resultPath, filepath.Join(chatRoot, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(resultPath), filepath.Join(chatRoot, "linked-dir")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"linked.txt", "linked-dir/" + filename, "files", "missing.txt"} {
		if _, err := engine.deliverShortChatFile(ctx, chat, name); !errors.Is(err, errShortChatFileNotFound) {
			t.Fatalf("invalid file %q was not rejected by the bounded reader: %v", name, err)
		}
	}
	if _, err := engine.deliverShortChatFile(ctx, sibling, "files/"+filename); !errors.Is(err, errShortChatFileNotFound) {
		t.Fatalf("sibling reached a different chat through a directory symlink: %v", err)
	}

	largeFile := filepath.Join(chatRoot, "large.bin")
	if err := os.WriteFile(largeFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(largeFile, MaxAttachmentBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.deliverShortChatFile(ctx, chat, "large.bin"); !errors.Is(err, ErrWorkspaceFileTooLarge) {
		t.Fatalf("oversized file was not rejected: %v", err)
	}

	if err := engine.executeCalls(ctx, chat, []providers.Call{{
		ID: "delivery-call", Name: domain.DeliverFileTool, Arguments: map[string]any{"path": "./files/" + filename},
	}}); err != nil {
		t.Fatal(err)
	}
	messages := readBranchMessages(t, engine, chat.ID)
	if len(messages) != 1 || len(messages[0].Attachments) != 1 || messages[0].ToolName != domain.DeliverFileTool {
		t.Fatalf("stable attachment metadata missing from stored tool result: %+v", messages)
	}
	assertDeliveredFile(t, engine, chat.ID, filename, content, messages[0])
	if stored, ok, err := engine.loadShortChatPlacement(owner, settings.MachineID, ""); err != nil || !ok || stored != placement {
		t.Fatalf("delivery changed VM placement or stopped state: %+v, %t, %v", stored, ok, err)
	}

	seedBranchMessages(t, engine, chat, []domain.Message{{ID: "after-delivery", Role: "user", Content: "Thank you"}})
	branch, err := engine.BranchConversation(ctx, owner, chat.ID, "after-delivery", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.StopAndDeleteAgent(ctx, chat.ID); err != nil {
		t.Fatal(err)
	}
	assertDeliveredFile(t, engine, branch.ID, filename, content, readBranchMessages(t, engine, branch.ID)[0])
}

func assertDeliveredFile(t *testing.T, engine *Engine, chatID, filename, content string, message domain.Message) {
	t.Helper()
	var result struct {
		Result domain.FileDelivery `json:"result"`
	}
	if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
		t.Fatal(err)
	}
	if len(message.Attachments) != 1 || result.Result != message.Attachments[0].Delivery() || message.Attachments[0].AgentID != chatID {
		t.Fatalf("delivery result and transcript attachment do not agree in the current chat: %+v", message)
	}
	meta, data, err := engine.DownloadAttachment(chatID, message.Attachments[0].ID)
	if err != nil || string(data) != content || meta.Name != filename || meta.Path != "" || meta.MachineID != "" {
		t.Fatalf("delivered file missing from scoped blob store: %+v, %q, %v", meta, data, err)
	}
}

func TestTruncatedShortChatDeliveryIsNeverStored(t *testing.T) {
	engine, _ := testEngine(t, nil)
	chat := createTestShortChat(t, engine, addTestChatUser(t, engine, "truncated-delivery-owner"))
	id := domain.DeliveredAttachmentPrefix + NewID()
	if _, err := engine.uploadAttachment(context.Background(), chat.ID, id, "short.txt", 10, strings.NewReader("short")); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated attachment returned %v", err)
	}
	if _, _, err := engine.DownloadAttachment(chat.ID, id); err == nil {
		t.Fatal("partially transferred file was made downloadable")
	}
}
