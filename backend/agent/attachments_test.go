package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
	a, err := engine.UploadAttachment(context.Background(), record.ID, "picture", "picture.png", "image/png", bytes.NewReader(data))
	if err != nil {
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
		if strings.Contains(command, " 'ensure'") {
			fmt.Fprintln(stdout, "microvm")
			return 0
		}
		_, _ = io.Copy(io.Discard, input)
		if strings.Contains(command, " 'stage'") {
			stages.Add(1)
			fmt.Fprintln(stdout, `ZENTRALE_VM_RECOVERY_REQUIRED {"reason":"unresponsive"}`)
			return 75
		}
		if strings.Contains(command, " 'stop'") || strings.Contains(command, " 'reset'") {
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

func TestAttachmentRestagesAfterConfirmedVMInterruption(t *testing.T) {
	for _, tc := range []struct {
		name       string
		interrupt  int32
		exitStatus int
		runs       int32
	}{
		{"check", 1, 77, 2},
		{"upload acknowledgement", 2, 77, 3},
		{"ordinary failure with a marker", 1, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stages, runs, stops atomic.Int32
			shell := shellPeerHandler("/bin/sh", os.Environ())
			engine, record, _ := vmExecutionTestEngine(t, nil, func(command string, input io.Reader, stdout, stderr io.Writer) int {
				if !strings.Contains(command, "<zentrale-microvm>") {
					return shell(command, input, stdout, stderr)
				}
				command = strings.ReplaceAll(command, `'"'"'`, `'`)
				switch {
				case strings.Contains(command, " 'ensure'"), strings.Contains(command, " 'select'"):
					fmt.Fprintln(stdout, "microvm")
				case strings.Contains(command, " 'stage'"):
					io.Copy(io.Discard, input)
					fmt.Fprintf(stdout, "/tmp/test-vm/session/requests/%032x\n", stages.Add(1))
				case strings.Contains(command, " 'stop'"), strings.Contains(command, " 'reset'"):
					stops.Add(1)
					fmt.Fprintln(stdout, `{}`)
				case strings.Contains(command, " 'run'"):
					run := runs.Add(1)
					if !strings.Contains(command, fmt.Sprintf("/%032x", run)) {
						t.Error("replayed a VM invocation instead of staging a fresh one")
					}
					if tc.interrupt == 2 && run == 2 {
						data, err := io.ReadAll(input)
						if err != nil || string(data) != "attachment" {
							t.Error("upload did not receive the original bytes", err)
						}
					}
					if run == tc.interrupt {
						fmt.Fprintln(stderr, `ZENTRALE_VM_INVOCATION_INTERRUPTED {"reason":"supervisor_stopped"}`)
						return tc.exitStatus
					}
					if tc.interrupt == 2 && run == 1 {
						fmt.Fprintln(stdout, "missing")
					} else {
						fmt.Fprintln(stdout, "present")
					}
				}
				return 0
			})
			release := engine.useEnvironment(record.ID)
			defer release()
			attachment := domain.Attachment{ID: "document", AgentID: record.ID, ProjectID: record.ProjectID, Name: "document.txt"}
			result, err := engine.stageAttachment(context.Background(), record, attachment, attachment.Name, []byte("attachment"))
			if (err == nil) != (tc.exitStatus == 77) || runs.Load() != tc.runs || stages.Load() != tc.runs || stops.Load() != 0 {
				t.Fatalf("attachment=%+v error=%v stages=%d runs=%d stops=%d", result, err, stages.Load(), runs.Load(), stops.Load())
			}
		})
	}
}

func TestAttachmentRestoresOnCurrentMachineAfterProjectMove(t *testing.T) {
	data := []byte("\x89PNG\r\n\x1a\nimage")
	shell := shellPeerHandler("/bin/sh", os.Environ())
	var stages, runs atomic.Int32
	engine, record, project := vmExecutionTestEngine(t, nil, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		if !strings.Contains(command, "<zentrale-microvm>") {
			return shell(command, input, stdout, stderr)
		}
		command = strings.ReplaceAll(command, `'"'"'`, `'`)
		switch {
		case strings.Contains(command, " 'ensure'"), strings.Contains(command, " 'select'"):
			fmt.Fprintln(stdout, "microvm")
		case strings.Contains(command, " 'stage'"):
			io.Copy(io.Discard, input)
			fmt.Fprintf(stdout, "/tmp/test-vm/session/requests/%032x\n", stages.Add(1))
		case strings.Contains(command, " 'run'"):
			if runs.Add(1) == 1 {
				fmt.Fprintln(stdout, "missing")
			} else if actual, err := io.ReadAll(input); err != nil || !bytes.Equal(actual, data) {
				t.Errorf("staged wrong attachment: %v", err)
			}
		}
		return 0
	})
	attachment := domain.Attachment{
		ID: "picture", AgentID: record.ID, ProjectID: project.ID,
		Name: "picture.png", MimeType: "image/png", Size: int64(len(data)),
		Path: "/old/scratch/attachments/picture/picture.png", MachineID: "previous-machine",
	}
	if err := engine.Store.SaveAttachment(attachment, data); err != nil {
		t.Fatal(err)
	}
	message := domain.Message{AgentID: record.ID, Role: "user", Attachments: []domain.Attachment{attachment}}
	history := []domain.Message{message, message}
	request, err := engine.prepareAttachmentRequestMessages(context.Background(), record.ID, history)
	if err != nil {
		t.Fatal(err)
	}
	if runs.Load() != 2 || stages.Load() != 2 {
		t.Fatalf("expected one check and one upload, got %d calls", runs.Load())
	}
	for _, prepared := range request {
		got := prepared.Attachments[0]
		if got.MachineID != project.MachineIDs[0] || got.Path != "/tmp/zentrale/attachments/picture/picture.png" {
			t.Fatalf("outgoing attachment uses the wrong machine or path: %+v", got)
		}
		if len(prepared.Images) != 1 || !bytes.Equal(prepared.Images[0].Data, data) {
			t.Fatal("request-local image hydration failed")
		}
	}
	stored, _, err := engine.Store.Attachment(record.ID, attachment.ID)
	if err != nil || stored != attachment || history[0].Attachments[0] != attachment || len(history[0].Images) != 0 {
		t.Fatal("restoration modified persisted or original message metadata", err)
	}
	encoded, err := json.Marshal(request)
	if err != nil || strings.Contains(string(encoded), "base64") || strings.Contains(string(encoded), "Images") {
		t.Fatal("attachment bytes entered the message JSON", err)
	}
}

func TestModelTurnStagesAttachmentAfterBusyVMBecomesReady(t *testing.T) {
	data := []byte("document\x00\xff")
	var stages, prepares, checks, uploads, stops, modelCalls atomic.Int32
	shell := shellPeerHandler("/bin/sh", os.Environ())
	provider := providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		modelCalls.Add(1)
		attachments := request.Messages[0].Attachments
		if checks.Load() != 1 || uploads.Load() != 1 || len(attachments) != 1 {
			t.Error("model received a request before the attachment was staged")
		} else if attachments[0].Path != "/tmp/zentrale/attachments/document/document.txt" || attachments[0].MachineID == "" {
			t.Errorf("model received an attachment without the correct VM placement: %+v", attachments[0])
		}

		return providers.Result{Text: "ready"}, nil
	})
	engine, record, _ := vmExecutionTestEngine(t, provider, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		if !strings.Contains(command, "<zentrale-microvm>") {
			return shell(command, input, stdout, stderr)
		}
		command = strings.ReplaceAll(command, `'"'"'`, `'`)
		switch {
		case strings.Contains(command, " 'select'"), strings.Contains(command, " 'ensure'"):
			fmt.Fprintln(stdout, "microvm")
		case strings.Contains(command, " 'prepare'"):
			prepares.Add(1)
			fmt.Fprintln(stdout, `{"os":"fedora","version":"45","bootId":"fixture"}`)
		case strings.Contains(command, " 'stage'"):
			io.Copy(io.Discard, input)
			stage := stages.Add(1)
			if stage <= 3 {
				fmt.Fprintln(stderr, `ZENTRALE_VM_RECOVERY_REQUIRED {"reason":"guest_busy"}`)
				return 75
			}
			fmt.Fprintf(stdout, "/tmp/test-vm/session/requests/%032x\n", stage)
		case strings.Contains(command, " 'run'"):
			payload, err := io.ReadAll(input)
			if err != nil {
				t.Error(err)
				return 1
			}

			switch {
			case len(payload) == 0:
				checks.Add(1)
				fmt.Fprintln(stdout, "missing")
			case bytes.Equal(payload, data):
				uploads.Add(1)
			case !bytes.Contains(payload, []byte("clear-project-git")):
				t.Error("unexpected guest invocation or changed upload bytes")
				return 1
			}
		case strings.Contains(command, " 'stop'"), strings.Contains(command, " 'reset'"):
			stops.Add(1)
		}
		return 0
	})

	attachment, err := engine.UploadAttachment(context.Background(), record.ID, "document", "document.txt", "application/octet-stream", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if stages.Load() != 0 || prepares.Load() != 0 || attachment.Path != "" || attachment.MachineID != "" {
		t.Fatalf("upload should not touch the VM: stages=%d prepares=%d receipt=%+v", stages.Load(), prepares.Load(), attachment)
	}

	release := engine.useEnvironment(record.ID)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	request := providers.Request{Messages: []domain.Message{{
		Role: "user", AgentID: record.ID, Attachments: []domain.Attachment{attachment},
	}}}
	_, reply, err := engine.completeVisible(ctx, record.ID, request)
	if err != nil || reply.Content != "ready" || modelCalls.Load() != 1 || prepares.Load() != 1 ||
		checks.Load() != 1 || uploads.Load() != 1 || stops.Load() != 0 {
		t.Fatalf("attachment wait: error=%v reply=%q stages=%d checks=%d uploads=%d prepares=%d modelCalls=%d stops=%d",
			err, reply.Content, stages.Load(), checks.Load(), uploads.Load(), prepares.Load(), modelCalls.Load(), stops.Load())
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
