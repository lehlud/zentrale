package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func jobStopWrite(job domain.Job) datastore.ResourceWrite {
	return datastore.ResourceWrite{Kind: "job_stop", ID: job.ID, Value: map[string]string{"id": job.ID, "machineId": job.MachineID, "targetAgentId": job.AgentID}}
}

func (engine *Engine) jobStopRequested(job domain.Job) bool {
	var stop map[string]any
	if engine.Store.Load("job_stop", job.ID, &stop) == nil {
		return true
	}
	var agentStop agentStopRecord
	return engine.Store.Load("agent_stop", job.AgentID, &agentStop) == nil && agentStop.Requested
}

func (engine *Engine) confirmDaemonStop(ctx context.Context, id, machineID string, connection *remote.Connection) error {
	data, err := engine.Store.Credential(daemonReceiptName(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} // Admission was never persisted.
	if err != nil {
		return err
	}
	var receipt daemonJobReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	if connection == nil {
		connection, err = engine.Remote.Connector.Dial(ctx, machineID)
		if err != nil {
			return err
		}
		defer connection.Close()
	}
	return remote.StopInvocation(ctx, connection, id, receipt.Generation)
}

// Stop intent survives a service restart and an unavailable remote machine.
// This outbox contains resource identities, never a shell command to replay.
func (engine *Engine) superviseDaemonStops() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		records, err := engine.Store.List("job_stop", nil, 500, 0)
		if err == nil {
			var workers sync.WaitGroup
			slots := make(chan struct{}, 8)
			for _, record := range records {
				id, machine := stringArg(record, "id"), stringArg(record, "machineId")
				select {
				case slots <- struct{}{}:
				case <-engine.ctx.Done():
					workers.Wait()
					return
				}
				workers.Add(1)
				go func() {
					defer workers.Done()
					defer func() { <-slots }()
					ctx, cancel := context.WithTimeout(engine.ctx, 10*time.Second)
					err := engine.confirmDaemonStop(ctx, id, machine, nil)
					cancel()
					if err == nil {
						_ = engine.Store.Patch("job", id, map[string]any{"status": "stopped", "error": ""})
						_ = engine.Store.DeleteCredential(daemonReceiptName(id))
						_ = engine.Store.DeleteCredential("job-input/" + id)
						_ = engine.Store.Remove("job_stop", id)
					}
				}()
			}
			workers.Wait()
		}
		select {
		case <-engine.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
