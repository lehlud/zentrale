package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestCodeModeUsesReviewedRemoteToolsAcrossProviders(t *testing.T) {
	for _, kind := range []string{
		"openai",
		"anthropic",
		"gemini",
		"openrouter",
		"mistral",
		"litellm",
		"opencode",
	} {
		t.Run(kind, func(t *testing.T) {
			engine, agent, project := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
				return providers.Result{
					Text:       `{"allowed":true,"reason":"","blanket":false}`,
					UsageKnown: true,
				}, nil
			}), shellPeerHandler("/bin/sh", os.Environ()))

			var provider domain.Provider
			_ = engine.Store.Load("provider", "approver-provider", &provider)
			provider.Kind = kind
			_ = engine.Store.Save("provider", provider.ID, "", provider)
			code := `
const patch = "*** Begin Patch\n*** Add File: answer.py\n+print(42)\n*** End Patch";
const edit = await tools.apply_patch({input:patch});
if (edit.error) throw new Error(edit.error);
const check = await tools.exec_command({cmd:"python3 answer.py",yield_time_ms:1000});
if (check.error) throw new Error(check.error);
print(check.job.output.trim());
`

			call := providers.Call{ID: "combined", Name: "code_mode", Arguments: map[string]any{"code": code}}
			if err := engine.executeCalls(context.Background(), agent, []providers.Call{call}); err != nil {
				t.Fatal(err)
			}

			content, err := os.ReadFile(filepath.Join(project.Directory, "answer.py"))
			if err != nil || string(content) != "print(42)\n" {
				t.Fatal(string(content), err)
			}

			history, _ := engine.loadHistory(agent.ID)
			if len(history) != 1 ||
				!strings.Contains(history[0].Content, `"output":"42\n"`) ||
				strings.Contains(history[0].Content, "print(42)") ||
				strings.Contains(history[0].Content, "_activities") {
				t.Fatal("intermediate results leaked into model context", history)
			}

			var audit struct {
				Activities []domain.ToolActivity `json:"activities"`
			}
			if err := engine.Store.Load("tool_activity", history[0].ID, &audit); err != nil ||
				len(audit.Activities) != 2 {
				t.Fatal(audit, err)
			}

			approvals, _ := engine.listAll("approval", &agent.ID)
			changes, _ := engine.listAll("file_change", &agent.ID)
			if len(approvals) != 2 || len(changes) != 1 {
				t.Fatal("inner actions bypassed approval or lost diffs", approvals, changes)
			}
		})
	}
}

func TestCodeModeCannotBypassDenialsOrResetLiveContext(t *testing.T) {
	engine, agent := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{
			Text:       `{"allowed":false,"reason":"Not authorized","blanket":false}`,
			UsageKnown: true,
		}, nil
	}))

	result, err := engine.runCodeMode(
		context.Background(),
		agent,
		map[string]any{
			"code": `print(await tools.exec_command({cmd:"touch denied"}));`,
		},
	)
	if err != nil || !strings.Contains(JSON(result), "Not authorized") {
		t.Fatal(result, err)
	}

	for _, code := range []string{`tools.context_clear({});`, `__call("code_mode",{code:"print(1)"});`} {
		if _, err := engine.runCodeMode(context.Background(), agent, map[string]any{
			"code": code,
		}); err == nil {
			t.Fatal("recursive or context-reset call allowed")
		}
	}
}

func TestInputDuringGenerationPreventsStaleTools(t *testing.T) {
	var engine *Engine
	turns, approvals := 0, 0
	provider := providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if request.Config.ID == "approver-provider" {
			approvals++
			return providers.Result{
				Text:       `{"allowed":true,"reason":"","blanket":false}`,
				UsageKnown: true,
			}, nil
		}

		turns++

		if turns == 1 {
			if err := engine.Prompt(request.CacheKey, "New instruction: do not create files; report status only."); err != nil {
				t.Fatal(err)
			}

			return responseCalls(providers.Call{
				ID:   "stale",
				Name: "apply_patch",
				Arguments: map[string]any{
					"input": "*** Begin Patch\n*** Add File: stale.txt\n+must not exist\n*** End Patch",
				},
			}), nil
		}

		if !strings.Contains(JSON(request.Messages), "New instruction") {
			t.Fatal("new input missing")
		}

		return providers.Result{Text: "Status reported.", UsageKnown: true}, nil
	})

	var agent domain.Agent
	var project domain.Project
	engine, agent, project = executionTestEngine(t, provider, shellPeerHandler("/bin/sh", os.Environ()))
	if err := engine.Prompt(agent.ID, "Create a file."); err != nil {
		t.Fatal(err)
	}

	if final := waitForAgent(t, engine, agent.ID); final.Status != "idle" || turns != 2 || approvals != 0 {
		t.Fatal(final, turns, approvals)
	}

	if _, err := os.Stat(filepath.Join(project.Directory, "stale.txt")); !os.IsNotExist(err) {
		t.Fatal("stale tool executed")
	}
}
