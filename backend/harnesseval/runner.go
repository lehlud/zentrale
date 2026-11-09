package harnesseval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"runtime/debug"
	"strings"
	"time"

	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

type Plan struct {
	Label     string               `json:"label"`
	MachineID string               `json:"machineId"`
	Models    []domain.ModelChoice `json:"models"`
	Approver  domain.ModelChoice   `json:"approver"`
	Cases     []string             `json:"cases"`

	Repetitions       int `json:"repetitions"`
	RunTimeoutSeconds int `json:"runTimeoutSeconds"`
	MaxModelRequests  int `json:"maxModelRequests"`
}

type RunResult struct {
	MachineID       string `json:"machineId"`
	AgentID         string `json:"agentId,omitempty"`
	ProjectID       string `json:"projectId,omitempty"`
	RemoteDirectory string `json:"remoteDirectory,omitempty"`

	Model         domain.ModelChoice `json:"model"`
	ProviderKind  string             `json:"providerKind"`
	Case          string             `json:"case"`
	FixtureSHA256 string             `json:"fixtureSha256"`
	Repetition    int                `json:"repetition"`

	Passed       bool   `json:"passed"`
	Error        string `json:"error,omitempty"`
	CleanupError string `json:"cleanupError,omitempty"`

	DurationMillis     int64 `json:"durationMillis"`
	SetupMillis        int64 `json:"setupMillis"`
	AgentMillis        int64 `json:"agentMillis"`
	VerificationMillis int64 `json:"verificationMillis"`
	CleanupMillis      int64 `json:"cleanupMillis"`
	ModelMillis        int64 `json:"modelMillis"`

	ModelRequests  int  `json:"modelRequests"`
	TokensReported bool `json:"tokensReported"`

	MainUsage     domain.UsageSummary `json:"mainUsage"`
	ApproverUsage domain.UsageSummary `json:"approverUsage"`
	TotalUsage    domain.UsageSummary `json:"totalUsage"`
	ToolCalls     map[string]int      `json:"toolCalls"`
	Sandbox       string              `json:"sandbox"`
}

type Report struct {
	Label     string      `json:"label"`
	StartedAt time.Time   `json:"startedAt"`
	Runs      []RunResult `json:"runs"`

	HarnessRevision string `json:"harnessRevision"`
	DirtyBuild      bool   `json:"dirtyBuild"`
}

func decodeValue(value any, target any) error {
	return json.Unmarshal([]byte(agent.JSON(value)), target)
}

// Run requires a disposable datastore. Every task gets a fresh remote directory
// and agent/VM; original projects and conversations are never used for a run.
func Run(ctx context.Context, store *datastore.Instance, client providers.Client, plan Plan) (Report, error) {
	report := Report{Label: plan.Label, StartedAt: time.Now().UTC(), Runs: []RunResult{}}

	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			if setting.Key == "vcs.revision" {
				report.HarnessRevision = setting.Value
			}

			if setting.Key == "vcs.modified" {
				report.DirtyBuild = setting.Value == "true"
			}
		}
	}

	if plan.Repetitions == 0 {
		plan.Repetitions = 1
	}

	if plan.RunTimeoutSeconds == 0 {
		plan.RunTimeoutSeconds = 180
	}

	if plan.MaxModelRequests == 0 {
		plan.MaxModelRequests = 40
	}

	if len(plan.Models) == 0 ||
		len(plan.Models) > 20 ||
		plan.Repetitions < 1 ||
		plan.Repetitions > 10 ||
		plan.RunTimeoutSeconds < 1 ||
		plan.RunTimeoutSeconds > 1800 ||
		plan.MaxModelRequests < 1 ||
		plan.MaxModelRequests > 200 {
		return report, errors.New("provide 1–20 models, 1–10 repetitions, 1–1800 seconds and 1–200 requests per run")
	}

	var machine domain.Machine
	if err := store.Load("machine", plan.MachineID, &machine); err != nil {
		return report, err
	}

	selected := map[string]bool{}
	allCases := len(plan.Cases) == 0

	for _, id := range plan.Cases {
		selected[id] = true
	}

	cases := []taskCase{}
	for _, task := range builtinCases() {
		if allCases || selected[task.id] {
			cases = append(cases, task)
		}

		delete(selected, task.id)
	}

	if len(selected) > 0 {
		return report, errors.New("unknown evaluation case")
	}

	for _, model := range plan.Models {
		for _, choice := range []domain.ModelChoice{model, plan.Approver} {
			var catalog domain.LanguageModel
			if err := store.Load("model", choice.ProviderID+":"+choice.ModelID, &catalog); err != nil {
				return report, fmt.Errorf("model is not configured: %s/%s", choice.ProviderID, choice.ModelID)
			}
		}

		for _, task := range cases {
			for repetition := 1; repetition <= plan.Repetitions; repetition++ {
				if err := ctx.Err(); err != nil {
					return report, err
				}

				result := runCase(ctx, store, client, plan, machine, model, task, repetition)
				report.Runs = append(report.Runs, result)
			}
		}
	}

	return report, nil
}

func runCase(
	parent context.Context,
	store *datastore.Instance,
	client providers.Client,
	plan Plan,
	machine domain.Machine,
	model domain.ModelChoice,
	task taskCase,
	repetition int,
) (result RunResult) {
	digest := sha256.Sum256([]byte(agent.JSON(task.files) + task.prompt + task.verify))
	result = RunResult{
		Model:         model,
		Case:          task.id,
		FixtureSHA256: hex.EncodeToString(digest[:]),
		Repetition:    repetition,
		MachineID:     machine.ID,
	}

	start := time.Now()
	defer func() { result.DurationMillis = time.Since(start).Milliseconds() }()

	ctx, cancel := context.WithTimeout(parent, time.Duration(plan.RunTimeoutSeconds)*time.Second)
	defer cancel()

	for _, providerID := range []string{model.ProviderID, plan.Approver.ProviderID} {
		var configuration domain.Provider
		if err := store.Load("provider", providerID, &configuration); err != nil {
			result.Error = err.Error()
			return
		}

		if providerID == model.ProviderID {
			result.ProviderKind = configuration.Kind
		}

		// A copied refresh token must never rotate the original account's token.
		if configuration.AuthMode == "device" {
			if configuration.OAuth == nil ||
				time.Until(configuration.OAuth.ExpiresAt) < time.Duration(plan.RunTimeoutSeconds+120)*time.Second {
				result.Error = "evaluation requires a device session valid beyond this run; refresh the original session before creating a snapshot"
				return
			}

			configuration.OAuth.RefreshToken = ""

			if err := store.Save("provider", providerID, "", configuration); err != nil {
				result.Error = err.Error()
				return
			}
		}
	}

	meter := newMeter(client, plan.MaxModelRequests)
	engine, err := agent.New(store, meter)
	if err != nil {
		result.Error = err.Error()
		return
	}

	closed := false
	finish := func() {
		if !closed {
			engine.Close()
			closed = true
		}
	}

	connector := engine.Remote.Connector
	connection, err := connector.Dial(ctx, machine.ID)
	if err != nil {
		finish()
		result.Error = err.Error()

		return
	}
	defer connection.Close()

	if err := remote.CheckAvailability(ctx, connection, machine); err != nil {
		finish()
		result.Error = err.Error()

		return
	}

	directory, err := remote.Run(ctx, connection.Client, "mktemp -d /tmp/zentrale-eval-XXXXXXXX")
	directory = strings.TrimSpace(directory)
	if err != nil ||
		!strings.HasPrefix(directory, "/tmp/zentrale-eval-") ||
		path.Dir(directory) != "/tmp" ||
		!remote.ValidID(path.Base(directory)) {
		finish()
		result.Error = "could not create a private evaluation directory"

		return
	}

	project := domain.Project{
		ID:        agent.NewID(),
		Name:      "Harness evaluation",
		Directory: directory + "/project",
		MachineIDs: []string{
			machine.ID,
		},
	}

	result.ProjectID, result.RemoteDirectory = project.ID, project.Directory
	var record domain.Agent
	defer func() {
		cleanupStarted := time.Now()
		defer func() { result.CleanupMillis = time.Since(cleanupStarted).Milliseconds() }()

		finish()

		if record.ID != "" {
			if err := engine.Delete("agent", record.ID); err != nil {
				result.CleanupError = err.Error()
			}
		}

		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()

		cleanupConnection, err := connector.Dial(cleanup, machine.ID)
		if err == nil {
			defer cleanupConnection.Close()

			cleanupCommand := "rm -rf -- " + remote.Quote(directory)

			if record.ID != "" {
				cleanupCommand += " " + remote.Quote("/tmp/zentrale-"+record.ID)
			}

			_, err = remote.Run(cleanup, cleanupConnection.Client, cleanupCommand)
		}

		if err != nil {
			result.CleanupError = err.Error()
		}
	}()

	if err := store.Save("project", project.ID, "", project); err != nil {
		result.Error = err.Error()
		return
	}

	record, err = engine.CreateWithModels(project.ID, "Evaluation "+task.id, model, plan.Approver, "")
	if err != nil {
		result.Error = err.Error()
		return
	}

	result.AgentID = record.ID
	seed := "import json,pathlib\nroot=pathlib.Path(" + fmt.Sprintf("%q", project.Directory) + ")\nroot.mkdir(mode=0o700)\nfiles=json.loads(" + fmt.Sprintf("%q", agent.JSON(task.files)) + ")\nfor name,content in files.items():\n (root/name).write_text(content)\n"
	if _, err := remote.Run(ctx, connection.Client, "python3 -I -c "+remote.Quote(seed)); err != nil {
		result.Error = err.Error()
		return
	}

	prompt := task.prompt + "\nWork only in this project. You may install python3 inside your Fedora MicroVM with packages_install network=true if needed. Other internet access is unnecessary. Do not edit unrelated files. Use the actual tools and verify the result."
	updates, unsubscribe := engine.Subscribe()
	defer unsubscribe()

	if err := engine.PromptContext(ctx, record.ID, prompt); err != nil {
		result.Error = err.Error()
		return
	}

	result.SetupMillis = time.Since(start).Milliseconds()
	agentStarted := time.Now()

	for {
		var current domain.Agent
		if err := store.Load("agent", record.ID, &current); err != nil {
			result.Error = err.Error()
			break
		}

		if current.Status != "running" {
			if current.Status != "idle" {
				result.Error = current.Error

				if result.Error == "" {
					result.Error = "agent ended with status " + current.Status
				}
			}

			break
		}

		select {
		case <-updates:
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			result.Error = ctx.Err().Error()
		}

		if ctx.Err() != nil {
			break
		}
	}

	// Stop and join all work before collecting metrics or running a verifier.
	finish()
	result.AgentMillis = time.Since(agentStarted).Milliseconds()
	result.MainUsage, _ = store.UsageSummary(record.ID)
	result.ApproverUsage, _ = store.UsageSummary(record.ApproverID)
	agents, _ := store.List("agent", &project.ID, 500, 0)

	for _, entry := range agents {
		id, _ := entry["id"].(string)
		usage, _ := store.UsageSummary(id)
		result.TotalUsage.InputTokens += usage.InputTokens
		result.TotalUsage.OutputTokens += usage.OutputTokens
		result.TotalUsage.CachedTokens += usage.CachedTokens
		result.TotalUsage.RealCost += usage.RealCost
		result.TotalUsage.FictionalCost += usage.FictionalCost
		result.TotalUsage.UnknownPrices += usage.UnknownPrices
		result.TotalUsage.RequestCount += usage.RequestCount
		result.TotalUsage.Estimated = result.TotalUsage.Estimated || usage.Estimated
		activities, _ := store.List("tool_activity", &id, 500, 0)
		meter.collectActivities(activities)
	}

	result.ModelRequests, result.ModelMillis, result.TokensReported, result.ToolCalls = meter.calls, meter.duration.Milliseconds(), meter.tokensReported, meter.tools

	if result.Error != "" {
		return
	}

	temporary := "/tmp/zentrale-" + record.ID
	verificationStarted := time.Now()
	defer func() {
		result.VerificationMillis = time.Since(verificationStarted).Milliseconds()
	}()

	_, err = remote.Run(ctx, connection.Client, "mkdir -p -- "+remote.Quote(temporary))
	if err != nil {
		result.Error = err.Error()
		return
	}

	verification, level, err := remote.Sandbox(ctx, connection, machine, remote.Execution{
		AgentID:            record.ID,
		ProjectID:          project.ID,
		Workspace:          project.Directory,
		TemporaryDirectory: temporary,

		Command: "python3 -I -c " + remote.Quote(task.verify),
		Tooling: true,
	})

	result.Sandbox = level

	if err == nil {
		output, verifyError := remote.Run(ctx, connection.Client, verification)
		err = verifyError
		lines := strings.Split(strings.TrimSpace(output), "\n")
		if err == nil && lines[len(lines)-1] != "PASS" {
			err = errors.New("verifier did not produce its success receipt")
		}
	}

	if err != nil {
		result.Error = "verification failed: " + err.Error()
		return
	}

	if task.requiredTool != "" && result.ToolCalls[task.requiredTool] == 0 {
		result.Error = "required tool was not used: " + task.requiredTool
		return
	}

	result.Passed = true

	return
}
