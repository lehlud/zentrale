package remote

import (
	"encoding/json"
	"errors"
	"strings"
)

// VMInvocationInterrupted means the trusted runner lost the invocation's VM
// generation. It does not prove that the command never started or authorize a
// replay. Only harness-owned, idempotent preparation may retry this failure.
type VMInvocationInterrupted struct {
	Reason string `json:"reason"`
}

func (failure *VMInvocationInterrupted) Error() string {
	return "managed environment invocation interrupted (disk retained): " + failure.Reason
}

// ParseVMInvocationFailure is only for commands staged by the native managed
// runner. Neither a human diagnostic string nor exit code 77 alone is proof.
func ParseVMInvocationFailure(output string, err error) *VMInvocationInterrupted {
	var exit interface{ ExitStatus() int }
	if !errors.As(err, &exit) || exit.ExitStatus() != 77 {
		return nil
	}
	const marker = "ZENTRALE_VM_INVOCATION_INTERRUPTED "
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		var failure VMInvocationInterrupted
		if json.Unmarshal([]byte(strings.TrimPrefix(line, marker)), &failure) != nil {
			continue
		}
		switch failure.Reason {
		case "generation_replaced", "supervisor_stopped":
			return &failure
		}
	}
	return nil
}
