package agent

import (
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

// These are prompt-contract tests, not a claim about any provider's measured
// delegation rate. Keep the default and useful exceptions visible to models.
func TestSystemPromptSetsConservativeDelegationDefault(t *testing.T) {
	prompt := (&Engine{}).buildSystemPrompt(domain.Agent{Role: "main"})
	for _, guidance := range []string{
		"Default to doing the work yourself",
		"Do not create subagents for routine tasks, small fixes",
		"tightly coupled sequential steps",
		"user explicitly requests delegation",
		"substantial, well-isolated workstream",
		"clear expected net benefit",
		"specialized expertise",
		"context isolation",
		"startup, context transfer, model cost",
		"If the benefit is unclear, continue yourself",
		"background/GPU jobs usually do not need a subagent",
		"smallest useful number of subagents",
		"Reuse or steer a suitable existing child",
		"verify the results before integrating them",
		"Delegation never expands the user's permissions or replaces independent approval",
		"Subagents cannot spawn further subagents",
	} {
		if !strings.Contains(prompt, guidance) {
			t.Errorf("missing delegation guidance: %q", guidance)
		}
	}
}

func TestSubagentDiscoveryIncludesDelegationThreshold(t *testing.T) {
	var help string
	for _, entry := range searchToolCatalog("subagent") {
		if strings.HasPrefix(entry, "subagent {") {
			help = entry
			break
		}
	}
	for _, guidance := range []string{
		"subagent {name,task,providerId?,approverProviderId?}",
		"main agents only",
		"default to doing the work yourself",
		"substantial, well-isolated work",
		"clear expected net benefit",
		"startup/coordination/review overhead",
		"user explicitly requests delegation",
		"reuse or steer a suitable existing child",
	} {
		if !strings.Contains(help, guidance) {
			t.Errorf("discovery missing delegation guidance: %q", guidance)
		}
	}
	if policy, ok := toolPolicies["subagent"]; !ok || policy.Review != reviewRequired {
		t.Fatal("delegation must still require independent approval")
	}
}
