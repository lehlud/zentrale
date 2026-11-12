package agent

import (
	"math"
	"testing"
	"time"

	"github.com/lehlud/zentrale/providers"
)

func TestCommandTimeoutRequiresFinitePositiveInteger(t *testing.T) {
	for _, value := range []any{nil, 0.0, -1.0, 1.5, math.Inf(1), math.NaN(), 604800001.0, "30000", true} {
		args := map[string]any{}
		if value != nil {
			args["timeout_ms"] = value
		}
		if _, err := commandTimeout(args); err == nil {
			t.Errorf("accepted invalid timeout %#v", value)
		}
	}
	for _, milliseconds := range []float64{1, 30000, 600000, 604800000} {
		got, err := commandTimeout(map[string]any{"timeout_ms": milliseconds})
		if err != nil || got != time.Duration(milliseconds)*time.Millisecond {
			t.Fatalf("timeout %v: %v, %v", milliseconds, got, err)
		}
	}
}

func TestBothExecutionToolFormsRequireTimeoutBeforeReview(t *testing.T) {
	for _, call := range []providers.Call{
		{Name: "exec_command", Arguments: map[string]any{"cmd": "true"}},
		{Name: "harness", Arguments: map[string]any{"action": "exec", "arguments": `{"command":"true"}`}},
	} {
		if _, err := decodeInteraction(call); err == nil {
			t.Fatalf("%s accepted missing timeout", call.Name)
		}
	}
}
