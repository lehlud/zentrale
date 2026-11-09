package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestDefaultFileReadIsBoundedForNamedToolsAndDispatcher(t *testing.T) {
	engine, record, project := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{Text: `{"allowed":true,"reason":"","blanket":false}`, UsageKnown: true}, nil
	}), shellPeerHandler("/bin/sh", os.Environ()))
	if err := os.MkdirAll(project.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("visible line\n", 200) + "suffix outside the default preview\n"
	if err := os.WriteFile(filepath.Join(project.Directory, "large.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := engine.file(context.Background(), record, "read_file", map[string]any{"path": "large.txt"}, false, false)
	if err != nil || raw["sha256"] != fmt.Sprintf("%x", sha256.Sum256([]byte(content))) {
		t.Fatal("default preview no longer hashes the entire file", err)
	}
	for _, call := range []providers.Call{
		{Name: "read_file", Arguments: map[string]any{"path": "large.txt"}},
		{Name: "harness", Arguments: map[string]any{"action": "read_file", "arguments": `{"path":"large.txt"}`}},
	} {
		result, err := engine.dispatch(context.Background(), record, call)
		encoded := JSON(result)
		if err != nil || strings.Contains(encoded, "suffix outside") || !strings.Contains(encoded, `"hasMore":true`) || !strings.Contains(encoded, `"totalLines":201`) {
			t.Fatalf("%s: default read was not bounded: %v", call.Name, err)
		}
	}
}

func TestNativePatchPlanAndPartialReadThroughSSH(t *testing.T) {
	turns := 0
	patch := "*** Begin Patch\n*** Update File: data.txt\n@@\n-second\n+updated\n*** Add File: proof.py\n+print('verified')\n*** End Patch\n"
	plan := func(status string) providers.Call {
		return providers.Call{ID: "plan-" + status, Name: "update_plan", Arguments: map[string]any{"plan": []any{map[string]any{"step": "Apply the requested changes", "status": status}, map[string]any{"step": "Run the verification", "status": "pending"}}}}
	}
	provider := providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if request.Config.ID == "approver-provider" {
			return providers.Result{Text: `{"allowed":true,"reason":"","blanket":false}`, UsageKnown: true}, nil
		}
		turns++
		for _, message := range request.Messages {
			if message.Role != "tool" {
				continue
			}
			var response map[string]any
			if err := json.Unmarshal([]byte(message.Content), &response); err != nil {
				t.Fatal(err)
			}
			if response["error"] != nil {
				t.Fatalf("native action failed: %v", response)
			}
			if strings.Contains(message.Content, "_changes") || strings.Contains(message.Content, "+++ b/") {
				t.Fatal("UI diffs leaked into model context")
			}
		}
		switch turns {
		case 1:
			return providers.Result{Text: "I will inspect the relevant line and apply the change.", Phase: "commentary", Wire: `[{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Inspecting the relevant line."}]}]`, UsageKnown: true}, nil
		case 2:
			return responseCalls(providers.Call{ID: "read", Name: "read_file", Arguments: map[string]any{"path": "data.txt", "offset": float64(2), "limit": float64(1)}}, plan("in_progress")), nil
		case 3:
			found := false
			for _, message := range request.Messages {
				if message.CallID == "read" {
					found = strings.Contains(message.Content, `"content":"second\n"`) && !strings.Contains(message.Content, "unchanged padding")
				}
			}
			if !found || !strings.Contains(JSON(request.Messages), "Apply the requested changes") {
				t.Fatal("partial read or persistent plan unavailable")
			}
			return responseCalls(providers.Call{ID: "edit", Name: "apply_patch", Arguments: map[string]any{"input": patch}}), nil
		case 4:
			return responseCalls(providers.Call{ID: "verify", Name: "exec_command", Arguments: map[string]any{"cmd": "python3 proof.py", "yield_time_ms": float64(1000)}}), nil
		case 5:
			return responseCalls(providers.Call{ID: "plan-done", Name: "update_plan", Arguments: map[string]any{"plan": []any{map[string]any{"step": "Apply the requested changes", "status": "completed"}, map[string]any{"step": "Run the verification", "status": "completed"}}}}), nil
		default:
			if !strings.Contains(JSON(request.Messages), "verified") {
				t.Fatal("verification output missing")
			}
			return providers.Result{Text: "Applied and verified.", Phase: "final_answer", UsageKnown: true}, nil
		}
	})
	engine, record, project := executionTestEngine(t, provider, shellPeerHandler("/bin/sh", os.Environ()))
	if err := os.MkdirAll(project.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	original := "first\nsecond\nthird\n" + strings.Repeat("unchanged padding for a larger file\n", 2000)
	filename := filepath.Join(project.Directory, "data.txt")
	if err := os.WriteFile(filename, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := engine.Prompt(record.ID, "Change second to updated in data.txt, create proof.py and execute it to verify the change."); err != nil {
		t.Fatal(err)
	}
	final := waitForAgent(t, engine, record.ID)
	if final.Status != "idle" || turns != 6 {
		t.Fatalf("progress ended the task or native action failed: %+v, turns=%d", final, turns)
	}
	content, _ := os.ReadFile(filename)
	if string(content) != strings.Replace(original, "second", "updated", 1) {
		t.Fatal("focused patch changed unrelated content")
	}
	changes, err := engine.listAll("file_change", &record.ID)
	if err != nil || len(changes) != 2 || changes[0]["messageId"] != changes[1]["messageId"] {
		t.Fatal("multiple changes were not linked to one tool result", changes, err)
	}
	for _, change := range changes {
		if stringArg(change, "patch") == "" {
			t.Fatal("actual diff missing", change)
		}
	}
	approvals, err := engine.listAll("approval", &record.ID)
	if err != nil || len(approvals) != 3 {
		t.Fatal("native read, patch and command did not each pass approval", approvals, err)
	}
	if len(patch)*100 >= len(original) {
		t.Fatal("fixture patch did not avoid resending unchanged contents")
	}
	t.Logf("single-line edit request: full file %d bytes, patch %d bytes", len(original), len(patch))
}

func TestNativeToolsStillDenyProjectMutationAndKeepPlanPrivate(t *testing.T) {
	engine, record, project := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{Text: `{"allowed":false,"reason":"Not authorized","blanket":false}`, UsageKnown: true}, nil
	}), shellPeerHandler("/bin/sh", os.Environ()))
	_, err := engine.dispatch(context.Background(), record, providers.Call{Name: "apply_patch", Arguments: map[string]any{"input": "*** Begin Patch\n*** Add File: denied.txt\n+must not exist\n*** End Patch"}})
	if err == nil {
		t.Fatal("denied native patch executed")
	}
	if _, err := os.Stat(filepath.Join(project.Directory, "denied.txt")); !os.IsNotExist(err) {
		t.Fatal("denied patch touched a file")
	}
	valid := map[string]any{"plan": []any{map[string]any{"step": "Own plan", "status": "in_progress"}}}
	if _, err := engine.dispatch(context.Background(), record, providers.Call{Name: "update_plan", Arguments: valid}); err != nil {
		t.Fatal("own plan unexpectedly required approval", err)
	}
	if _, err := engine.updatePlan(record, map[string]any{"plan": []any{map[string]any{"step": "One", "status": "in_progress"}, map[string]any{"step": "Two", "status": "in_progress"}}}); err == nil {
		t.Fatal("ambiguous active plan was accepted")
	}
	var saved taskPlan
	if err := engine.Store.Load("plan", record.ID, &saved); err != nil || saved.Plan[0].Step != "Own plan" {
		t.Fatal("invalid plan overwrote the saved one", saved, err)
	}
	if _, err := engine.executeInspection(context.Background(), record, domain.Agent{ID: record.ApproverID}, providers.Call{Name: "update_plan", Arguments: valid}); err == nil {
		t.Fatal("approver gained a mutating native tool")
	}
}

func TestForegroundYieldKeepsJobAlive(t *testing.T) {
	running := &runningJob{done: make(chan struct{}), job: domain.Job{ID: "job", Status: "running"}, cancel: func() { t.Error("yield cancelled the job") }}
	result, err := waitForForegroundFor(context.Background(), running, "microvm", time.Millisecond)
	if err != nil || result.(map[string]any)["jobId"] != "job" {
		t.Fatal(result, err)
	}
	running.job.Status = "completed"
	close(running.done)
	result, err = waitForForegroundFor(context.Background(), running, "microvm", time.Millisecond)
	if err != nil || result.(map[string]any)["job"].(domain.Job).Status != "completed" {
		t.Fatal(result, err)
	}
}

func TestRepeatedProgressCannotRunAnUnboundedPaidLoop(t *testing.T) {
	calls := 0
	audits := 0
	engine, record := testEngine(t, nil)
	engine.Provider = providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if request.System == goalReviewInstructions {
			audits++
			return goalVerdict("blocked", "This provider cannot produce the required tool actions", "Every requested action turn yielded only planning; fixing or changing the provider requires external intervention", "", "provider-cannot-emit-actions"), nil
		}
		calls++
		return providers.Result{Text: "Still planning.", Phase: "commentary", UsageKnown: true}, nil
	})
	if err := engine.Prompt(record.ID, "Finish the task."); err != nil {
		t.Fatal(err)
	}
	final := waitForAgent(t, engine, record.ID)
	if final.Status != "blocked" || calls != 9 || audits != 3 {
		t.Fatal("progress loop was reported complete or remained unbounded", final, calls)
	}
}
