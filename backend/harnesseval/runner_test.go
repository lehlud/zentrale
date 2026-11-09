package harnesseval

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func evaluationStore(t *testing.T) *datastore.Instance {
	t.Helper()
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	peer, err := testssh.StartWithCommandHandler(func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		process := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		process.Stdout, process.Stderr = stdout, stderr
		input, err := process.StdinPipe()
		if err != nil {
			return 1
		}
		go func() { _, _ = io.Copy(input, stdin); _ = input.Close() }()
		if err := process.Run(); err != nil {
			return 1
		}
		return 0
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.Close)
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	public, err := remote.DecodePublicKey(peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}
	machine := domain.Machine{ID: "eval-machine", Name: "Evaluation peer", Host: peer.Host, Port: peer.Port, User: "developer", KeyID: key.ID, Sandbox: "none"}
	_ = store.Save("machine", machine.ID, "", machine)
	for _, kind := range []string{"openai", "anthropic", "gemini", "mistral", "openrouter", "litellm", "opencode"} {
		_ = store.Save("provider", kind, "", domain.Provider{ID: kind, Kind: kind, BaseURL: "https://fixture.invalid"})
		_ = store.Save("model", kind+":fixture", kind, domain.LanguageModel{ID: "fixture", ProviderID: kind, ContextWindow: 128000, PricesKnown: true, InputPrice: 1, OutputPrice: 2})
	}
	_ = store.Save("provider", "approver", "", domain.Provider{ID: "approver", Kind: "openai"})
	_ = store.Save("model", "approver:fixture", "approver", domain.LanguageModel{ID: "fixture", ProviderID: "approver", ContextWindow: 128000, PricesKnown: true})
	return store
}

// Scripted decisions test the real harness/SSH/verifier path, not model quality.
type scriptedModels struct {
	mutex sync.Mutex
	turns map[string]int
	fail  bool
}

func (models *scriptedModels) Complete(_ context.Context, request providers.Request) (providers.Result, error) {
	models.mutex.Lock()
	defer models.mutex.Unlock()
	result := providers.Result{UsageKnown: true, InputTokens: 50, OutputTokens: 10, CachedTokens: 5}
	if strings.Contains(request.System, "goal_completion_review") {
		result.Text = `{"status":"complete","reason":"Scripted completion audit","evidence":"Fixture response; the independent external verifier still validates the actual files"}`
		return result, nil
	}
	if request.Config.ID == "approver" {
		result.Text = `{"allowed":true,"reason":"","blanket":false}`
		return result, nil
	}
	if models.fail {
		result.Text = "Done."
		return result, nil
	}
	turn := models.turns[request.CacheKey]
	models.turns[request.CacheKey] = turn + 1
	history := agent.JSON(request.Messages)
	id := ""
	for _, message := range request.Messages {
		if message.Role != "tool" {
			continue
		}
		var envelope struct {
			Result map[string]any `json:"result"`
		}
		_ = json.Unmarshal([]byte(message.Content), &envelope)
		if value, ok := envelope.Result["jobId"].(string); ok {
			id = value
		}
		if job, ok := envelope.Result["job"].(map[string]any); ok {
			if value, ok := job["id"].(string); ok {
				id = value
			}
		}
	}
	var name string
	var args map[string]any
	switch {
	case strings.Contains(history, "Fix total(values)"):
		switch turn {
		case 0:
			name, args = "read_file", map[string]any{"path": "tally.py"}
		case 1:
			name, args = "apply_patch", map[string]any{"input": "*** Begin Patch\n*** Update File: tally.py\n@@\n-    return sum(set(values))\n+    return sum(values)\n*** End Patch"}
		case 2:
			name, args = "exec_command", map[string]any{"cmd": "python3 -c 'from tally import total; assert total([2,2,3])==7'", "yield_time_ms": 1000}
		}
	case strings.Contains(history, "Run counter.py"):
		switch turn {
		case 0:
			name, args = "exec_command", map[string]any{"cmd": "python3 -u counter.py", "interactive": true, "yield_time_ms": 1000}
		case 1:
			name, args = "write_stdin", map[string]any{"id": id, "chars": "2\n5\nquit\n", "yield_time_ms": 500}
		case 2:
			name, args = "harness", map[string]any{"action": "job_wait", "arguments": agent.JSON(map[string]any{"id": id, "wait_time_ms": 1000})}
		}
	default:
		switch turn {
		case 0:
			name, args = "exec_command", map[string]any{"cmd": "python3 build.py", "yield_time_ms": 1000}
		case 1:
			name, args = "harness", map[string]any{"action": "job_logs", "arguments": agent.JSON(map[string]any{"id": id, "tail": true})}
		case 2:
			name, args = "apply_patch", map[string]any{"input": "*** Begin Patch\n*** Update File: compute.py\n@@\n-    return n*(n-1)//2\n+    return n*(n+1)//2\n*** End Patch"}
		case 3:
			name, args = "exec_command", map[string]any{"cmd": "python3 -c 'from compute import triangle; assert triangle(10)==55'", "yield_time_ms": 1000}
		}
	}
	if name == "" {
		result.Text = "Verified."
		return result, nil
	}
	result.Calls = []providers.Call{{ID: "step", Name: name, Arguments: args}}
	switch request.Config.Kind {
	case "anthropic":
		result.Wire = agent.JSON([]any{map[string]any{"type": "tool_use", "id": "step", "name": name, "input": args}})
	case "gemini":
		result.Calls[0].ID = "gemini-0"
		result.Wire = agent.JSON(map[string]any{"parts": []any{map[string]any{"functionCall": map[string]any{"name": name, "args": args}}}})
	case "openai":
		result.Wire = agent.JSON([]any{map[string]any{"type": "function_call", "call_id": "step", "name": name, "arguments": agent.JSON(args)}})
	default:
		result.Wire = agent.JSON(map[string]any{"tool_calls": []any{map[string]any{"id": "step", "type": "function", "function": map[string]any{"name": name, "arguments": agent.JSON(args)}}}})
	}
	return result, nil
}

func TestEvaluationUsesAllProviderKindsAndIndependentVerifiers(t *testing.T) {
	store := evaluationStore(t)
	plan := Plan{MachineID: "eval-machine", Approver: domain.ModelChoice{ProviderID: "approver", ModelID: "fixture"}, RunTimeoutSeconds: 20}
	for _, kind := range []string{"openai", "anthropic", "gemini", "mistral", "openrouter", "litellm", "opencode"} {
		plan.Models = append(plan.Models, domain.ModelChoice{ProviderID: kind, ModelID: "fixture"})
	}
	report, err := Run(context.Background(), store, &scriptedModels{turns: map[string]int{}}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Runs) != 21 {
		t.Fatal(len(report.Runs))
	}
	for _, result := range report.Runs {
		if !result.Passed || result.CleanupError != "" || result.ModelRequests == 0 || !result.TokensReported || result.TotalUsage.RequestCount != result.ModelRequests || result.DurationMillis == 0 {
			t.Errorf("%+v", result)
		}
	}
}

func TestEvaluationRejectsUnverifiedModelClaimsAndHonorsCaseSelection(t *testing.T) {
	store := evaluationStore(t)
	plan := Plan{MachineID: "eval-machine", Models: []domain.ModelChoice{{ProviderID: "openai", ModelID: "fixture"}}, Approver: domain.ModelChoice{ProviderID: "approver", ModelID: "fixture"}, Cases: []string{"focused-edit"}, RunTimeoutSeconds: 10}
	report, err := Run(context.Background(), store, &scriptedModels{turns: map[string]int{}, fail: true}, plan)
	if err != nil || len(report.Runs) != 1 || report.Runs[0].Passed || !strings.Contains(report.Runs[0].Error, "verification failed") {
		t.Fatal(report, err)
	}
	if strings.Contains(agent.JSON(report), "x509_data") || strings.Contains(agent.JSON(report), "credential") {
		t.Fatal("report contains connection secrets")
	}
}
