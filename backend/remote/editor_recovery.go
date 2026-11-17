package remote

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
)

type editorReceipt struct {
	CommitIdentity                                       domain.CommitIdentity `json:"commitIdentity"`
	Project                                              domain.Project        `json:"project"`
	Info                                                 domain.EditorSession  `json:"info"`
	Generation                                           string                `json:"generation"`
	Command, Address, Token, Sandbox, TemporaryDirectory string
}

func editorReceiptName(id string) string                  { return "editor-runtime/" + id }
func editorSessionKey(projectID, machineID string) string { return projectID + ":" + machineID }
func sameEditorTarget(left, right EditorTarget) bool {
	return left.MachineID == right.MachineID && left.CommitIdentity == right.CommitIdentity && left.TemporaryDirectory == right.TemporaryDirectory && left.PreparedSandbox == right.PreparedSandbox
}

func (editors *Editors) durableEditorContext(ctx context.Context, session *editorSession, connection *Connection, attach bool) context.Context {
	if !connection.DaemonEnabled() {
		return ctx
	}
	invocation := Invocation{ID: "editor-" + session.info.ID, AttachOnly: attach, DetachOnCancel: func() bool { return editors.ctx.Err() != nil }}
	if attach {
		invocation.Generation = session.restored.Generation
	} else {
		invocation.Persist = func(generation string) error {
			receipt := editorReceipt{Project: session.project, Info: session.info, Generation: generation, Command: session.runtime.command, Address: session.runtime.address, Token: session.runtime.token, Sandbox: session.runtime.sandbox, TemporaryDirectory: session.target.TemporaryDirectory, CommitIdentity: session.target.CommitIdentity}
			data, err := json.Marshal(receipt)
			if err != nil {
				return err
			}
			return editors.remote.Connector.Store.SaveCredentialResource(editorReceiptName(session.info.ID), data, "editor_attachment", session.info.ID, session.project.ID, map[string]string{"id": session.info.ID, "projectId": session.project.ID, "machineId": session.info.MachineID})
		}
	}
	return WithInvocation(ctx, invocation)
}

// Restore retains workbench URLs and private server tokens across service/host
// restarts. Existing remote processes are attached, never staged or spawned.
func (editors *Editors) Restore() error {
	for offset := 0; ; offset += 500 {
		records, err := editors.remote.Connector.Store.List("editor_attachment", nil, 500, offset)
		if err != nil {
			return err
		}
		for _, record := range records {
			id, _ := record["id"].(string)
			data, err := editors.remote.Connector.Store.Credential(editorReceiptName(id))
			if err != nil {
				return err
			}
			var receipt editorReceipt
			if err := json.Unmarshal(data, &receipt); err != nil {
				return err
			}
			var project domain.Project
			if err := editors.remote.Connector.Store.Load("project", receipt.Project.ID, &project); err != nil {
				continue
			}
			if !sameEditorProject(receipt.Project, project) {
				continue
			}
			if err := editors.remote.RestoreUsage(project, "ide:"+receipt.Info.ID, receipt.Info.MachineID, false); err != nil {
				return err
			}
			ctx, cancel := context.WithCancel(editors.ctx)
			session := &editorSession{project: receipt.Project, info: receipt.Info, restored: &receipt, target: EditorTarget{AgentID: receipt.Info.AgentID, MachineID: receipt.Info.MachineID, TemporaryDirectory: receipt.TemporaryDirectory, PreparedSandbox: receipt.Sandbox, CommitIdentity: receipt.CommitIdentity}, runtime: editorRuntime{command: receipt.Command, address: receipt.Address, token: receipt.Token, sandbox: receipt.Sandbox}, ready: make(chan struct{}), done: make(chan struct{}), cancel: cancel, lastUsed: time.Now()}
			editors.mutex.Lock()
			editors.sessions[editorSessionKey(project.ID, receipt.Info.MachineID)] = session
			editors.workers.Add(1)
			editors.mutex.Unlock()
			go func() { defer editors.workers.Done(); editors.run(ctx, session) }()
		}
		if len(records) < 500 {
			return nil
		}
	}
}

func (editors *Editors) runRestored(ctx context.Context, session *editorSession, ready func(error)) {
	connection, err := editors.remote.Connector.Reattach(ctx, session.info.MachineID)
	if err != nil {
		ready(err)
		return
	}
	defer connection.Close()
	session.connection = connection
	ctx = editors.durableEditorContext(ctx, session, connection, true)
	err = runEditorProcess(ctx, connection, "", func(output string) {
		select {
		case <-session.ready:
			return
		default:
		}
		if socket := editorOutputSocket(output); socket != "" {
			session.socketPath = socket
			ready(nil)
		}
	})
	if err == nil {
		err = errors.New("restored editor process exited")
	}
	ready(err)
}
