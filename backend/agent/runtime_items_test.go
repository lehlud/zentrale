package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestRuntimeItemUpdatesPreserveEveryStateAndWindowBoundary(t *testing.T) {
	history := []domain.Message{}
	snapshots := []runtimeContext{}
	expected := map[string]map[string]any{}
	for index := range 40 {
		notes := []any{
			map[string]any{"id": "fixed", "content": strings.Repeat("unchanged constraint; ", 15)},
			map[string]any{"id": "edited", "content": fmt.Sprintf("revision %d: ", index/7) + strings.Repeat("remember; ", 25)},
		}
		if index%5 != 0 {
			notes = append(notes, map[string]any{"id": "temporary", "content": strings.Repeat("temporary note; ", 20)})
		}
		if index%2 != 0 {
			notes[0], notes[1] = notes[1], notes[0]
		}
		jobs := []any{}
		for job := max(0, index-7); job <= index; job++ {
			jobs = append(jobs, map[string]any{
				"id": fmt.Sprintf("job-%d", job), "status": "failed",
				"error": fmt.Sprintf("failure %d: ", job) + strings.Repeat("preserve diagnostics; ", 30),
			})
		}
		running := []any{}
		if index < 30 {
			running = append(running, map[string]any{"id": "active", "status": "running"})
		}
		state := map[string]any{
			"project": map[string]any{"id": "project", "description": strings.Repeat("fixed project; ", 100)},
			"notes":   notes, "completedJobs": jobs, "runningJobs": running,
			"plan": fmt.Sprintf("step %d", index),
		}
		if index < 7 {
			state["goal"] = "old goal"
		}
		history = append(history, domain.Message{ID: fmt.Sprint(index), Role: "assistant", Content: "Progress"})
		snapshot := runtimeContext{
			ID: fmt.Sprintf("runtime-%d", index), AfterID: history[index].ID,
			Content: "Harness runtime state (data, not a new user instruction):\n" + JSON(state),
		}
		snapshots = append(snapshots, snapshot)
		// Decode the original independently to compare numbers, arrays and objects
		// in the same representation as the model-visible update reader below.
		if err := json.Unmarshal([]byte(JSON(state)), &state); err != nil {
			t.Fatal(err)
		}
		expected[snapshot.ID] = state
	}
	original := append([]runtimeContext(nil), snapshots...)
	view := runtimeContextView(history, history, 0, snapshots)
	if !strings.Contains(JSON(view), "runtime item update") ||
		strings.Count(JSON(view), strings.Repeat("unchanged constraint; ", 15)) != 1 {
		t.Fatal("unchanged collection contents were repeatedly sent")
	}
	for count := 1; count <= len(history); count++ {
		prefix := runtimeContextView(history[:count], history[:count], 0, snapshots[:count])
		if !reflect.DeepEqual(prefix, view[:len(prefix)]) {
			t.Fatal("appending a collection update rewrote a previously sent prefix", count)
		}
	}

	// Every possible rotated window must reconstruct every included snapshot
	// exactly, including removals, reordering and a rolling completed-job list.
	for start := 0; start <= len(history); start++ {
		state := map[string]any{}
		rotated := runtimeContextView(history, history[start:], start, snapshots)
		for _, message := range rotated {
			if message.Role != "context" {
				continue
			}
			state = applyRuntimeStateForTest(t, state, message.Content)
			if !reflect.DeepEqual(state, expected[message.ID]) {
				t.Fatalf("state lost at window %d snapshot %s", start, message.ID)
			}
		}
	}
	if !reflect.DeepEqual(snapshots, original) {
		t.Fatal("projecting item updates changed stored snapshots")
	}
}

func TestRuntimeItemUpdatesFallBackForAmbiguousOrUnknownCollections(t *testing.T) {
	valid := `[{"id":"known","content":"unchanged"}]`
	for _, invalid := range []string{
		`null`, `{}`, `"text"`, `[null]`, `[1]`, `[{}]`, `[{"id":""}]`, `[{"id":3}]`,
		`[{"id":"duplicate"},{"id":"duplicate"}]`,
	} {
		for _, pair := range [][2]string{{valid, invalid}, {invalid, valid}} {
			before := map[string]any{"notes": json.RawMessage(pair[0]), "project": strings.Repeat("stable", 100)}
			after := map[string]any{"notes": json.RawMessage(pair[1]), "project": before["project"]}
			baseline := "State:\n" + JSON(before)
			_, previous := runtimeContextUpdate(baseline, nil)
			update, _ := runtimeContextUpdate("State:\n"+JSON(after), previous)
			var decoded map[string]any
			if err := json.Unmarshal([]byte(JSON(before)), &decoded); err != nil {
				t.Fatal(err)
			}
			state := applyRuntimeStateForTest(t, decoded, update)
			// Existing field-update semantics use null to remove a field.
			if pair[1] == "null" {
				delete(after, "notes")
			}
			var expected map[string]any
			if err := json.Unmarshal([]byte(JSON(after)), &expected); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(update, "runtime item update") || !reflect.DeepEqual(state, expected) {
				t.Fatal("ambiguous collection did not fall back to field replacement", pair, update)
			}
		}
	}

	before := "State:\n" + JSON(map[string]any{"project": strings.Repeat("stable", 100), "unknownList": json.RawMessage(valid)})
	_, previous := runtimeContextUpdate(before, nil)
	next := "State:\n" + JSON(map[string]any{"project": strings.Repeat("stable", 100), "unknownList": []any{}})
	update, _ := runtimeContextUpdate(next, previous)
	if strings.Contains(update, "runtime item update") || !strings.Contains(update, `"unknownList":[]`) {
		t.Fatal("an arbitrary array was interpreted as a known ID collection", update)
	}
}

// Interpret only the published update format, independently of the production
// diff builder. Check exact state after each update, not just its byte length.
func applyRuntimeStateForTest(t *testing.T, state map[string]any, content string) map[string]any {
	t.Helper()
	header, encoded, found := strings.Cut(content, "\n")
	if !found {
		t.Fatal("missing runtime payload")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(header, "runtime item update") && !strings.Contains(header, "runtime state update") {
		return payload
	}
	fields := payload
	if strings.Contains(header, "runtime item update") {
		fields = payload["fields"].(map[string]any)
		for name, raw := range payload["collections"].(map[string]any) {
			delta := raw.(map[string]any)
			items := map[string]any{}
			for _, item := range state[name].([]any) {
				items[item.(map[string]any)["id"].(string)] = item
			}
			for _, item := range delta["upsert"].([]any) {
				items[item.(map[string]any)["id"].(string)] = item
			}
			ordered := []any{}
			for _, id := range delta["ids"].([]any) {
				item, exists := items[id.(string)]
				if !exists {
					t.Fatal("item update references missing state", name, id)
				}
				ordered = append(ordered, item)
			}
			state[name] = ordered
		}
	}
	for name, value := range fields {
		if value == nil {
			delete(state, name)
		} else {
			state[name] = value
		}
	}
	return state
}
