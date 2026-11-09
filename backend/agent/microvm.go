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
	if action == "microvm_reset" {
		engine.mu.Lock()

		for _, running := range engine.jobs {
			running.mu.Lock()
			owned := running.job.AgentID == agentRecord.ID
			running.mu.Unlock()

			if owned {
				engine.mu.Unlock()
				return nil, errors.New("stop your running jobs before resetting the MicroVM")
			}
		}

		engine.mu.Unlock()

		if err := engine.Editors.StopAgent(agentRecord.ID); err != nil {
			return nil, err
		}

		engine.closeMCPServers(agentRecord.ID)
	}

	connection, machine, project, err := engine.connect(ctx, agentRecord)
	if err != nil {
		return nil, err
	}
	defer connection.Close()

	result, err := remote.ManageMicroVM(ctx, connection, machine, strings.TrimPrefix(action, "microvm_"), remote.Execution{
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

	return engine.Store.Save("microvm", agentRecord.ID+":"+machine.ID, agentRecord.ID, microVMRecord{
		AgentID:   agentRecord.ID,
		ProjectID: project.ID,
		MachineID: machine.ID,
		Workspace: project.Directory,
	})
}

func (engine *Engine) removeAgentMicroVMs(agentID string) error {
	return engine.manageAgentMicroVMs(context.Background(), agentID, "remove")
}

// StopAgentVM releases passthrough devices while preserving the agent's guest
// disk. Serialize against prompts and editor preparation so work cannot restart
// the VM midway through stopping it.
func (engine *Engine) StopAgentVM(ctx context.Context, agentID string) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	var record domain.Agent
	if err := engine.Store.Load("agent", agentID, &record); err != nil {
		return err
	}

	if record.Role == "approver" {
		return errors.New("select the working agent to stop its VM")
	}

	if record.Status == "running" ||
		record.Status == "waiting" ||
		engine.agentsAreBusy(map[string]bool{
			agentID: true,
		}) {
		return errors.New("stop the agent and its running jobs before stopping its VM")
	}

	if err := engine.Editors.StopAgent(agentID); err != nil {
		return err
	}

	engine.closeMCPServers(agentID)

	if err := engine.manageAgentMicroVMs(ctx, agentID, "stop"); err != nil {
		return err
	}

	engine.invalidate(agentID)
	engine.releaseConnection(agentID)
	engine.Emit("resources", agentID, nil)

	return nil
}

func (engine *Engine) manageAgentMicroVMs(ctx context.Context, agentID, action string) error {
	records, err := engine.listAll("microvm", &agentID)
	if err != nil {
		return err
	}

	for _, record := range records {
		machineID := stringArg(record, "machineId")
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		connection, err := engine.Remote.Connector.Dial(ctx, machineID)
		if err != nil {
			cancel()
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

		if err != nil {
			return fmt.Errorf("%s VM on %s: %w", action, machineID, err)
		}
	}

	return nil
}
