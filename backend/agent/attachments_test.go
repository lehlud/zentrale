package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func TestAttachmentQueueReceiptsAndHistoryMetadata(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Error("queued input leaked into a model turn")
		return providers.Result{}, nil
	}))
	// Keep the queue inactive without starting a worker; exercise delivery directly.
	engine.mu.Lock()
	engine.running[record.ID] = func() {}
	engine.mu.Unlock()
	defer func() { engine.mu.Lock(); delete(engine.running, record.ID); engine.mu.Unlock() }()
	data := []byte("\x89PNG\r\n\x1a\nimage")
	a := domain.Attachment{ID: "picture", AgentID: record.ID, ProjectID: record.ProjectID, Name: "picture.png", MimeType: "image/png", Size: int64(len(data)), Path: agentTemporaryDirectory(record.ID) + "/attachments/picture/picture.png", MachineID: "test-worker"}
	if err := engine.Store.SaveAttachment(a, data); err != nil {
		t.Fatal(err)
	}
	input, err := engine.SubmitInputWithAttachments(record.ID, "receipt", "", "queue", []string{a.ID})
	if err != nil || input.Status != "queued" || len(input.Attachments) != 1 {
		t.Fatal(input, err)
	}
	if _, err := engine.SubmitInputWithAttachments(record.ID, "receipt", "", "queue", []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.SubmitInput(record.ID, "receipt", "", "queue"); err == nil {
		t.Fatal("receipt attachment mismatch accepted")
	}
	if history, err := engine.loadHistory(record.ID); err != nil || len(history) != 0 {
		t.Fatal("queued input leaked into history", history, err)
	}
	engine.configMu.Lock()
	delivered, err := engine.deliverInputs(record.ID, true)
	engine.configMu.Unlock()
	if err != nil || !delivered {
		t.Fatal(delivered, err)
	}
	history, err := engine.loadHistory(record.ID)
	if err != nil || len(history) != 1 || history[0].Content != "" || len(history[0].Attachments) != 1 {
		t.Fatal(history, err)
	}
	request := engine.attachmentRequestMessages(history)
	if len(request[0].Images) != 1 || len(history[0].Images) != 0 {
		t.Fatal("request-local hydration failed", request, history)
	}
	persisted, _ := json.Marshal(request)
	if strings.Contains(string(persisted), "base64") || strings.Contains(string(persisted), "Images") {
		t.Fatal("blob leaked", string(persisted))
	}
	instructions, err := engine.ownInstructions(record)
	if err != nil || len(instructions) != 1 || !strings.Contains(JSON(instructions), "picture.png") {
		t.Fatal("approver/original-instruction attachment lost", instructions, err)
	}
	if _, err := engine.SubmitInputWithAttachments(record.ID, "receipt", "", "queue", []string{a.ID}); err != nil {
		t.Fatal("delivered receipt retry failed", err)
	}
}

func TestAttachmentSandboxNeverRecoversOrResetsVM(t *testing.T) {
	var stages, stops atomic.Int32
	engine, record, project := executionTestEngine(t, nil, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		command = strings.ReplaceAll(command, `'"'"'`, `'`)
		if strings.Contains(command, " 'ensure' ") {
			fmt.Fprintln(stdout, "microvm")
			return 0
		}
		_, _ = io.Copy(io.Discard, input)
		if strings.Contains(command, " 'stage' ") {
			stages.Add(1)
			fmt.Fprintln(stdout, `ZENTRALE_VM_RECOVERY_REQUIRED {"reason":"unresponsive"}`)
			return 75
		}
		if strings.Contains(command, " 'stop' ") || strings.Contains(command, " 'reset' ") {
			stops.Add(1)
		}
		fmt.Fprint(stdout, `{}`)
		return 0
	})
	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	machine.Sandbox = remote.SandboxMicroVM
	connection, err := engine.Remote.Connector.Dial(context.Background(), machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, _, err = engine.sandboxWithRecovery(context.Background(), connection, machine, remote.Execution{AgentID: record.ID, ProjectID: project.ID, Workspace: project.Directory, Command: "true", Tooling: true}, false)
	var recovery *remote.VMRecoveryRequired
	if !errors.As(err, &recovery) || stages.Load() != 1 || stops.Load() != 0 {
		t.Fatalf("attachment triggered recovery: stages=%d stops=%d error=%v", stages.Load(), stops.Load(), err)
	}
}

func TestAttachmentValidationLimitsAndScope(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) { return providers.Result{}, nil }))
	untitled := record
	untitled.Name = "Neue Unterhaltung"
	if err := engine.nameFirstConversation(&untitled, ""); err != nil || untitled.Name != "Neue Unterhaltung" {
		t.Fatal("attachment-only message erased conversation title", untitled.Name, err)
	}
	for _, name := range []string{"", ".", "..", "../escape", "a/b", "a\\b", "bad\x00", "bad\n"} {
		if _, err := attachmentFilename(name); err == nil {
			t.Fatal("unsafe filename", name)
		}
	}
	if name, err := attachmentFilename("normal file.png"); err != nil || name != "normal_file.png" {
		t.Fatal(name, err)
	}
	if _, err := engine.inputAttachments(record, make([]string, 11)); err == nil {
		t.Fatal("11 attachments allowed")
	}
	for _, test := range []struct {
		sizes []int64
		valid bool
	}{
		{[]int64{0, MaxAttachmentBytes}, true},
		{[]int64{MaxAttachmentBytes, MaxAttachmentBytes, MaxAttachmentBytes, MaxAttachmentBytes}, true},
		{[]int64{MaxAttachmentBytes, MaxAttachmentBytes, MaxAttachmentBytes, MaxAttachmentBytes, 1}, false},
		{[]int64{MaxAttachmentBytes + 1}, false},
		{[]int64{-1}, false},
	} {
		items := make([]domain.Attachment, len(test.sizes))
		for i, size := range test.sizes {
			items[i].Size = size
		}
		if err := validateAttachmentSizes(items); (err == nil) != test.valid {
			t.Fatal(test, err)
		}
	}
	if _, err := engine.inputAttachments(record, []string{"unknown"}); err == nil {
		t.Fatal("unknown attachment allowed")
	}
	data := []byte("file")
	a := domain.Attachment{ID: "file", AgentID: record.ID, ProjectID: "foreign-project", Name: "file", Size: 4}
	if err := engine.Store.SaveAttachment(a, data); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.inputAttachments(record, []string{a.ID}); err == nil {
		t.Fatal("foreign project allowed")
	}
	if _, err := engine.inputAttachments(record, []string{a.ID, a.ID}); err == nil {
		t.Fatal("duplicate allowed")
	}
	for _, role := range []string{"subagent", "approver"} {
		child := domain.Agent{ID: role, Role: role, ProjectID: record.ProjectID, ParentID: record.ID}
		if err := engine.Store.Save("agent", child.ID, child.ProjectID, child); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.UserAttachmentTarget(child.ID); err == nil {
			t.Fatal("non-main allowed", role)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.UploadAttachment(ctx, record.ID, "cancelled", "file", "application/octet-stream", strings.NewReader("file")); err == nil {
		t.Fatal("cancelled upload accepted")
	}
	// No remote handshake is permitted for oversized bodies.
	large := strings.NewReader(strings.Repeat("x", MaxAttachmentBytes+1))
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := engine.UploadAttachment(ctx, record.ID, "large", "large", "application/octet-stream", large); err == nil || !strings.Contains(err.Error(), "25 MiB") {
		t.Fatal(err)
	}
}
