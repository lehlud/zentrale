package remote

import (
	"context"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func HostKeyChallenge(err error) domain.SSHHostKey {
	var identity *HostKeyError
	if errors.As(err, &identity) {
		return domain.SSHHostKey{
			Host:        identity.Host,
			Fingerprint: identity.Fingerprint,
			PublicKey:   identity.PublicKey,
		}
	}

	return domain.SSHHostKey{}
}

func (connector *Connector) Check(ctx context.Context, machineID string) domain.MachineConnectionCheck {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	machine, err := connector.Machine(machineID)
	if err != nil {
		return domain.MachineConnectionCheck{Error: err.Error()}
	}

	connection, err := connector.Dial(ctx, machineID)
	if err != nil {
		return domain.MachineConnectionCheck{Error: err.Error(), HostKey: HostKeyChallenge(err)}
	}
	defer connection.Close()

	return domain.MachineConnectionCheck{Connected: true, Sandbox: InspectSandbox(ctx, connection, machine)}
}
