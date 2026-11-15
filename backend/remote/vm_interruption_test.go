package remote

import (
	"context"
	"fmt"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestVMInvocationInterruptionRequiresNativeProtocol(t *testing.T) {
	for _, test := range []struct {
		name   string
		status uint32
		output string
		want   string
	}{
		{"replaced", 77, `ZENTRALE_VM_INVOCATION_INTERRUPTED {"reason":"generation_replaced"}`, "generation_replaced"},
		{"stopped", 77, `ZENTRALE_VM_INVOCATION_INTERRUPTED {"reason":"supervisor_stopped"}`, "supervisor_stopped"},
		{"ordinary exit", 1, `ZENTRALE_VM_INVOCATION_INTERRUPTED {"reason":"generation_replaced"}`, ""},
		{"successful command", 0, `ZENTRALE_VM_INVOCATION_INTERRUPTED {"reason":"generation_replaced"}`, ""},
		{"plain diagnostic", 77, "MicroVM connection was interrupted; its disk is retained", ""},
		{"invalid metadata", 77, `ZENTRALE_VM_INVOCATION_INTERRUPTED {`, ""},
		{"unknown reason", 77, `ZENTRALE_VM_INVOCATION_INTERRUPTED {"reason":"permission denied"}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, _ := commandLifecyclePeer(t, "", func(_ string, channel ssh.Channel, _ <-chan *ssh.Request) {
				fmt.Fprintln(channel.Stderr(), test.output)
				fixtureCommandExit(channel, test.status)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			output, err := RunPipe(ctx, client, "fixture", nil)
			failure := ParseVMInvocationFailure(output, err)
			if test.want == "" {
				if failure != nil {
					t.Fatalf("ordinary command became retryable: %v", failure)
				}
			} else if failure == nil || failure.Reason != test.want {
				t.Fatalf("lost VM interruption: %v, original: %v", failure, err)
			}
		})
	}
}
