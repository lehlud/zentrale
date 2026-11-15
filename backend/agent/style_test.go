package agent

import (
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

// Check the shared prompt contract, not a model's measured formatting behavior.
func TestSystemPromptRequiresSemanticWhitespace(t *testing.T) {
	for _, role := range []string{"main", "subagent"} {
		t.Run(role, func(t *testing.T) {
			prompt := (&Engine{}).buildSystemPrompt(domain.Agent{Role: role})

			for _, guidance := range []string{
				"Use blank lines to separate semantic steps across languages",
				"Only tightly related statements belong together without a blank line",
				"separate independent guard clauses",
				"distinct setup, processing and result-building steps",
				"Single-line groups are valid: never merge unrelated statements just to avoid them",
				"Choose boundaries by meaning, not a fixed line count",
				"keep cohesive data literals and call/error-check pairs together",
				"Split long expressions into readable multiline forms or well-named intermediate values",
				"formatters do not decide semantic grouping",
			} {
				if !strings.Contains(prompt, guidance) {
					t.Errorf("missing readability guidance: %q", guidance)
				}
			}
		})
	}
}
