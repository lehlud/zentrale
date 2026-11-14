package agent

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestToolPolicyPreservesReviewAndDiscoveryBoundaries(t *testing.T) {
	// Golden classifications are intentionally independent of the production
	// catalog: changing authorization or the JavaScript surface is not a refactor.
	groups := map[toolReview]string{
		reviewRequired: `apply_patch list_files write_file delete_file exec python
			venv_create packages_install microvm_reset microvm_info container websearch
			webfetch github clone_source source_git machine_list machine_properties
			machine_select resource_list storage_list storage_quota subagent steer_agent
			inspect_agent question_answer`,
		reviewReadSnapshot: `read_file`,
		reviewInHandler:    `write_stdin mcp_connect mcp_list mcp_tools mcp_call mcp_disconnect`,
		reviewNone: `code_mode tool_search approval_clarify context_clear context_rotate
			context_compact instructions_read update_plan create_goal get_goal update_goal
			tool_output note_save note_list note_read note_delete question_ask question_list
			question_withdraw job_status job_logs job_wait job_stop venv_cleanup`,
	}
	network := strings.Fields("packages_install websearch webfetch github clone_source")
	lifetime := strings.Fields("exec python venv_create packages_install container source_git clone_source")
	hidden := strings.Fields("code_mode tool_search approval_clarify context_clear context_rotate context_compact")
	wantNames := []string{"exec_command"}
	count := 0
	for review, actions := range groups {
		for _, action := range strings.Fields(actions) {
			count++
			want := toolPolicy{
				Review:          review,
				RequiresNetwork: slices.Contains(network, action),
				CommandLifetime: slices.Contains(lifetime, action),
				HideInCodeMode:  slices.Contains(hidden, action),
			}
			if got, exists := toolPolicies[action]; !exists || got != want {
				t.Errorf("%s: policy %+v (exists=%v), want %+v", action, got, exists, want)
			}
			if !want.HideInCodeMode {
				wantNames = append(wantNames, action)
			}
		}
	}
	if len(toolPolicies) != count {
		t.Fatalf("unclassified tools: catalog has %d, classified %d", len(toolPolicies), count)
	}
	slices.Sort(wantNames)
	if got := codeModeTools(); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("code-mode surface changed:\ngot  %v\nwant %v", got, wantNames)
	}
}

func TestDispatchPolicyGuardsBeforeReviewOrExecution(t *testing.T) {
	// No store, provider or SSH manager: these guards must not touch them.
	engine := &Engine{}
	for _, test := range []struct {
		action, arguments, want string
		agent                   domain.Agent
	}{
		{"unknown", `{}`, "unknown action", domain.Agent{}},
		{"webfetch", `{"url":"https://example.com"}`, "network=true", domain.Agent{}},
		{"clone_source", `{"timeout_ms":1000}`, "network=true", domain.Agent{}},
		{"exec", `{"timeout_ms":1000,"outsideSandbox":true}`, "outsideSandbox requires", domain.Agent{}},
		{"microvm_reset", `{}`, "only the main agent", domain.Agent{Role: "subagent"}},
		{"microvm_reset", `{}`, "only the main agent", domain.Agent{Role: "main", ParentID: "parent"}},
	} {
		t.Run(test.action+"/"+test.agent.Role, func(t *testing.T) {
			_, err := engine.dispatch(context.Background(), test.agent, providers.Call{
				Name: "harness", Arguments: map[string]any{"action": test.action, "arguments": test.arguments},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %q", err, test.want)
			}
		})
	}
	if _, err := engine.dispatch(context.Background(), domain.Agent{}, providers.Call{
		Name: "tool_search", Arguments: map[string]any{"query": "git"},
	}); err != nil {
		t.Fatalf("local discovery unexpectedly needed approval: %v", err)
	}
}
