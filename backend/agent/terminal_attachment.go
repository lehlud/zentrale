package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type TerminalAttachment struct {
	ID               string
	OutputOffset     int64
	KeepOnDisconnect func() bool
}

type terminalReceipt struct {
	ID, ProjectID, AgentID, MachineID, Generation string
	LastSequence                                  uint64
	PendingSequence                               uint64
	PendingInput                                  *remote.PendingInput
}

func terminalReceiptName(id string) string { return "terminal-runtime/" + id }

// TerminalAttached identifies a shell independently of either browser or SSH
// connections. Browser cursors name bytes already received, never guessed bytes
// sent by a previous server process. Reviewed stdin is committed before ACK.
func (engine *Engine) TerminalAttached(ctx context.Context, projectID, agentID string, attachment TerminalAttachment, cols, rows int, use func(context.Context, *remote.Terminal) error) error {
	if attachment.ID == "" || engine.Remote.Connector.NativeSessionsForTests {
		return engine.Terminal(ctx, projectID, agentID, cols, rows, use)
	}
	if !remote.ValidID(attachment.ID) || attachment.OutputOffset < 0 {
		return errors.New("invalid terminal attachment or output cursor")
	}
	unlock, err := engine.terminalGates.LockContext(ctx, attachment.ID)
	if err != nil {
		return err
	}
	defer unlock()
	var receipt terminalReceipt
	data, err := engine.Store.Credential(terminalReceiptName(attachment.ID))
	reattaching := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if reattaching {
		if err := json.Unmarshal(data, &receipt); err != nil {
			return err
		}
		if receipt.ProjectID != projectID || receipt.AgentID != agentID {
			return errors.New("terminal attachment belongs to another environment")
		}
	} else {
		if attachment.OutputOffset != 0 {
			return errors.New("terminal attachment is unknown; shell was not restarted")
		}
		receipt = terminalReceipt{ID: attachment.ID, ProjectID: projectID, AgentID: agentID}
	}
	saveReceipt := func() error {
		payload, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		return engine.Store.SetCredential(terminalReceiptName(attachment.ID), payload)
	}
	keep := func() bool { return attachment.KeepOnDisconnect != nil && attachment.KeepOnDisconnect() }
	refresh := func() error {
		return engine.Store.Save("terminal_attachment", attachment.ID, projectID, map[string]any{"id": attachment.ID, "projectId": projectID, "machineId": receipt.MachineID, "expiresAt": time.Now().UTC().Add(15 * time.Minute)})
	}
	run := func(ctx context.Context, connection *remote.Connection, machine domain.Machine, project domain.Project, target remote.EditorTarget) error {
		receipt.MachineID = machine.ID
		invocation := remote.Invocation{ID: "terminal-" + attachment.ID, Generation: receipt.Generation, AttachOnly: reattaching, IdleTimeout: 15 * time.Minute, DetachOnCancel: keep, ReplayInput: receipt.PendingInput}
		if reattaching {
			invocation.OutputOffset = attachment.OutputOffset + int64(len("\x00ZENTRALE_TERMINAL_READY\n"))
		}
		invocation.Persist = func(generation string) error {
			receipt.Generation = generation
			if err := saveReceipt(); err != nil {
				return err
			}
			return refresh()
		}
		invocation.BeforeInput = func(offset int64, data []byte, eof bool) error {
			receipt.PendingInput = &remote.PendingInput{Offset: offset, Data: data, EOF: eof}
			return saveReceipt()
		}
		invocation.AfterInput = func() error {
			if receipt.PendingSequence > 0 {
				receipt.LastSequence = receipt.PendingSequence
			}
			receipt.PendingSequence = 0
			receipt.PendingInput = nil
			return saveReceipt()
		}
		ctx = remote.WithInvocation(ctx, invocation)
		if target.RuntimeLock != nil {
			if err := target.RuntimeLock.Lock(ctx); err != nil {
				return err
			}
		}
		terminal, err := remote.StartTerminal(ctx, connection, machine, project, target, cols, rows)
		if target.RuntimeLock != nil {
			target.RuntimeLock.Unlock()
		}
		if err != nil {
			return err
		}
		defer terminal.Close()
		terminal.ConfirmInput = func(sequence uint64, write func() error) error {
			if sequence == 0 {
				return errors.New("terminal input requires a sequence number")
			}
			if sequence <= receipt.LastSequence {
				return nil
			}
			if sequence != receipt.LastSequence+1 {
				return errors.New("terminal input sequence has a gap")
			}
			receipt.PendingSequence = sequence
			return write()
		}
		leaseContext, cancel := context.WithCancel(ctx)
		leaseDone := make(chan struct{})
		go func() {
			defer close(leaseDone)
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-leaseContext.Done():
					return
				case <-ticker.C:
					_ = refresh()
				}
			}
		}()
		defer func() { cancel(); <-leaseDone }()
		return use(ctx, terminal)
	}
	if reattaching {
		var project domain.Project
		if err := engine.Store.Load("project", projectID, &project); err != nil {
			return err
		}
		if err := engine.projectEnvironmentAvailable(projectID); err != nil {
			return err
		}
		release := engine.useEnvironment(remote.EditorScopeID(projectID, ""))
		defer release()
		machine, err := engine.Remote.Connector.Machine(receipt.MachineID)
		if err != nil {
			return err
		}
		owner := "terminal:" + attachment.ID
		if err := engine.Remote.RestoreUsage(project, owner, machine.ID, false); err != nil {
			return err
		}
		defer engine.Remote.Release(owner)
		connection, err := engine.Remote.Connector.Reattach(ctx, machine.ID)
		if err != nil {
			return err
		}
		defer connection.Close()
		err = run(ctx, connection, machine, project, remote.EditorTarget{AgentID: agentID, MachineID: machine.ID})
	} else {
		_, err = engine.withEditorEnvironment(ctx, projectID, agentID, func(ctx context.Context, connection *remote.Connection, machine domain.Machine, project domain.Project, target remote.EditorTarget) (domain.EditorSession, error) {
			return domain.EditorSession{}, run(ctx, connection, machine, project, target)
		})
	}
	if !keep() || err == nil {
		_ = engine.Store.Remove("terminal_attachment", attachment.ID)
		_ = engine.Store.DeleteCredential(terminalReceiptName(attachment.ID))
	} else {
		_ = refresh()
	}
	return err
}
