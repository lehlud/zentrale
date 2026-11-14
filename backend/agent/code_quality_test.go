package agent

import (
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

// These tests protect the prompt contract, not any model's measured code quality.
func TestAgentPromptsRequireReadableCode(t *testing.T) {
	for _, role := range []string{"main", "subagent"} {
		t.Run(role, func(t *testing.T) {
			prompt := (&Engine{}).buildSystemPrompt(domain.Agent{Role: role})
			for _, guidance := range []string{
				"Write clean, clear, human-readable code",
				"proactively introduce appropriate abstractions",
				"Do not sacrifice readability for compactness or add unnecessary abstraction",
				"Non-human-readable code is allowed only when the user explicitly requests it",
			} {
				if !strings.Contains(prompt, guidance) {
					t.Errorf("missing code-quality guidance: %q", guidance)
				}
			}
		})
	}
}

func TestReviewerPromptsCheckOnlyObviousCodeQualityViolations(t *testing.T) {
	for name, prompt := range map[string]string{
		"action approval": approverInstructions,
		"goal completion": goalReviewInstructions,
	} {
		t.Run(name, func(t *testing.T) {
			for _, guidance := range []string{
				"new or changed code is clean, clear, human-readable",
				"uses appropriate abstractions",
				"Reject only obvious code-quality violations",
				"heavily compressed or obfuscated handwritten code",
				"do not nitpick style or demand speculative refactoring",
				"Non-human-readable code is allowed only when the original user instructions explicitly request it",
			} {
				if !strings.Contains(prompt, guidance) {
					t.Errorf("missing code-quality review guidance: %q", guidance)
				}
			}
		})
	}
}
