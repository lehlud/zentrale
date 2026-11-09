package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

type providerFunction func(context.Context, providers.Request) (providers.Result, error)

func TestFailedSSHPreparationKeepsAcceptedPromptForRetry(t *testing.T) {
	var calls atomic.Int32
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		calls.Add(1)
		return providers.Result{Text: "Completed", UsageKnown: true}, nil
	}))

	var project domain.Project
	if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
		t.Fatal(err)
	}

	server, err := testssh.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	server.RejectAuthentication(true)
	public, err := remote.DecodePublicKey(server.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}

	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}

	machine.Host, machine.Port = server.Host, server.Port

	if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}

	if err := engine.Prompt(record.ID, "Create a Python quicksort example"); err != nil {
		t.Fatal(err)
	}

	if final := waitForAgent(t, engine, record.ID); final.Status != "error" {
		t.Fatal(final)
	}

	if calls.Load() != 0 {
		t.Fatal("a failed preflight reached the model")
	}

	messages, err := engine.loadHistory(record.ID)
	if err != nil || len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("failed start lost the accepted prompt: %v %v", messages, err)
	}

	server.RejectAuthentication(false)

	if err := engine.ResumeInputs(record.ID); err != nil {
		t.Fatal(err)
	}

	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" {
		t.Fatal(final.Error)
	}

	messages, err = engine.loadHistory(record.ID)
	if err != nil || len(messages) != 2 || calls.Load() != 1 {
		t.Fatalf("retry duplicated the prompt or response: %v %v, requests=%d", messages, err, calls.Load())
	}
}

func (complete providerFunction) Complete(ctx context.Context, request providers.Request) (providers.Result, error) {
	return complete(ctx, request)
}

// Legacy fixtures exercise task/approval behavior; their default completion
// auditor accepts the scripted outcome. Goal tests install their own real
// reviewer behavior explicitly and never use this shortcut.
func withTestGoalReviewer(provider providers.Client) providers.Client {
	wrapped := providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		if request.System == goalReviewInstructions {
			return providers.Result{
				Text:       `{"status":"complete","reason":"Fixture task finished","evidence":"Scripted fixture result"}`,
				UsageKnown: true,
			}, nil
		}

		return provider.Complete(ctx, request)
	})

	if native, ok := provider.(providers.NativeCompactor); ok {
		return testGoalCompactor{Client: wrapped, NativeCompactor: native}
	}

	return wrapped
}

type testGoalCompactor struct {
	providers.Client
	providers.NativeCompactor
}

func testEngine(t *testing.T, provider providers.Client) (*Engine, domain.Agent) {
	t.Helper()
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	for _, id := range []string{"main-provider", "approver-provider"} {
		configuration := domain.Provider{
			ID:   id,
			Kind: "openai",
			Name: id,
		}
		if err := store.Save("provider", id, "", configuration); err != nil {
			t.Fatal(err)
		}

		model := domain.LanguageModel{
			ID:            "test",
			ProviderID:    id,
			Name:          "Test",
			ContextWindow: 128000,
			PricesKnown:   true,
			InputPrice:    1,
			OutputPrice:   2,
			CachedPrice:   0.1,
		}
		if err := store.Save("model", modelResourceID(id, model.ID), id, model); err != nil {
			t.Fatal(err)
		}
	}

	defaults := domain.ModelDefaults{
		Main:     domain.ModelChoice{ProviderID: "main-provider", ModelID: "test"},
		Approver: domain.ModelChoice{ProviderID: "approver-provider", ModelID: "test"},
	}
	if err := store.SetSetting("model_defaults", JSON(defaults)); err != nil {
		t.Fatal(err)
	}

	sshServer, err := testssh.Start()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(sshServer.Close)
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}

	public, err := remote.DecodePublicKey(sshServer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}

	machine := domain.Machine{
		ID:      "test-worker",
		Name:    "Test worker",
		Host:    sshServer.Host,
		Port:    sshServer.Port,
		User:    "developer",
		KeyID:   key.ID,
		Sandbox: "none",
	}
	if err := store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}

	project := domain.Project{
		ID:        "project",
		Name:      "Project",
		Directory: "/srv/projects/test",
		MachineIDs: []string{
			machine.ID,
		},
	}
	if err := store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}

	engine, err := New(store, withTestGoalReviewer(provider))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(engine.Close)
	mainAgent, err := engine.Create("project", "Main", "main-provider", "approver-provider", "")
	if err != nil {
		t.Fatal(err)
	}

	return engine, mainAgent
}

func waitForAgent(t *testing.T, engine *Engine, agentID string) domain.Agent {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	updates, unsubscribe := engine.Subscribe()
	defer unsubscribe()

	for {
		var record domain.Agent
		if err := engine.Store.Load("agent", agentID, &record); err != nil {
			t.Fatal(err)
		}

		if record.Status != "running" {
			return record
		}

		select {
		case <-updates:
		case <-timeout.C:
			t.Fatal("agent did not finish")
		}
	}
}

func TestDeniedInteractionNeverReachesRemoteExecution(t *testing.T) {
	var mutex sync.Mutex
	mainTurns, approvalTurns := 0, 0
	provider := providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		mutex.Lock()
		defer mutex.Unlock()

		if request.Config.ID == "approver-provider" {
			approvalTurns++

			if !strings.Contains(JSON(request.Messages), "Delete everything") {
				t.Error("approver cannot see user instructions")
			}

			return providers.Result{
				Text:       `{"allowed":false,"reason":"Outside the requested scope","blanket":false}`,
				UsageKnown: true,
			}, nil
		}

		mainTurns++

		if mainTurns == 1 {
			return providers.Result{
				Text: "Checking a command",
				Calls: []providers.Call{
					{
						ID:        "call-one",
						Name:      "harness",
						Arguments: map[string]any{"action": "exec", "arguments": `{"command":"rm -rf /"}`},
					},
				},
				UsageKnown: true,
			}, nil
		}

		if !strings.Contains(JSON(request.Messages), "auto-approval denied") {
			t.Error("main agent did not receive denial")
		}

		return providers.Result{Text: "I stopped the unsafe operation.", UsageKnown: true}, nil
	})

	engine, mainAgent := testEngine(t, provider)
	if err := engine.Prompt(mainAgent.ID, "Delete everything in an unrelated folder"); err != nil {
		t.Fatal(err)
	}

	final := waitForAgent(t, engine, mainAgent.ID)
	if final.Status != "idle" {
		t.Fatalf("denial should be recoverable: %+v", final)
	}

	if mainTurns != 2 || approvalTurns != 1 {
		t.Fatalf("turn counts: main=%d approver=%d", mainTurns, approvalTurns)
	}

	approvals, err := engine.listAll("approval", &mainAgent.ID)
	if err != nil || len(approvals) != 1 || approvals[0]["allowed"] != false {
		t.Fatalf("approval audit: %v %v", approvals, err)
	}

	mainHistory, _ := engine.loadHistory(mainAgent.ID)
	approverHistory, _ := engine.loadHistory(mainAgent.ApproverID)

	if strings.Contains(JSON(mainHistory), "Outside the requested scope\",\"blanket") {
		t.Fatal("approver context leaked into main history")
	}

	if len(approverHistory) != 4 {
		t.Fatalf("independent approver history length: %d", len(approverHistory))
	}
}

func TestPromptDuringFinalInferenceIsNotLost(t *testing.T) {
	started := make(chan struct{})
	continueFirst := make(chan struct{})
	calls := 0
	provider := providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		calls++

		if calls == 1 {
			close(started)

			select {
			case <-continueFirst:
			case <-ctx.Done():
				return providers.Result{}, ctx.Err()
			}
		}

		return providers.Result{Text: "Completed", UsageKnown: true}, nil
	})

	engine, mainAgent := testEngine(t, provider)
	if err := engine.Prompt(mainAgent.ID, "First instruction"); err != nil {
		t.Fatal(err)
	}

	<-started

	if err := engine.Prompt(mainAgent.ID, "Second instruction"); err != nil {
		t.Fatal(err)
	}

	close(continueFirst)

	if final := waitForAgent(t, engine, mainAgent.ID); final.Status != "idle" {
		t.Fatal(final.Error)
	}

	if calls != 2 {
		t.Fatalf("new instruction was lost; inference calls=%d", calls)
	}
}

func TestNotesSurviveContextChangesAndLimitsUseCharacters(t *testing.T) {
	engine, mainAgent := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{Text: "Summary", UsageKnown: true}, nil
	}))

	for index := range 10 {
		_, err := engine.note(
			mainAgent,
			"note_save",
			map[string]any{
				"title":   fmt.Sprintf("Notiz %d", index),
				"content": strings.Repeat("ä", 350),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	if _, err := engine.note(mainAgent, "note_save", map[string]any{
		"title":   "Eleven",
		"content": "text",
	}); err == nil {
		t.Fatal("eleventh note accepted")
	}

	if err := engine.appendMessage(mainAgent.ID, "user", "Remember the task", "", "", ""); err != nil {
		t.Fatal(err)
	}

	for _, action := range []string{"context_compact", "context_clear", "context_rotate"} {
		if _, err := engine.manageContext(context.Background(), mainAgent, action, map[string]any{
			"start": float64(0),
		}); err != nil {
			t.Fatal(err)
		}

		notes, err := engine.note(mainAgent, "note_list", nil)
		if err != nil || len(notes.([]map[string]any)) != 10 {
			t.Fatalf("notes lost after %s: %v", action, err)
		}
	}
}

func TestSubagentsCannotSpawnAndQuestionsHaveOwnership(t *testing.T) {
	engine, mainAgent := testEngine(t, nil)
	child, err := engine.Create(mainAgent.ProjectID, "Child", mainAgent.ProviderID, "approver-provider", mainAgent.ID)
	if err != nil {
		t.Fatal(err)
	}

	if child.ApproverID == mainAgent.ApproverID {
		t.Fatal("subagent reused its parent's approver")
	}

	if _, err := engine.Create(mainAgent.ProjectID, "Grandchild", mainAgent.ProviderID, "approver-provider", child.ID); err == nil {
		t.Fatal("subagent created another subagent")
	}

	result, err := engine.question(
		mainAgent,
		"question_ask",
		map[string]any{
			"title": "Which target?",
			"options": []any{
				"One",
				"Two",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	question := result.(domain.Question)
	if err := engine.AnswerQuestion(question.ID, "", "withdrawn", child.ID); err == nil {
		t.Fatal("another agent withdrew the question")
	}

	if err := engine.AnswerQuestion(question.ID, "", "withdrawn", mainAgent.ID); err != nil {
		t.Fatal(err)
	}

	if err := engine.AnswerQuestion(question.ID, "", "withdrawn", mainAgent.ID); err == nil {
		t.Fatal("closed question reopened")
	}
}

func TestJobWaitIsInterruptedByUserInput(t *testing.T) {
	engine, mainAgent := testEngine(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	running := &runningJob{
		cancel: cancel,
		done:   make(chan struct{}),
		job:    domain.Job{ID: "job", AgentID: mainAgent.ID, Status: "running"},
	}
	if err := engine.Store.Save("job", "job", mainAgent.ID, running.job); err != nil {
		t.Fatal(err)
	}

	engine.mu.Lock()
	engine.jobs["job"] = running
	engine.wake[mainAgent.ID] = make(chan struct{}, 1)
	engine.wake[mainAgent.ID] <- struct{}{}
	engine.mu.Unlock()
	result, err := engine.jobTool(ctx, mainAgent, "job_wait", "job")
	if err != nil {
		t.Fatal(err)
	}

	if result.(map[string]string)["status"] != "interrupted" {
		t.Fatal("new instruction did not interrupt wait")
	}

	if _, err := engine.jobTool(ctx, domain.Agent{ID: "other-agent"}, "job_stop", "job"); err == nil {
		t.Fatal("another agent stopped the job")
	}
}

func TestInvalidApprovalResponseFailsClosed(t *testing.T) {
	engine, mainAgent := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{Text: "looks fine", UsageKnown: true}, nil
	}))
	if _, err := engine.approve(
		context.Background(),
		mainAgent,
		interaction{
			Action: "exec",
			Arguments: map[string]any{
				"command": "true",
			},
		},
	); err == nil {
		t.Fatal("invalid approval silently allowed an action")
	}

	if _, err := engine.Inspect(context.Background(), mainAgent, "exec", map[string]any{
		"command": "true",
	}); err == nil {
		t.Fatal("approver gained command execution")
	}
}

func TestCancelledInferenceCanBeStopped(t *testing.T) {
	started := make(chan struct{})
	engine, mainAgent := testEngine(t, providerFunction(func(ctx context.Context, _ providers.Request) (providers.Result, error) {
		close(started)
		<-ctx.Done()

		return providers.Result{}, ctx.Err()
	}))
	if err := engine.Prompt(mainAgent.ID, "Wait"); err != nil {
		t.Fatal(err)
	}

	<-started

	if err := engine.Stop(mainAgent.ID); err != nil {
		t.Fatal(err)
	}

	final := waitForAgent(t, engine, mainAgent.ID)
	if final.Status != "stopped" || final.Error != "" {
		t.Fatalf("cancelled agent state: %+v", final)
	}
}

func TestPartialMalformedApprovalCannotAuthorizeAnAction(t *testing.T) {
	decision := parseApprovalDecision(`{"allowed":true,"reason":42}`)
	if decision.Allowed || decision.Reason == "" {
		t.Fatalf("partial JSON result authorized an action: %+v", decision)
	}
}

func TestHarnessNotificationWakesAgentWithoutBecomingUserInstruction(t *testing.T) {
	received := make(chan string, 1)
	engine, mainAgent := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		received <- request.Messages[len(request.Messages)-1].Content
		return providers.Result{
			Text:       "I will inspect the checkpoints before restarting.",
			UsageKnown: true,
		}, nil
	}))

	notice := "Harness event: ML training terminated on worker-one. Resume from checkpoints yourself."
	if err := engine.NotifyAgent(mainAgent.ID, notice); err != nil {
		t.Fatal(err)
	}

	final := waitForAgent(t, engine, mainAgent.ID)
	if final.Status != "idle" {
		t.Fatal(final.Error)
	}

	if message := <-received; message != notice {
		t.Fatalf("termination notice was not delivered: %q", message)
	}

	instructions, err := engine.listAll("instruction", &mainAgent.ProjectID)
	if err != nil || len(instructions) != 0 {
		t.Fatal("harness event was recorded as a user instruction")
	}
}

func TestChangedInstructionsInvalidateInFlightApproval(t *testing.T) {
	started := make(chan struct{})
	continueReview := make(chan struct{})
	engine, mainAgent := testEngine(t, providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		close(started)

		select {
		case <-continueReview:
		case <-ctx.Done():
			return providers.Result{}, ctx.Err()
		}

		return providers.Result{Text: `{"allowed":true,"blanket":true}`, UsageKnown: true}, nil
	}))

	outcome := make(chan error, 1)
	go func() {
		_, err := engine.approve(
			context.Background(),
			mainAgent,
			interaction{
				Action: "exec",
				Arguments: map[string]any{
					"command": "true",
				},
			},
		)

		outcome <- err
	}()
	<-started
	engine.invalidateProjectGrants(mainAgent.ProjectID)
	close(continueReview)

	if err := <-outcome; err == nil || !strings.Contains(err.Error(), "instructions changed") {
		t.Fatalf("stale approval accepted: %v", err)
	}
}

func TestRemovingAgentIncludesChildrenApproversAndHistory(t *testing.T) {
	engine, mainAgent := testEngine(t, nil)
	child, err := engine.Create(mainAgent.ProjectID, "Child", mainAgent.ProviderID, "approver-provider", mainAgent.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.appendMessage(child.ID, "user", "Child history", "", "", ""); err != nil {
		t.Fatal(err)
	}

	if err := engine.Delete("agent", mainAgent.ApproverID); err == nil {
		t.Fatal("approver could be removed independently")
	}

	if err := engine.Delete("agent", mainAgent.ID); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{mainAgent.ID, mainAgent.ApproverID, child.ID, child.ApproverID} {
		var record domain.Agent
		if err := engine.Store.Load("agent", id, &record); err == nil {
			t.Fatal("orphaned agent", id)
		}

		history, err := engine.loadHistory(id)
		if err != nil || len(history) != 0 {
			t.Fatal("orphaned history", id, err)
		}
	}
}

func TestExplicitStopIsNotUndoneByJobNotification(t *testing.T) {
	calls := 0
	engine, mainAgent := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		calls++
		return providers.Result{Text: "Done", UsageKnown: true}, nil
	}))
	if err := engine.Stop(mainAgent.ID); err != nil {
		t.Fatal(err)
	}

	if err := engine.NotifyAgent(mainAgent.ID, "Job terminated"); err != nil {
		t.Fatal(err)
	}

	if calls != 0 {
		t.Fatal("job event restarted an explicitly stopped agent")
	}

	history, err := engine.loadHistory(mainAgent.ID)
	if err != nil || len(history) != 1 || history[0].Role != "event" {
		t.Fatal("termination event was lost", err)
	}

	if err := engine.Prompt(mainAgent.ID, "Resume"); err != nil {
		t.Fatal(err)
	}

	waitForAgent(t, engine, mainAgent.ID)

	if calls != 1 {
		t.Fatal("new user instruction did not resume the agent")
	}
}

func TestCompactionSplitsOversizedToolResultsWithoutLosingHistory(t *testing.T) {
	requests := 0
	engine, mainAgent := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		requests++

		if estimatedPromptTokens(request.System, request.Tools)+estimatedMessageTokens(request.Messages) > request.Model.ContextWindow {
			t.Error("compaction exceeded the model's input window")
		}

		return providers.Result{Text: "Resume training from checkpoint 42.", UsageKnown: true}, nil
	}))

	model := domain.LanguageModel{
		ID:            mainAgent.ModelID,
		ProviderID:    mainAgent.ProviderID,
		ContextWindow: 4096,
	}
	if err := engine.Store.Save("model", modelResourceID(model.ProviderID, model.ID), model.ProviderID, model); err != nil {
		t.Fatal(err)
	}

	if err := engine.appendMessage(mainAgent.ID, "tool", strings.Repeat("ä checkpoint 42; ", 2000), "", "call", "harness"); err != nil {
		t.Fatal(err)
	}

	configuration, err := engine.inferenceConfiguration(context.Background(), mainAgent)
	if err != nil {
		t.Fatal(err)
	}

	prepared, messages, err := engine.prepareContext(context.Background(), mainAgent, configuration, false)
	if err != nil {
		t.Fatal(err)
	}

	if requests != 0 ||
		prepared.ContextStart != 0 ||
		len(messages) != 2 ||
		len(messages[1].Content) > configuration.Model.ContextWindow {
		t.Fatal("a large recoverable tool output triggered unnecessary compaction")
	}

	history, err := engine.loadHistory(mainAgent.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The cumulative summarizer also accepts a full transcript independently of
	// the bounded inference projection.
	compacted, err := engine.compactContext(context.Background(), mainAgent, configuration, history, len(history))
	if err != nil {
		t.Fatal(err)
	}

	if requests < 2 || !strings.Contains(compacted.Summary, "checkpoint 42") {
		t.Fatalf("incremental compaction: %+v, requests=%d", compacted, requests)
	}

	history, err = engine.loadHistory(mainAgent.ID)
	if err != nil || len(history) != 1 || !strings.Contains(history[0].Content, "ä checkpoint") {
		t.Fatal("original history lost", err)
	}
}
