package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type daemonJobReceipt struct {
	Generation      string           `json:"generation"`
	Sandbox         string           `json:"sandbox"`
	Execution       executionRequest `json:"execution"`
	LaunchDirectory string           `json:"launchDirectory"`
}

func daemonReceiptName(id string) string { return "job-runtime/" + id }

func (engine *Engine) daemonJobContext(ctx context.Context, running *runningJob) context.Context {
	jobIdentity := domain.Job{ID: running.job.ID, AgentID: running.job.AgentID}
	invocation := remote.Invocation{
		ID: running.job.ID, Timeout: time.Until(running.job.StartedAt.Add(time.Duration(running.job.TimeoutMS) * time.Millisecond)),
		DetachOnCancel: func() bool { return engine.ctx.Err() != nil && !engine.jobStopRequested(jobIdentity) },
	}
	if running.daemonReceipt != nil {
		invocation.Generation = running.daemonReceipt.Generation
		invocation.AttachOnly = true
	} else {
		invocation.Persist = func(generation string) error {
			// Reader/function fields cannot be serialized. The original approved
			// command/privileges remain available for reviewing future stdin.
			execution := running.execution
			execution.Input, execution.OpenInput = nil, nil
			receipt := daemonJobReceipt{Generation: generation, Sandbox: running.sandbox, Execution: execution, LaunchDirectory: running.launchDirectory}
			data, err := json.Marshal(receipt)
			if err != nil {
				return err
			}
			return engine.Store.SetCredential(daemonReceiptName(running.job.ID), data)
		}
	}
	if running.input != nil {
		name := "job-input/" + running.job.ID
		invocation.BeforeInput = func(offset int64, data []byte, eof bool) error {
			payload, err := json.Marshal(remote.PendingInput{Offset: offset, Data: data, EOF: eof})
			if err != nil {
				return err
			}
			return engine.Store.SetCredential(name, payload)
		}
		invocation.AfterInput = func() error { return engine.Store.DeleteCredential(name) }
		if data, err := engine.Store.Credential(name); err == nil {
			var pending remote.PendingInput
			if json.Unmarshal(data, &pending) == nil {
				invocation.ReplayInput = &pending
			}
		}
		invocation.InputReady = func(write func(context.Context, []byte, bool) error) {
			running.mu.Lock()
			running.daemonInput = write
			close(running.inputReady)
			running.mu.Unlock()
		}
	}
	return remote.WithInvocation(ctx, invocation)
}

// Register every attachment before resuming agent turns or idle VM cleanup.
// Reconciliation contacts the original host and never replays shell commands.
func (engine *Engine) recoverDaemonJobs() error {
	records, err := engine.listAll("job", nil)
	if err != nil {
		return err
	}
	for _, record := range records {
		if stringArg(record, "status") != "running" {
			continue
		}
		var job domain.Job
		if err := engine.Store.Load("job", stringArg(record, "id"), &job); err != nil {
			return err
		}
		if engine.jobStopRequested(job) {
			if err := engine.Store.SaveResources([]datastore.ResourceWrite{jobStopWrite(job)}); err != nil {
				return err
			}
			if err := engine.Store.Patch("job", job.ID, map[string]any{"status": "stopping"}); err != nil {
				return err
			}
			continue
		}
		data, err := engine.Store.Credential(daemonReceiptName(job.ID))
		if err != nil {
			return err
		}
		var receipt daemonJobReceipt
		if err := json.Unmarshal(data, &receipt); err != nil {
			return err
		}
		var project domain.Project
		var agent domain.Agent
		if engine.Store.Load("agent", job.AgentID, &agent) == nil && engine.Store.Load("project", agent.ProjectID, &project) == nil {
			if err := engine.Remote.RestoreUsage(project, "job:"+job.ID, job.MachineID, receipt.Execution.GPU); err != nil {
				return err
			}
		}
		deadline := job.StartedAt.Add(time.Duration(job.TimeoutMS) * time.Millisecond)
		ctx, cancel := context.WithDeadline(engine.ctx, deadline)
		running := &runningJob{job: job, cancel: cancel, done: make(chan struct{}), changed: make(chan struct{}), output: &remote.OutputBuffer{}, execution: receipt.Execution, sandbox: receipt.Sandbox, yielded: true, launchDirectory: receipt.LaunchDirectory, daemonReceipt: &receipt}
		var input io.Reader
		if receipt.Execution.Interactive {
			running.input = newJobInput()
			running.inputReady = make(chan struct{})
			input = running.input
			context.AfterFunc(ctx, running.input.stop)
		}
		engine.mu.Lock()
		engine.jobs[job.ID] = running
		engine.mu.Unlock()
		engine.workers.Add(1)
		go func() {
			machine, err := engine.Remote.Connector.Machine(job.MachineID)
			var connection *remote.Connection
			if err == nil {
				connection, err = engine.Remote.Connector.Reattach(ctx, job.MachineID)
			}
			if err != nil {
				cancel()
				running.mu.Lock()
				running.job.Status = "interrupted"
				running.job.Error = fmt.Sprintf("reattach original remote process: %v; command was not replayed", err)
				final := running.job
				running.mu.Unlock()
				_ = engine.Store.Save("job", job.ID, job.AgentID, final)
				close(running.done)
				engine.mu.Lock()
				delete(engine.jobs, job.ID)
				engine.mu.Unlock()
				engine.workers.Done()
				return
			}
			running.mu.Lock()
			running.connection = connection
			running.mu.Unlock()
			engine.runJob(ctx, running, machine, connection, "", input)
		}()
	}
	return nil
}
