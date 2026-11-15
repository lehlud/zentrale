package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestJobObservationsKeepEvidenceWithoutRepeatingLaunchScripts(t *testing.T) {
	for _, nested := range []bool{false, true} {
		job := map[string]any{
			"id": "job", "status": "failed", "command": strings.Repeat("echo setup\n", 1000),
			"output": "actual diagnostic", "error": "exit 1", "machineId": "machine",
		}
		result := map[string]any(job)
		if nested {
			result = map[string]any{"job": job, "nextCursor": "cursor", "hasMore": true}
		}

		original := JSON(map[string]any{"result": result})
		message := domain.Message{ID: "message", Role: "tool", ToolName: "job_wait", Content: original}
		projected := modelHistory([]domain.Message{message}, 128000)[0]
		if len(projected.Content)*5 >= len(original) {
			t.Fatalf("observation did not remove repeated script: %d -> %d bytes", len(original), len(projected.Content))
		}

		for _, evidence := range []string{"actual diagnostic", "exit 1", "failed", "machine", "readOriginal", "message", "tool_output"} {
			if !strings.Contains(projected.Content, evidence) {
				t.Fatalf("lost %q in %s", evidence, projected.Content)
			}
		}

		if nested && !strings.Contains(projected.Content, "cursor") {
			t.Fatal("pagination cursor lost")
		}

		if !json.Valid([]byte(projected.Content)) || message.Content != original {
			t.Fatal("invalid projection or changed original")
		}
	}
}

func TestObservationProjectionDoesNotRewriteApplicationData(t *testing.T) {
	content := JSON(map[string]any{"result": map[string]any{
		"id": "document", "status": "draft", "command": strings.Repeat("example ", 300),
	}})
	for _, name := range []string{"read_file", "exec", "github", "code_mode"} {
		message := domain.Message{ID: "message", Role: "tool", ToolName: name, Content: content}
		if actual := modelHistory([]domain.Message{message}, 128000)[0].Content; actual != content {
			t.Fatalf("%s application data changed", name)
		}
	}
}

func BenchmarkJobObservationProjection(b *testing.B) {
	original := JSON(map[string]any{"result": map[string]any{
		"job": map[string]any{
			"id": "job", "status": "completed", "command": strings.Repeat("echo setup\n", 1000),
			"output": "acceptance: PASS", "machineId": "machine",
		},
		"nextCursor": "cursor", "hasMore": false,
	}})
	message := domain.Message{ID: "message", Role: "tool", ToolName: "job_wait", Content: original}
	projected := modelHistory([]domain.Message{message}, 128000)[0].Content

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		result := modelHistory([]domain.Message{message}, 128000)
		if !strings.Contains(result[0].Content, "acceptance: PASS") {
			b.Fatal("lost verification evidence")
		}
	}

	b.ReportMetric(float64(len(original)), "original-B")
	b.ReportMetric(float64(len(projected)), "projected-B")
	b.ReportMetric(100*(1-float64(len(projected))/float64(len(original))), "saved-%")
}
