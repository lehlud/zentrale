package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"golang.org/x/crypto/ssh"
)

func TestSandboxInspectionRetainsCancellationAndExitStatus(t *testing.T) {
	client, _ := commandLifecyclePeer(t, "", func(_ string, channel ssh.Channel, _ <-chan *ssh.Request) {
		fixtureCommandExit(channel, 1)
	})
	connection := &Connection{Client: client}
	machine := domain.Machine{ID: "worker", Sandbox: SandboxMicroVM}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	status, err := InspectSandboxWithError(ctx, connection, machine)
	if !errors.Is(err, context.Canceled) || status.Error == "" || status.MachineID != machine.ID {
		t.Fatalf("lost cancellation or diagnostic: status=%+v error=%v", status, err)
	}
	status, err = InspectSandboxWithError(context.Background(), connection, machine)
	var exit *ssh.ExitError
	if !errors.As(err, &exit) || exit.ExitStatus() != 1 || status.Error == "" {
		t.Fatalf("lost ordinary command failure: status=%+v error=%v", status, err)
	}
}
