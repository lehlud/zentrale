package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

const foregroundCommandLimit = 90 * time.Second

type executionRequest struct {
	MachineID        string
	GPU              bool
	Background       bool
	leaseOwner       string
	excludedMachines map[string]bool

	Command        string
	DisplayCommand string
	Input          io.Reader

	Network           bool
	OutsideSandbox    bool
	RequireContainers bool
	GuestRoot         bool

	YieldTime   time.Duration
	Interactive bool
	TTY         bool
	OutputBytes int
}

func (engine *Engine) execute(
	ctx context.Context,
	agentRecord domain.Agent,
	command string,
	network, outsideSandbox bool,
) (any, error) {
	return engine.executeRemote(ctx, agentRecord, executionRequest{
		Command: command, Network: network, OutsideSandbox: outsideSandbox,
	})
}

func (engine *Engine) executeRemote(
	ctx context.Context,
	agentRecord domain.Agent,
	request executionRequest,
) (any, error) {
	releaseEnvironment := engine.useEnvironment(agentRecord.ID)
	defer releaseEnvironment()
	command := request.Command
	request.Interactive = request.Interactive || request.TTY

	if strings.TrimSpace(command) == "" || len(command) > 200000 || !noSecrets(command) {
		return nil, errors.New("invalid command")
	}

	if request.Interactive && (request.Input != nil || len(command) > 64000) {
		return nil, errors.New("interactive commands must be at most 64 KB and cannot use a fixed stdin payload")
	}

	engine.invalidate(agentRecord.ID)
	prepared, err := engine.prepareJobExecution(ctx, agentRecord, &request)
	if err != nil {
		return nil, err
	}
	defer prepared.release()

	connection, machine, project := prepared.connection, prepared.machine, prepared.project
	wrappedCommand, sandbox := prepared.command, prepared.sandbox
	if err := engine.rememberExecutionSandbox(machine, sandbox, request.OutsideSandbox); err != nil {
		connection.Close()
		return nil, err
	}

	if err := engine.rememberMicroVM(agentRecord, machine, project, sandbox); err != nil {
		connection.Close()
		return nil, err
	}

	displayCommand := request.DisplayCommand
	if displayCommand == "" {
		displayCommand = command
	}

	running, err := engine.startJob(agentRecord, machine, project, connection, displayCommand, wrappedCommand, request, sandbox)
	if err != nil {
		connection.Close()
		return nil, err
	}

	var result any

	if request.Background {
		result = map[string]any{
			"jobId":     running.job.ID,
			"machineId": machine.ID,
			"status":    "running",
			"sandbox":   sandbox,
		}
	} else {
		result, err = waitForForegroundFor(ctx, running, sandbox, request.YieldTime)
	}

	if err != nil {
		return nil, err
	}

	running.mu.Lock()
	job := running.job
	running.mu.Unlock()
	arguments := map[string]any{}

	if request.OutputBytes > 0 {
		arguments["max_output_bytes"] = float64(request.OutputBytes)
	}

	read, err := engine.readJobOutput(job, arguments)
	if err != nil {
		return nil, err
	}

	response := result.(map[string]any)
	response["nextCursor"], response["hasMore"], response["omittedBytes"] = read.NextCursor, read.HasMore, read.OmittedBytes

	if read.TailCursor != "" {
		response["tailCursor"] = read.TailCursor
	}

	if _, exists := response["job"]; exists {
		response["job"] = read.Job
	} else {
		response["output"] = read.Output
	}

	if request.Interactive {
		response["interactive"] = true
		response["tty"] = request.TTY
	}

	return response, nil
}

func (engine *Engine) startJob(
	agentRecord domain.Agent,
	machine domain.Machine,
	project domain.Project,
	connection *remote.Connection,
	command, wrappedCommand string,
	request executionRequest,
	sandbox string,
) (*runningJob, error) {
	jobContext, cancel := context.WithCancel(engine.ctx)
	running := &runningJob{
		yielded:    request.Background,
		cancel:     cancel,
		done:       make(chan struct{}),
		output:     &remote.OutputBuffer{},
		changed:    make(chan struct{}),
		execution:  request,
		sandbox:    sandbox,
		connection: connection,
		project:    project,
		job: domain.Job{
			ID:        NewID(),
			AgentID:   agentRecord.ID,
			MachineID: machine.ID,
			Command:   command,

			Status:    "running",
			StartedAt: time.Now().UTC(),
		},
	}

	input := request.Input

	if request.Interactive {
		running.input = newJobInput()
		input = running.input
		context.AfterFunc(jobContext, running.input.stop)
	}

	if err := engine.Store.Save("job", running.job.ID, agentRecord.ID, running.job); err != nil {
		cancel()
		return nil, err
	}

	engine.mu.Lock()
	engine.jobs[running.job.ID] = running
	engine.workers.Add(1)
	engine.mu.Unlock()
	owner := request.leaseOwner
	if owner == "" {
		owner = agentRecord.ID
	}

	engine.Remote.Hold(owner, running.job.ID)
	engine.Emit("job", agentRecord.ID, running.job)
	go engine.runJob(jobContext, running, machine, connection, wrappedCommand, input)

	return running, nil
}

func waitForForeground(ctx context.Context, running *runningJob, sandbox string) (any, error) {
	return waitForForegroundFor(ctx, running, sandbox, foregroundCommandLimit)
}

func waitForForegroundFor(
	ctx context.Context,
	running *runningJob,
	sandbox string,
	yield time.Duration,
) (any, error) {
	if yield <= 0 {
		yield = foregroundCommandLimit
	}

	deadline := time.NewTimer(yield)
	defer deadline.Stop()

	select {
	case <-running.done:
		running.mu.Lock()
		result := running.job
		running.mu.Unlock()

		return map[string]any{"job": result, "sandbox": sandbox}, nil
	case <-deadline.C:
		running.mu.Lock()
		running.yielded = running.job.Status == "running"
		running.mu.Unlock()

		return map[string]any{
			"jobId":     running.job.ID,
			"machineId": running.job.MachineID,
			"status":    "running",
			"sandbox":   sandbox,
		}, nil
	case <-ctx.Done():
		running.cancel()
		return nil, ctx.Err()
	}
}

func (engine *Engine) runJob(
	ctx context.Context,
	running *runningJob,
	machine domain.Machine,
	connection *remote.Connection,
	command string,
	input io.Reader,
) {
	defer engine.workers.Done()
	defer running.cancel()
	defer connection.Close()
	defer engine.Remote.Release("job:" + running.job.ID)

	monitorCompleted := make(chan struct{})
	go func() {
		defer close(monitorCompleted)

		engine.monitorOccupancy(ctx, running, machine, connection)
	}()
	lastPublished := time.Time{}
	output, runError := remote.RunStreamingInput(ctx, connection.Client, command, input, running.input != nil, func(output string) {
		running.mu.Lock()
		running.job.Output = output
		publish := time.Since(lastPublished) >= 500*time.Millisecond
		snapshot := running.job
		var window remote.OutputWindow

		if publish {
			lastPublished = time.Now()
			window = running.output.Window()
		}

		running.mu.Unlock()

		if publish {
			_ = engine.Store.Save("job", snapshot.ID, snapshot.AgentID, snapshot)
			_ = engine.Store.Save(
				"job_output",
				snapshot.ID,
				snapshot.AgentID,
				jobOutputCheckpoint{
					ID:      snapshot.ID,
					AgentID: snapshot.AgentID,
					Window:  window,
				},
			)

			engine.Emit("job", snapshot.AgentID, snapshot)
		}
	}, func(chunk []byte) {
		running.mu.Lock()
		_, _ = running.output.Write(chunk)
		close(running.changed)
		running.changed = make(chan struct{})
		running.mu.Unlock()
	})

	commandCancelled := ctx.Err() != nil
	running.cancel()
	<-monitorCompleted
	running.mu.Lock()
	running.job.Output = output
	running.job.Status = "completed"

	if runError != nil {
		running.job.Status = "failed"
		running.job.Error = runError.Error()
		var exitStatus *ssh.ExitError
		connectionLost := !commandCancelled && !errors.As(runError, &exitStatus)
		if connectionLost && running.terminationReason == "" {
			running.terminationReason = "remote command connection was lost"
		}

		if running.terminationReason != "" {
			running.job.Status = "interrupted"
			running.job.Error = running.terminationReason
		} else if errors.Is(runError, context.Canceled) {
			running.job.Status = "stopped"
		}
	}

	final := running.job
	window := running.output.Window()
	yielded := running.yielded
	running.mu.Unlock()
	_ = engine.Store.Save(
		"job_output",
		final.ID,
		final.AgentID,
		jobOutputCheckpoint{
			ID:      final.ID,
			AgentID: final.AgentID,
			Window:  window,
		},
	)

	_ = engine.Store.Save("job", final.ID, final.AgentID, final)

	if yielded {
		completionError, _ := outputPreview(final.Error, 500)
		_ = engine.Store.Save("job_completion", final.ID, final.AgentID, map[string]any{
			"id":         final.ID,
			"agentId":    final.AgentID,
			"status":     final.Status,
			"error":      completionError,
			"finishedAt": time.Now().UTC(),
		})
	}

	engine.Emit("job", final.AgentID, final)
	close(running.done)
	engine.mu.Lock()
	delete(engine.jobs, final.ID)
	engine.mu.Unlock()
	engine.Remote.Release("job:" + final.ID)
	engine.scheduleVMIdleCheck(final.AgentID)

	if final.Status == "interrupted" {
		notice := fmt.Sprintf(
			"Harness event: job %s was terminated on machine %s: %s. The process was not migrated and the command was not replayed. Select an available machine and, if the task should continue, restart the process yourself using checkpoints in the shared or stable project directory. Inspect job logs before deciding how to resume.",
			final.ID,
			final.MachineID,
			final.Error,
		)

		_ = engine.NotifyAgent(final.AgentID, notice)
	}

	engine.cleanupVenvsIfIdle(final.AgentID)
}

func (engine *Engine) monitorOccupancy(
	ctx context.Context,
	running *runningJob,
	machine domain.Machine,
	connection *remote.Connection,
) {
	if !machine.SingleUser {
		return
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			probeContext, cancel := context.WithTimeout(ctx, 4*time.Second)
			err := remote.CheckAvailability(probeContext, connection, machine)
			cancel()

			if err != nil {
				running.mu.Lock()
				running.terminationReason = "machine became unavailable or another user logged in"
				running.mu.Unlock()
				engine.Emit("failover", running.job.AgentID, map[string]string{
					"unavailableMachine": machine.ID,
				})
				running.cancel()

				return
			}
		}
	}
}

func (engine *Engine) jobTool(
	ctx context.Context,
	agentRecord domain.Agent,
	action, jobID string,
) (any, error) {
	return engine.jobToolArguments(ctx, agentRecord, action, map[string]any{"id": jobID})
}

func (engine *Engine) jobToolArguments(
	ctx context.Context,
	agentRecord domain.Agent,
	action string,
	arguments map[string]any,
) (any, error) {
	jobID := stringArg(arguments, "id")
	var job domain.Job
	if err := engine.Store.Load("job", jobID, &job); err != nil {
		return nil, err
	}

	if job.AgentID != agentRecord.ID {
		return nil, errors.New("job belongs to another agent")
	}

	engine.mu.Lock()
	running := engine.jobs[jobID]
	wakeChannel := engine.wake[agentRecord.ID]
	engine.mu.Unlock()

	if action == "job_stop" {
		if running != nil {
			running.cancel()
		}

		return true, nil
	}

	if action == "job_wait" && running != nil {
		wait, err := outputInteger(arguments, "wait_time_ms", 30000, 1, 30000)
		if err != nil {
			return nil, err
		}

		timer := time.NewTimer(time.Duration(wait) * time.Millisecond)
		defer timer.Stop()

		select {
		case <-running.done:
		case <-timer.C:
		case <-wakeChannel:
			return map[string]string{"status": "interrupted", "reason": "new user instruction"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if running != nil {
		running.mu.Lock()
		job = running.job
		running.mu.Unlock()
	}

	if action == "job_status" {
		job.Output = ""
		return job, nil
	}

	return engine.readJobOutput(job, arguments)
}

func (engine *Engine) cleanupVenvsIfIdle(agentID string) {
	engine.mu.Lock()
	busy := engine.running[agentID] != nil

	for _, job := range engine.jobs {
		job.mu.Lock()
		busy = busy || (job.job.AgentID == agentID && job.job.Status == "running")
		job.mu.Unlock()
	}

	engine.mu.Unlock()

	if !busy {
		engine.cleanupVenvs(agentID)
	}
}

// StopJob is the single-user application's explicit user control. Agent tool
// calls use jobTool instead, which additionally checks the requesting owner.
func (engine *Engine) StopJob(jobID string) error {
	var job domain.Job
	if err := engine.Store.Load("job", jobID, &job); err != nil {
		return err
	}

	_, err := engine.jobTool(engine.ctx, domain.Agent{ID: job.AgentID}, "job_stop", jobID)

	return err
}
