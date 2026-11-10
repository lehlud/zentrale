package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

var fedoraPackageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.-]{0,127}$`)

func fedoraInstallCommand(arguments map[string]any) (string, error) {
	packages, err := stringsArg(arguments, "packages")
	if err != nil || len(packages) == 0 || len(packages) > 64 {
		return "", errors.New("provide 1 to 64 Fedora package names")
	}

	quoted := make([]string, 0, len(packages))

	for _, name := range packages {
		if !fedoraPackageName.MatchString(name) {
			return "", errors.New("use Fedora package names, without options, URLs or shell expressions")
		}

		quoted = append(quoted, remote.Quote(name))
	}

	// Fedora Minimal may ship microdnf or dnf5. Neither executes on the SSH host.
	return `if command -v microdnf >/dev/null 2>&1; then
    manager=microdnf
elif command -v dnf5 >/dev/null 2>&1; then
    manager=dnf5
else
    manager=dnf
fi
exec "$manager" -y install ` + strings.Join(quoted, " "), nil
}

func (engine *Engine) installPackages(
	ctx context.Context,
	agentRecord domain.Agent,
	arguments map[string]any,
) (any, error) {
	command, err := fedoraInstallCommand(arguments)
	if err != nil {
		return nil, err
	}

	return engine.executeRemote(ctx, agentRecord, executionRequest{
		Command: command, Network: true, GuestRoot: true,
	})
}

func (engine *Engine) microVMTool(ctx context.Context, agentRecord domain.Agent, action string) (any, error) {
	releaseEnvironment := engine.useEnvironment(agentRecord.ID)
	defer releaseEnvironment()
	if action == "microvm_reset" && (agentRecord.Role != "main" || agentRecord.ParentID != "") {
		return nil, errors.New("only the main agent can reset the shared environment")
	}

	connection, machine, project, err := engine.connect(ctx, agentRecord)
	if err != nil {
		return nil, err
	}
	defer connection.Close()

	result, err := engine.manageVM(ctx, connection, machine, strings.TrimPrefix(action, "microvm_"), remote.Execution{
		AgentID:   agentRecord.ID,
		ProjectID: project.ID,

		Workspace:          project.Directory,
		TemporaryDirectory: agentTemporaryDirectory(agentRecord.ID),
	})
	if err == nil && action == "microvm_reset" {
		engine.invalidate(agentRecord.ID)
	}

	return result, err
}

// Called with the VM lifecycle lock held. A new command cannot pass staging
// between this check and reset; already prepared commands hold a preparation pin.
func (engine *Engine) prepareEnvironmentReset(agentID string) error {
	var owner domain.Agent
	if err := engine.Store.Load("agent", agentID, &owner); err != nil {
		return err
	}
	family, err := engine.agentFamily(owner)
	if err != nil {
		return err
	}
	children := make(map[string]bool, len(family))
	for id := range family {
		if id != agentID {
			children[id] = true
		}
	}
	if engine.agentsAreBusy(children) {
		return errors.New("stop subagents and running jobs before resetting the shared environment")
	}

	engine.mu.Lock()
	busy := engine.preparing[agentID] > 1 // The reset request itself holds one pin.
	for _, running := range engine.jobs {
		running.mu.Lock()
		busy = busy || family[running.job.AgentID]
		running.mu.Unlock()
	}
	engine.mu.Unlock()
	if busy {
		return errors.New("stop running jobs and wait for command preparation before resetting the shared environment")
	}

	for id := range family {
		if err := engine.Editors.StopAgent(id); err != nil {
			return err
		}
		engine.closeMCPServers(id)
	}
	return nil
}

// Remember every machine used by this agent, including one left during failover.
// Removing the agent must not leave its guest disk or supervisor behind.
type microVMRecord struct {
	AgentID   string `json:"agentId"`
	ProjectID string `json:"projectId"`
	MachineID string `json:"machineId"`
	Workspace string `json:"workspace"`
}

func (engine *Engine) rememberMicroVM(
	agentRecord domain.Agent,
	machine domain.Machine,
	project domain.Project,
	sandbox string,
) error {
	if sandbox != remote.SandboxMicroVM || machine.MicroVMCommand != "" {
		return nil
	}
	owner, err := engine.vmOwner(agentRecord.ID)
	if err != nil {
		return err
	}

	return engine.Store.Save("microvm", owner.ID+":"+machine.ID, owner.ID, microVMRecord{
		AgentID:   owner.ID,
		ProjectID: project.ID,
		MachineID: machine.ID,
		Workspace: project.Directory,
	})
}

func (engine *Engine) removeAgentMicroVMs(agentID string) error {
	return engine.manageAgentMicroVMs(context.Background(), agentID, "remove")
}

func (engine *Engine) manageAgentMicroVMs(ctx context.Context, agentID, action string) error {
	records, err := engine.listAll("microvm", &agentID)
	if err != nil {
		return err
	}

	for _, record := range records {
		machineID := stringArg(record, "machineId")
		operation := engine.vmOperation(agentID, machineID)
		if err := operation.Lock(ctx); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		connection, err := engine.Remote.Connector.Dial(ctx, machineID)
		if err != nil {
			cancel()
			operation.Unlock()
			return fmt.Errorf("%s VM on %s: %w", action, machineID, err)
		}

		machine, err := engine.Remote.Connector.Machine(machineID)
		if err == nil {
			_, err = remote.ManageMicroVM(ctx, connection, machine, action, remote.Execution{
				AgentID:   agentID,
				ProjectID: stringArg(record, "projectId"),

				Workspace:          stringArg(record, "workspace"),
				TemporaryDirectory: agentTemporaryDirectory(agentID),
			})
		}

		connection.Close()
		cancel()
		if err == nil {
			operation.stopped = action == "stop" || action == "remove"
		}
		operation.Unlock()

		if err != nil {
			return fmt.Errorf("%s VM on %s: %w", action, machineID, err)
		}
	}

	return nil
}
