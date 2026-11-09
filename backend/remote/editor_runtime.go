package remote

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/vscode"
)

// EditorTarget keeps terminals and workspace extensions in the selected agent's
// environment. Projects without agents use their own stable editor VM scope.
type EditorTarget struct {
	AgentID            string
	MachineID          string
	CommitIdentity     domain.CommitIdentity
	TemporaryDirectory string
}

func EditorScopeID(projectID, agentID string) string {
	if agentID != "" {
		return agentID
	}
	digest := sha256.Sum256([]byte(projectID))
	return "editor-" + hex.EncodeToString(digest[:16])
}

type editorRuntime struct {
	command string
	address string
	token   string
	sandbox string
}

func prepareEditorRuntime(ctx context.Context, connection *Connection, machine domain.Machine, project domain.Project, target EditorTarget, prefix string) (editorRuntime, error) {
	level, err := sandboxLevel(ctx, connection, machine)
	if err != nil {
		return editorRuntime{}, err
	}
	command := "python3 -u -I -c " + Quote(editorSupervisor) + " " + Quote(prefix) + " " + Quote(project.Directory) + " " + Quote(vscode.RemoteManifest())
	if level != SandboxMicroVM {
		return editorRuntime{command: WithCommitIdentity(command, target.CommitIdentity), sandbox: SandboxDisabled}, nil
	}
	if machine.MicroVMCommand != "" {
		return editorRuntime{}, errors.New("VS Code inside a MicroVM requires the managed Fedora runner; this custom runner does not expose an editor tunnel")
	}
	scope := EditorScopeID(project.ID, target.AgentID)
	temporary := target.TemporaryDirectory
	if temporary == "" {
		temporary = "/tmp/zentrale-" + scope
	}
	execution := Execution{AgentID: scope, ProjectID: project.ID, Workspace: project.Directory, TemporaryDirectory: temporary, Network: true}
	if _, err := Run(ctx, connection.Client, "mkdir -p -- "+Quote(execution.Workspace)+" "+Quote(execution.TemporaryDirectory)+" && chmod 700 -- "+Quote(execution.TemporaryDirectory)); err != nil {
		return editorRuntime{}, err
	}
	proof, err := ManageMicroVM(ctx, connection, machine, "prepare", execution)
	if err != nil {
		return editorRuntime{}, err
	}
	port, valid := proof["editorPort"].(float64)
	if !valid || port < 1 || port > 65535 || port != float64(int(port)) {
		return editorRuntime{}, errors.New("managed MicroVM did not expose its editor tunnel")
	}
	// These packages are installed in Fedora, never on the SSH host. They persist
	// alongside the agent's other packages and survive editor restarts.
	bootstrap := execution
	bootstrap.GuestRoot = true
	bootstrap.Command = `if ! command -v python3 >/dev/null 2>&1 || ! command -v git >/dev/null 2>&1 || ! test -f /usr/lib64/libstdc++.so.6; then
  if command -v microdnf >/dev/null 2>&1; then manager=microdnf
  elif command -v dnf5 >/dev/null 2>&1; then manager=dnf5
  else manager=dnf; fi
  "$manager" -y install python3 git libstdc++ libatomic
fi`
	wrapped, _, err := Sandbox(ctx, connection, machine, bootstrap)
	if err != nil {
		return editorRuntime{}, err
	}
	if _, err := Run(ctx, connection.Client, wrapped); err != nil {
		return editorRuntime{}, fmt.Errorf("prepare Fedora editor packages: %w", err)
	}
	token := rand.Text() + rand.Text()
	configuration, _ := json.Marshal(map[string]string{"token": token})
	execution.Command = WithCommitIdentity(command+" "+Quote(string(configuration)), target.CommitIdentity)
	wrapped, _, err = Sandbox(ctx, connection, machine, execution)
	return editorRuntime{command: wrapped, address: net.JoinHostPort("127.0.0.1", fmt.Sprint(int(port))), token: token, sandbox: SandboxMicroVM}, err
}

func dialEditor(ctx context.Context, session *editorSession) (net.Conn, error) {
	if session.runtime.address == "" {
		return session.connection.Client.DialContext(ctx, "unix", session.socketPath)
	}
	connection, err := session.connection.Client.DialContext(ctx, "tcp", session.runtime.address)
	if err != nil {
		return nil, err
	}
	// SSH stream channels do not implement socket deadlines. Closing this one
	// stream cancels a stuck authentication without breaking other editor streams.
	authentication, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stop := context.AfterFunc(authentication, func() { _ = connection.Close() })
	defer stop()
	if _, err := io.Copy(connection, strings.NewReader(session.runtime.token+"\n")); err != nil {
		connection.Close()
		return nil, err
	}
	var acknowledgement [3]byte
	if _, err := io.ReadFull(connection, acknowledgement[:]); err != nil {
		connection.Close()
		return nil, err
	}
	if string(acknowledgement[:]) != "OK\n" || authentication.Err() != nil {
		connection.Close()
		return nil, errors.New("MicroVM editor tunnel authentication failed")
	}
	return connection, nil
}
