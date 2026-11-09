package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func goalVerdict(status, reason, evidence, next, blocker string) providers.Result {
	return providers.Result{
		Text: JSON(goalDecision{
			Status:   status,
			Reason:   reason,
			Evidence: evidence,
			NextStep: next,
			Blocker:  blocker,
		}),
		UsageKnown: true,
	}
}

func TestGoalReviewContextRetainsCallsWithoutProviderReplayData(t *testing.T) {
	engine, record := testEngine(t, nil)
	wire := `[{"type":"reasoning","encrypted_content":"OPAQUE_PROVIDER_REPLAY"},{"type":"function_call","call_id":"proof-call","name":"read_file","arguments":"{\"path\":\"proof.txt\"}"}]`
	if err := engine.appendMessage(record.ID, "assistant", "Inspecting evidence", wire, "", ""); err != nil {
		t.Fatal(err)
	}

	if err := engine.appendMessage(record.ID, "tool", `{"result":"ACTUAL_FILE_EVIDENCE"}`, "", "proof-call", "read_file"); err != nil {
		t.Fatal(err)
	}

	candidate := domain.Message{ID: "candidate", Content: "Ready for review", Wire: wire}
	value, err := engine.goalReviewContext(record, domain.Goal{Objective: "Verify proof.txt"}, candidate)
	if err != nil {
		t.Fatal(err)
	}

	context := JSON(value)
	if strings.Contains(context, "OPAQUE_PROVIDER_REPLAY") ||
		!strings.Contains(context, "proof.txt") ||
		!strings.Contains(context, "ACTUAL_FILE_EVIDENCE") {
		t.Fatal("review context lost evidence or duplicated provider replay", context)
	}
}

func TestGoalCompletionRequiresIndependentEvidenceAndContinuesWork(t *testing.T) {
	engine, record, _ := executionTestEngine(t, nil, shellPeerHandler("/bin/sh", os.Environ()))
	mainTurns, auditTurns := 0, 0
	engine.Provider = providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if request.System == goalReviewInstructions {
			auditTurns++

			switch auditTurns {
			case 1:
				return goalVerdict(
					"continue",
					"The required artifact and its validation are missing",
					"",
					"Create proof.txt with the verified result, then inspect it.",
					"",
				), nil
			case 2:
				return responseCalls(providers.Call{
					ID:   "inspect-proof",
					Name: "inspect",
					Arguments: map[string]any{
						"action": "read_file",
						"arguments": JSON(map[string]any{
							"path": "proof.txt",
						}),
					},
				}), nil
			default:
				if !strings.Contains(JSON(request.Messages), "VERIFIED_ARTIFACT") {
					t.Error("reviewer did not receive actual inspected file evidence")
				}

				return goalVerdict(
					"complete",
					"All required work was verified",
					"Read proof.txt from the current project; it contains VERIFIED_ARTIFACT",
					"",
					"",
				), nil
			}
		}

		if request.Config.ID == "approver-provider" {
			return providers.Result{
				Text:       `{"allowed":true,"reason":"","blanket":false}`,
				UsageKnown: true,
			}, nil
		}

		mainTurns++

		if mainTurns == 1 {
			return providers.Result{Text: "I am done.", UsageKnown: true}, nil
		}

		if mainTurns == 2 {
			if !strings.Contains(JSON(request.Messages), "required artifact") {
				t.Error("main agent did not receive audit feedback")
			}

			return responseCalls(providers.Call{
				ID:   "proof",
				Name: "apply_patch",
				Arguments: map[string]any{
					"input": "*** Begin Patch\n*** Add File: proof.txt\n+VERIFIED_ARTIFACT\n*** End Patch",
				},
			}), nil
		}

		return providers.Result{Text: "The artifact is now present and verified.", UsageKnown: true}, nil
	})

	if err := engine.Prompt(record.ID, "Create and verify the proof artifact."); err != nil {
		t.Fatal(err)
	}

	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" {
		t.Fatal(final)
	}

	goal, err := engine.CurrentGoal(record.ID)
	if err != nil ||
		goal.Status != "complete" ||
		goal.Continuations != 1 ||
		mainTurns != 3 ||
		auditTurns != 3 {
		t.Fatal(goal, mainTurns, auditTurns, err)
	}

	reviews, _ := engine.listAll("goal_review", &record.ID)
	if len(reviews) != 2 || reviews[0]["status"] != "continue" || reviews[1]["status"] != "complete" {
		t.Fatal(reviews)
	}

	inputs, _ := engine.Store.AgentInputs(record.ID)
	if len(inputs) != 0 {
		t.Fatal("verified completion left unfinished receipts", inputs)
	}
}

func TestGoalToolsCannotReplaceOrSelfCertifyUnfinishedGoal(t *testing.T) {
	engine, record := testEngine(t, nil)
	value, err := engine.dispatch(
		context.Background(),
		record,
		providers.Call{
			Name: "create_goal",
			Arguments: map[string]any{
				"objective": "Keep the full original objective",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	goal := value.(*domain.Goal)

	if _, err := engine.goalTool(record, "create_goal", map[string]any{
		"objective": "A smaller easier task",
	}); err == nil {
		t.Fatal("unfinished goal was replaced")
	}

	if _, err := engine.dispatch(
		context.Background(),
		record,
		providers.Call{
			Name: "update_goal",
			Arguments: map[string]any{
				"status": "complete",
				"reason": "I say it is done",
			},
		},
	); err != nil {
		t.Fatal(err)
	}

	current, err := engine.CurrentGoal(record.ID)
	if err != nil || current.Status != "active" || current.RequestedStatus != "complete" {
		t.Fatal("agent self-certified completion", current, err)
	}

	other, err := engine.CurrentGoal("another-agent")
	if err != nil || other != nil {
		t.Fatal("goal leaked into another conversation", other, err)
	}

	if _, err := engine.goalTool(record, "update_goal", map[string]any{"status": "active"}); err == nil {
		t.Fatal("agent resumed a goal by itself")
	}

	if err := engine.CancelGoal(record.ID, goal.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := engine.goalTool(record, "create_goal", map[string]any{
		"objective": "A user-approved replacement task",
	}); err != nil {
		t.Fatal("cancellation trapped the conversation", err)
	}
}

func TestGoalBlockedAuditRequiresThreeConsecutiveChecksAndResetsOnResume(t *testing.T) {
	engine, record := testEngine(t, nil)
	var audits atomic.Int32
	engine.Provider = providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if request.System == goalReviewInstructions {
			n := audits.Add(1)
			goal, _ := engine.CurrentGoal(record.ID)
			if n == 4 && (goal.BlockedAttempts != 0 || goal.Status != "active") {
				t.Error("resume did not reset the blocked audit", goal)
			}

			if n == 5 && (goal.BlockedAttempts != 1 || goal.Status != "active") {
				t.Error("resumed goal stopped before three checks", goal)
			}

			return goalVerdict(
				"blocked",
				"The required credential is absent",
				"The configured account has no credential and no authorized alternative exists",
				"",
				"missing-account-credential",
			), nil
		}

		return providers.Result{
			Text:       "The task cannot proceed without the credential.",
			UsageKnown: true,
		}, nil
	})

	if err := engine.Prompt(record.ID, "Finish work that requires the configured account."); err != nil {
		t.Fatal(err)
	}

	if final := waitForAgent(t, engine, record.ID); final.Status != "blocked" {
		t.Fatal(final)
	}

	goal, _ := engine.CurrentGoal(record.ID)
	if goal.Status != "blocked" || goal.BlockedAttempts != 3 || audits.Load() != 3 {
		t.Fatal(goal, audits.Load())
	}

	if err := engine.ResumeInputs(record.ID); err != nil {
		t.Fatal(err)
	}

	if final := waitForAgent(t, engine, record.ID); final.Status != "blocked" {
		t.Fatal(final)
	}

	goal, _ = engine.CurrentGoal(record.ID)
	if goal.BlockedAttempts != 3 || audits.Load() != 6 {
		t.Fatal(goal, audits.Load())
	}
}

func TestInvalidGoalAuditCannotCompleteOrBlockAUserTask(t *testing.T) {
	for _, response := range []string{
		`{"allowed":true,"reason":"","blanket":false}`,
		`{"status":"complete","reason":"Done","evidence":null}`,
		`{"status":"complete","reason":"Done","evidence":42}`,
		`{"status":"complete","reason":"Done","evidence":"  \n\t"}`,
		`{"status":"complete","reason":"   ","evidence":"A claim"}`,
		`{"status":"blocked","reason":"Hard","evidence":"It takes time"}`,
	} {
		t.Run(response, func(t *testing.T) {
			engine, record := testEngine(t, nil)
			engine.Provider = providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
				if request.System == goalReviewInstructions {
					return providers.Result{Text: response, UsageKnown: true}, nil
				}

				return providers.Result{Text: "Done", UsageKnown: true}, nil
			})

			if err := engine.Prompt(record.ID, "Actually finish the task"); err != nil {
				t.Fatal(err)
			}

			if final := waitForAgent(t, engine, record.ID); final.Status != "error" {
				t.Fatal(final)
			}

			goal, _ := engine.CurrentGoal(record.ID)
			if goal.Status != "active" || goal.BlockedAttempts != 0 {
				t.Fatal("invalid review terminated a goal", goal)
			}

			inputs, _ := engine.Store.AgentInputs(record.ID)
			if len(inputs) != 1 || inputs[0].Status != "blocked" {
				t.Fatal("unverified input was acknowledged as complete", inputs)
			}
		})
	}
}

func TestSteeringSupersedesAnInFlightGoalVerdict(t *testing.T) {
	engine, record := testEngine(t, nil)
	mainTurns, audits := 0, 0
	engine.Provider = providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if request.System == goalReviewInstructions {
			audits++

			if audits == 1 {
				if err := engine.Prompt(record.ID, "Additional requirement: preserve the second deliverable"); err != nil {
					t.Error(err)
				}
			} else if !strings.Contains(JSON(request.Messages), "second deliverable") {
				t.Error("fresh requirements missing from resumed audit")
			}

			return goalVerdict("complete", "Requirements verified", "Current verified artifacts satisfy the task", "", ""), nil
		}

		mainTurns++

		return providers.Result{Text: "Completed the current requirements.", UsageKnown: true}, nil
	})

	if err := engine.Prompt(record.ID, "Complete the original deliverable"); err != nil {
		t.Fatal(err)
	}

	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" {
		t.Fatal(final)
	}

	goal, _ := engine.CurrentGoal(record.ID)
	reviews, _ := engine.listAll("goal_review", &record.ID)
	if mainTurns != 2 ||
		audits != 2 ||
		goal.Status != "complete" ||
		len(goal.SourceInputIDs) != 2 ||
		len(reviews) != 2 ||
		reviews[0]["status"] != "superseded" {
		t.Fatal(mainTurns, audits, goal, reviews)
	}
}

func TestPauseDuringGoalReviewSurvivesRestartAndCanResume(t *testing.T) {
	engine, record := testEngine(t, nil)
	started := make(chan struct{})
	engine.Provider = providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		if request.System == goalReviewInstructions {
			close(started)
			<-ctx.Done()

			return providers.Result{}, ctx.Err()
		}

		return providers.Result{Text: "Proposed completion", UsageKnown: true}, nil
	})

	if err := engine.Prompt(record.ID, "Complete and independently verify this work"); err != nil {
		t.Fatal(err)
	}

	<-started

	if err := engine.Stop(record.ID); err != nil {
		t.Fatal(err)
	}

	if final := waitForAgent(t, engine, record.ID); final.Status != "stopped" {
		t.Fatal(final)
	}

	goal, _ := engine.CurrentGoal(record.ID)
	if goal.Status != "paused" {
		t.Fatal(goal)
	}

	engine.Close()
	var calls atomic.Int32
	restarted, err := New(engine.Store, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		calls.Add(1)

		if request.System == goalReviewInstructions {
			return goalVerdict("complete", "Done", "Verified current state", "", ""), nil
		}

		return providers.Result{Text: "Completed after resume", UsageKnown: true}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(restarted.Close)

	if calls.Load() != 0 {
		t.Fatal("paused goal resumed automatically")
	}

	if err := restarted.ResumeInputs(record.ID); err != nil {
		t.Fatal(err)
	}

	if final := waitForAgent(t, restarted, record.ID); final.Status != "idle" {
		t.Fatal(final)
	}

	resumed, _ := restarted.CurrentGoal(record.ID)
	if resumed.ID != goal.ID || resumed.Status != "complete" {
		t.Fatal(resumed)
	}
}

func TestProviderFailureIsNotAGoalBlocker(t *testing.T) {
	engine, record := testEngine(t, nil)
	engine.Provider = providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if request.System == goalReviewInstructions {
			return providers.Result{}, errors.New("provider temporarily unavailable")
		}

		return providers.Result{Text: "Candidate", UsageKnown: true}, nil
	})

	if err := engine.Prompt(record.ID, "Finish the task"); err != nil {
		t.Fatal(err)
	}

	if final := waitForAgent(t, engine, record.ID); final.Status != "error" {
		t.Fatal(final)
	}

	goal, _ := engine.CurrentGoal(record.ID)
	if goal.Status != "active" {
		t.Fatal("temporary provider issue became a permanent task blocker", goal)
	}
}

func TestActiveGoalRecoversWithoutAPendingInputReceipt(t *testing.T) {
	engine, record := testEngine(t, nil)
	value, err := engine.goalTool(
		record,
		"create_goal",
		map[string]any{
			"objective": "Finish the persistent objective after restart",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	goal := value.(*domain.Goal)
	engine.Close()
	restarted, err := New(engine.Store, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if !strings.Contains(JSON(request.Messages), goal.Objective) {
			t.Error("persistent objective missing from recovered context")
		}

		if request.System == goalReviewInstructions {
			return goalVerdict("complete", "Completed the recovered objective", "Verified the required persisted state", "", ""), nil
		}

		return providers.Result{Text: "Finished after restart", UsageKnown: true}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(restarted.Close)

	if final := waitForAgent(t, restarted, record.ID); final.Status != "idle" {
		t.Fatal(final)
	}

	current, err := restarted.CurrentGoal(record.ID)
	if err != nil || current.ID != goal.ID || current.Status != "complete" {
		t.Fatal("restart lost or failed to complete the goal", current, err)
	}

	if err := restarted.CancelGoal(record.ID, goal.ID); err == nil {
		t.Fatal("a verified completed goal was rewritten as cancelled")
	}

	restarted.Close()
	completed, err := New(engine.Store, providerFunction(func(_ context.Context, _ providers.Request) (providers.Result, error) {
		t.Error("completed goal restarted without a new user instruction")
		return providers.Result{}, errors.New("unexpected restart")
	}))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(completed.Close)
	completed.mu.Lock()
	running := completed.running[record.ID] != nil
	completed.mu.Unlock()

	if running {
		t.Fatal("completed goal has a recovery worker")
	}
}

func TestDiscardedGoalStaysStoppedAcrossNotificationsAndRestart(t *testing.T) {
	engine, record := testEngine(t, nil)
	value, err := engine.goalTool(record, "create_goal", map[string]any{
		"objective": "Discard this blocked objective",
	})
	if err != nil {
		t.Fatal(err)
	}

	goal := value.(*domain.Goal)
	record.Status, record.Error = "blocked", "Old blocker explanation"

	if err := engine.saveAgent(record); err != nil {
		t.Fatal(err)
	}

	if err := engine.Store.Save(
		"agent_input",
		"later-task",
		record.ID,
		domain.AgentInput{
			ID:        "later-task",
			AgentID:   record.ID,
			ProjectID: record.ProjectID,
			Text:      "Queued future work",
			Status:    "queued",
			Mode:      "queue",
		},
	); err != nil {
		t.Fatal(err)
	}

	if err := engine.CancelGoal(record.ID, goal.ID); err != nil {
		t.Fatal(err)
	}

	if err := engine.NotifyAgent(record.ID, "A background notification arrived"); err != nil {
		t.Fatal(err)
	}

	engine.Close()
	restarted, err := New(engine.Store, providerFunction(func(_ context.Context, _ providers.Request) (providers.Result, error) {
		t.Error("discarded goal or its queue restarted automatically")
		return providers.Result{}, errors.New("unexpected restart")
	}))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(restarted.Close)
	restarted.mu.Lock()
	running := restarted.running[record.ID] != nil
	restarted.mu.Unlock()

	if running {
		t.Fatal("discarded goal has a recovery worker")
	}

	if err := restarted.Store.Load("agent", record.ID, &record); err != nil ||
		record.Status != "stopped" ||
		record.Error != "" {
		t.Fatal("discard left an obsolete blocker visible", record, err)
	}

	inputs, _ := restarted.Store.AgentInputs(record.ID)
	if len(inputs) != 1 || inputs[0].Status != "queued" {
		t.Fatal("discard lost future queued work", inputs)
	}
}
