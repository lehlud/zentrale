package agent

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestEditConversationBranchesThroughNormalInput(t *testing.T) {
	requests := make(chan providers.Request, 2)
	engine, original := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		requests <- request
		return providers.Result{Text: "new answer", UsageKnown: true}, nil
	}))
	owner := domain.InitialUserID
	projectID := original.ProjectID

	seedBranchMessages(t, engine, original, []domain.Message{
		{ID: "prior", Role: "user", Content: "Keep the answer short"},
		{ID: "first-answer", Role: "assistant", Content: "Hello"},
		{ID: "edit-point", Role: "user", Content: "Old instruction"},
		{ID: "old-continuation", Role: "assistant", Content: "Old answer"},
	})
	seedBranchMessages(t, engine, domain.Agent{ID: original.ApproverID}, []domain.Message{
		{ID: "prior-review", Role: "assistant", Content: "Authorization from source conversation only"},
	})
	before := readBranchMessages(t, engine, original.ID)

	edit := "New instruction"
	branch, err := engine.BranchConversation(t.Context(), owner, original.ID, "edit-point", &edit)
	if err != nil {
		t.Fatal(err)
	}
	if branch.ParentID != "" || branch.ProjectID != projectID || branch.ApproverID == original.ApproverID ||
		branch.BranchRootID != original.ID || branch.BranchParentID != original.ID || branch.BranchMessageID != "edit-point" || branch.BranchMessageLineageID != "edit-point" {
		t.Fatalf("unexpected branch metadata: %+v", branch)
	}

	if record := waitForAgent(t, engine, branch.ID); record.Status != "idle" {
		t.Fatalf("branch input did not finish: %+v", record)
	}
	select {
	case request := <-requests:
		found := map[string]bool{}
		for _, message := range request.Messages {
			found[message.Content] = true
		}
		if !found["Keep the answer short"] || !found["Hello"] || !found[edit] || found["Old instruction"] || found["Old answer"] {
			t.Fatalf("provider saw wrong branch context: %v", found)
		}
	default:
		t.Fatal("branch edit did not invoke the provider")
	}

	history := readBranchMessages(t, engine, branch.ID)
	if len(history) != 4 || history[0].Lineage() != "prior" || history[1].Lineage() != "first-answer" ||
		history[2].Lineage() != "edit-point" || history[2].Content != edit || history[3].Content != "new answer" {
		t.Fatalf("unexpected edited history: %+v", history)
	}
	if after := readBranchMessages(t, engine, original.ID); !reflect.DeepEqual(before, after) {
		t.Fatalf("branch changed source history: before %+v, after %+v", before, after)
	}
	for _, message := range readBranchMessages(t, engine, branch.ApproverID) {
		if strings.Contains(message.Content, "Authorization from source conversation only") {
			t.Fatalf("branch inherited reviewer state: %+v", message)
		}
	}
	if receipt, err := engine.Store.AgentInputs(branch.ID); err != nil || len(receipt) != 0 {
		t.Fatalf("replacement receipt did not complete: %v, %v", receipt, err)
	}
	approval, err := engine.approvalInstructions(branch)
	if err != nil || len(approval.UserInstructions) != 2 || approval.UserInstructions[0]["inherited"] != true ||
		approval.UserInstructions[1]["content"] != edit || approval.UserInstructions[1]["authority"] != "original_user_instruction_at_stated_scope" {
		t.Fatalf("original and replacement instruction authority was confused: %+v, %v", approval, err)
	}

	if _, err := engine.SubmitInput(branch.ID, "followup", "Keep going", "steer"); err != nil {
		t.Fatal(err)
	}
	if record := waitForAgent(t, engine, branch.ID); record.Status != "idle" {
		t.Fatalf("a subsequent branch turn failed: %+v", record)
	}
	if history := readBranchMessages(t, engine, branch.ID); len(history) != 6 || history[4].ID != "followup" {
		t.Fatalf("subsequent turn did not extend the branch: %+v", history)
	}
}

func TestConversationForkAttachmentsSurviveSourceDeletion(t *testing.T) {
	engine, _ := testEngine(t, nil)
	owner := addTestChatUser(t, engine, "branch-owner")
	chat := createTestShortChat(t, engine, owner)
	if err := engine.SetShortChatPinned(owner, chat.ID, true); err != nil {
		t.Fatal(err)
	}

	doc := []byte("private document")
	attachment := domain.Attachment{ID: "document", AgentID: chat.ID, Name: "note.txt", Size: int64(len(doc)), Path: "/old/stage", MachineID: "old-worker"}
	if err := engine.Store.SaveAttachment(attachment, doc); err != nil {
		t.Fatal(err)
	}
	unused := attachment
	unused.ID = "after-branch-point"
	if err := engine.Store.SaveAttachment(unused, doc); err != nil {
		t.Fatal(err)
	}
	seedBranchMessages(t, engine, chat, []domain.Message{
		{ID: "initial", Role: "user", Content: "Background"},
		{ID: "tool-result", Role: "tool", Content: "Recorded tool output"},
		{ID: "target", Role: "user", Content: "Read the document", Attachments: []domain.Attachment{attachment}},
		{ID: "after", Role: "user", Content: "Something else", Attachments: []domain.Attachment{unused}},
	})
	if err := engine.Store.Save("tool_activity", "tool-result", chat.ID, map[string]any{
		"id": "tool-result", "agentId": chat.ID,
		"activities": []domain.ToolActivity{{Name: "read_file", Content: "Recorded evidence"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Save("file_change", "tool-result", chat.ID, domain.FileChange{
		ID: "tool-result", AgentID: chat.ID, MessageID: "tool-result", Path: "sample.txt", Patch: "+example",
	}); err != nil {
		t.Fatal(err)
	}

	branch, err := engine.BranchConversation(t.Context(), owner, chat.ID, "target", nil)
	if err != nil {
		t.Fatal(err)
	}
	if branch.ChatOwnerID != owner || branch.ChatPinned || branch.ProjectID != "" || engine.AgentWorkerActive(branch.ID) {
		t.Fatalf("fork unexpectedly changed scope, pinned itself or started work: %+v", branch)
	}
	history := readBranchMessages(t, engine, branch.ID)
	if len(history) != 3 || history[2].Lineage() != "target" || len(history[2].Attachments) != 1 {
		t.Fatalf("fork did not preserve exactly the selected prefix: %+v", history)
	}
	if _, _, err := engine.Store.Attachment(branch.ID, unused.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("attachment outside selected prefix leaked: %v", err)
	}

	nested, err := engine.BranchConversation(t.Context(), owner, branch.ID, history[2].ID, nil)
	if err != nil || nested.BranchParentID != branch.ID || nested.BranchRootID != chat.ID || nested.BranchMessageLineageID != "target" {
		t.Fatalf("nested fork did not retain lineage: %+v, %v", nested, err)
	}
	if err := engine.StopAndDeleteAgent(t.Context(), chat.ID); err != nil {
		t.Fatal(err)
	}
	changes, err := engine.Store.MessageFileChanges([]string{history[1].ID}, nil)
	if err != nil || len(changes) != 1 || changes[0]["agentId"] != branch.ID || changes[0]["patch"] != "+example" {
		t.Fatalf("historical diff was not preserved: %+v, %v", changes, err)
	}
	var audit struct {
		Activities []domain.ToolActivity `json:"activities"`
	}
	if err := engine.Store.Load("tool_activity", history[1].ID, &audit); err != nil || len(audit.Activities) != 1 || audit.Activities[0].Content != "Recorded evidence" {
		t.Fatalf("historical tool display was not preserved: %+v, %v", audit, err)
	}
	for _, fork := range []domain.Agent{branch, nested} {
		meta, blob, err := engine.Store.Attachment(fork.ID, attachment.ID)
		if err != nil || string(blob) != string(doc) || meta.AgentID != fork.ID || meta.ProjectID != "" || meta.Path != "" || meta.MachineID != "" {
			t.Fatalf("fork attachment is not independently scoped: %+v, %q, %v", meta, blob, err)
		}
	}
	if siblings, err := engine.ConversationBranches(t.Context(), owner, nested.ID); err != nil || len(siblings) != 2 || siblings[0].ID != branch.ID || siblings[1].ID != nested.ID {
		t.Fatalf("remaining family lookup failed after deleting source: %+v, %v", siblings, err)
	}
}

func TestConversationBranchRejectsForeignAttachmentAtomically(t *testing.T) {
	engine, _ := testEngine(t, nil)
	owner := addTestChatUser(t, engine, "branch-private-owner")
	chat := createTestShortChat(t, engine, owner)
	other := createTestShortChat(t, engine, owner)
	attachment := domain.Attachment{ID: "other-chat-file", AgentID: other.ID, Name: "other.txt", Size: 1}
	if err := engine.Store.SaveAttachment(attachment, []byte("X")); err != nil {
		t.Fatal(err)
	}
	seedBranchMessages(t, engine, chat, []domain.Message{
		{ID: "bad-scope", Role: "user", Attachments: []domain.Attachment{attachment}},
	})

	text := "Do not read outside this chat"
	if _, err := engine.BranchConversation(t.Context(), owner, chat.ID, "bad-scope", &text); err == nil {
		t.Fatal("foreign attachment was accepted")
	}
	if branches, err := engine.ConversationBranches(t.Context(), owner, chat.ID); err != nil || len(branches) != 1 || branches[0].ID != chat.ID {
		t.Fatalf("failed operation left a branch behind: %+v, %v", branches, err)
	}
}

func TestConversationBranchEditShortChatAttachment(t *testing.T) {
	seen := make(chan providers.Request, 1)
	engine, _ := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		seen <- request
		return providers.Result{Text: "Read the note", UsageKnown: true}, nil
	}))
	owner := addTestChatUser(t, engine, "short-chat-edit-owner")
	chat := createTestShortChat(t, engine, owner)
	attachment := domain.Attachment{ID: "note", AgentID: chat.ID, Name: "note.txt", Size: 1}
	if err := engine.Store.SaveAttachment(attachment, []byte("A")); err != nil {
		t.Fatal(err)
	}
	seedBranchMessages(t, engine, chat, []domain.Message{
		{ID: "short-chat-edit", Role: "user", Content: "Summarize this", Attachments: []domain.Attachment{attachment}},
	})

	text := ""
	branch, err := engine.BranchConversation(t.Context(), owner, chat.ID, "short-chat-edit", &text)
	if err != nil {
		t.Fatal(err)
	}
	if record := waitForAgent(t, engine, branch.ID); record.Status != "idle" {
		t.Fatalf("edited short chat did not finish: %+v", record)
	}

	history := readBranchMessages(t, engine, branch.ID)
	if len(history) != 2 || history[0].Content != "" || history[0].Lineage() != "short-chat-edit" ||
		len(history[0].Attachments) != 1 || history[0].Attachments[0].AgentID != branch.ID {
		t.Fatalf("attachment-only edit was not delivered with scoped metadata: %+v", history)
	}
	select {
	case request := <-seen:
		for _, message := range request.Messages {
			if message.Role == "user" && len(message.Attachments) == 1 && message.Attachments[0].AgentID == branch.ID {
				return
			}
		}
		t.Fatal("model request did not include the scoped attachment")
	default:
		t.Fatal("attachment-only branch edit never started")
	}
}

func seedBranchMessages(t *testing.T, engine *Engine, record domain.Agent, messages []domain.Message) {
	t.Helper()
	for _, message := range messages {
		message.AgentID, message.CreatedAt = record.ID, time.Now().UTC()
		if err := engine.Store.Save("message", message.ID, record.ID, message); err != nil {
			t.Fatal(err)
		}
	}
}

func readBranchMessages(t *testing.T, engine *Engine, agentID string) []domain.Message {
	t.Helper()
	messages, err := engine.Store.MessageHistory(t.Context(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	return messages
}
