package remote

import "fmt"

// Jump-only hosts participate in SSH routing, never in command execution,
// sandbox detection, daemon installation, terminals or editor forwarding.
func (connector *Connector) RequireWorkTarget(id string) error {
	machine, err := connector.Machine(id)
	if err != nil {
		return err
	}
	if machine.JumpHostOnly {
		return fmt.Errorf("machine %q is jump host only and cannot be used for work", machine.Name)
	}
	return nil
}

func (connection *Connection) requireWorkTarget() error {
	if connection.store == nil || connection.machineID == "" {
		return nil
	}
	return (&Connector{Store: connection.store}).RequireWorkTarget(connection.machineID)
}
