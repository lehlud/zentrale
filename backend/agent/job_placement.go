package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type preparedExecution struct {
	connection       *remote.Connection
	machine          domain.Machine
	project          domain.Project
	command, sandbox string
	release          func()
}

func (engine *Engine) prepareJobExecution(
	ctx context.Context,
	record domain.Agent,
	request *executionRequest,
) (*preparedExecution, error) {
	if request.GPU && request.OutsideSandbox {
		return nil, errors.New("GPU jobs require the managed q35 VM; outsideSandbox is incompatible")
	}

	var failures []error

	for {
		connection, machine, project, release, err := engine.connectForJob(ctx, record, request)
		if err != nil {
			return nil, errors.Join(append(failures, err)...)
		}

		prepare := func() (string, string, error) {
			if request.RequireContainers && !machine.Containers {
				return "", "", errors.New("container runtime not enabled on this machine")
			}

			identity, err := engine.ProjectCommitIdentity(project)
			if err != nil {
				return "", "", err
			}

			temporary, err := engine.temporaryDirectory(record.ID)
			if err != nil {
				return "", "", err
			}
			if err := prepareRemoteWorkspace(ctx, connection, project.Directory, temporary); err != nil {
				return "", "", err
			}

			if request.GPU {
				if err := engine.rememberMicroVM(record, machine, project, remote.SandboxMicroVM); err != nil {
					return "", "", err
				}
			}

			return engine.sandbox(ctx, connection, machine, remote.Execution{
				AgentID:            record.ID,
				ProjectID:          project.ID,
				GuestRoot:          request.GuestRoot,
				Workspace:          project.Directory,
				TemporaryDirectory: temporary,

				Command:        remote.WithCommitIdentity(request.Command, identity),
				Network:        request.Network,
				OutsideSandbox: request.OutsideSandbox,
				ProtocolIO:     request.Interactive,
				TTY:            request.TTY,
			})
		}

		command, sandbox, err := prepare()
		if err == nil {
			return &preparedExecution{
				connection: connection,
				machine:    machine,
				project:    project,
				release:    release,
				command:    command,
				sandbox:    sandbox,
			}, nil
		}

		connection.Close()
		release()

		// No approved user command has started yet. An automatic GPU placement
		// may try another host if VFIO is held by another VM or setup fails.
		if !request.GPU || request.MachineID != "" || ctx.Err() != nil {
			return nil, err
		}

		if request.excludedMachines == nil {
			request.excludedMachines = map[string]bool{}
		}

		request.excludedMachines[machine.ID] = true
		failures = append(failures, fmt.Errorf("%s: %w", machine.ID, err))
	}
}

func (engine *Engine) connectForJob(
	ctx context.Context,
	record domain.Agent,
	request *executionRequest,
) (*remote.Connection, domain.Machine, domain.Project, func(), error) {
	noop := func() {}

	if request.MachineID == "" && !request.GPU {
		connection, machine, project, err := engine.connect(ctx, record)
		return connection, machine, project, noop, err
	}

	var project domain.Project
	if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
		return nil, domain.Machine{}, project, noop, err
	}

	owner := "exec:" + NewID()
	release := func() { engine.Remote.Release(owner) }
	connection, machine, err := engine.Remote.ConnectForJob(ctx, project, owner, request.MachineID, request.GPU, request.excludedMachines)
	if err != nil {
		release()
		return nil, machine, project, noop, err
	}

	if err = remote.CheckRuntime(ctx, connection); err == nil {
		project, err = engine.Remote.ResolveProject(ctx, connection, machine, project)
	}

	if err == nil {
		machine.StorageMounts, err = engine.Remote.PrepareStorage(ctx, connection, machine, project)
	}

	if err != nil {
		connection.Close()
		release()

		return nil, machine, project, noop, err
	}

	request.leaseOwner = owner

	return connection, machine, project, release, nil
}
