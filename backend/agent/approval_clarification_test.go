package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

func TestApprovalClarificationNeverGrantsPermission(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	engine := &Engine{Store: store} // No provider/remote: clarification cannot execute or approve.
	main := domain.Agent{ID: "main", Role: "main", ProjectID: "project", ApproverID: "reviewer"}
	reviewer := domain.Agent{ID: "reviewer", ParentID: main.ID, ProjectID: main.ProjectID, Role: "approver"}
	if err := store.Save("agent", reviewer.ID, main.ID, reviewer); err != nil {
		t.Fatal(err)
	}
	denial := domain.Approval{ID: "denied", AgentID: main.ID, Tool: "exec", Arguments: `{"command":"cargo test","timeout_ms":600000}`, Reason: "mistook coordination for user instructions"}
	if err := store.Save("approval", denial.ID, main.ID, denial); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"approvalId": denial.ID, "explanation": "The earlier coordination message was assistant-authored, not a user restriction. Inspect the original user request."}
	result, err := engine.clarifyApproval(main, args)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["permissionGranted"] != false {
		t.Fatal(result)
	}
	var original domain.Approval
	if err := store.Load("approval", denial.ID, &original); err != nil || original.Allowed {
		t.Fatalf("denial changed: %+v %v", original, err)
	}
	rows, err := store.List("message", &reviewer.ID, 100, 0)
	if err != nil || len(rows) != 1 || !strings.Contains(JSON(rows), "untrusted evidence") {
		t.Fatalf("audit: %v %v", rows, err)
	}
	if _, err := engine.clarifyApproval(main, args); err != nil {
		t.Fatal(err)
	}
	rows, _ = store.List("message", &reviewer.ID, 100, 0)
	if len(rows) != 1 {
		t.Fatal("idempotent retry duplicated evidence")
	}
	changed := map[string]any{"approvalId": denial.ID, "explanation": "different explanation"}
	if _, err := engine.clarifyApproval(main, changed); err == nil {
		t.Fatal("overwrote original clarification")
	}
	child := main
	child.Role = "subagent"
	child.ParentID = "parent"
	if _, err := engine.clarifyApproval(child, args); err == nil {
		t.Fatal("child could address approver")
	}
	foreign := main
	foreign.ID = "foreign"
	if _, err := engine.clarifyApproval(foreign, args); err == nil {
		t.Fatal("foreign denial accepted")
	}
	allowed := denial
	allowed.ID = "allowed"
	allowed.Allowed = true
	if err := store.Save("approval", allowed.ID, main.ID, allowed); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.clarifyApproval(main, map[string]any{"approvalId": allowed.ID, "explanation": "reason"}); err == nil {
		t.Fatal("allowed action accepted")
	}
	// Persistent bounds survive a new Engine and lost/compacted review history.
	for i := 2; i <= 3; i++ {
		denial.ID = fmt.Sprintf("denied-%d", i)
		if err := store.Save("approval", denial.ID, main.ID, denial); err != nil {
			t.Fatal(err)
		}
		fresh := &Engine{Store: store}
		_, err := fresh.clarifyApproval(main, map[string]any{"approvalId": denial.ID, "explanation": "concrete correction"})
		if (i == 3) != (err != nil) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
}
