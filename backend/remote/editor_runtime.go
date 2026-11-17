package remote

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
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
	RuntimeLock        RuntimeLock
	Recovery           VMRecovery
	// Harness-only selection from cold preparation or a fully validated
	// readiness receipt. It is never accepted from a browser/model argument.
	PreparedSandbox string
}

// RuntimeLock serializes guest startup with automatic stop and removal.
type RuntimeLock interface {
	Lock(context.Context) error
	Unlock()
}

func EditorScopeID(projectID, _ string) string { return "project-" + projectID }

type editorRuntime struct {
	command string
	address string
	token   string
	sandbox string
}

func prepareEditorRuntime(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
	project domain.Project,
	target EditorTarget,
	prefix string,
) (editorRuntime, error) {
	runtime, err := prepareEditorRuntimeOnce(ctx, connection, machine, project, target, prefix)
	var recovery *VMRecoveryRequired
	if !errors.As(err, &recovery) || target.Recovery == nil {
		return runtime, err
	}
	temporary := target.TemporaryDirectory
	if temporary == "" {
		temporary = "/tmp/zentrale-" + EditorScopeID(project.ID, target.AgentID)
	}
	execution := Execution{
		AgentID: EditorScopeID(project.ID, target.AgentID), ProjectID: project.ID,
		Workspace: project.Directory, TemporaryDirectory: temporary,
		EditorSessionID: prefix[strings.LastIndex(prefix, "/")+1:],
	}
	if err := target.Recovery.RecoverVM(ctx, connection, machine, execution, recovery.Reason); err != nil {
		return editorRuntime{}, err
	}
	return prepareEditorRuntimeOnce(ctx, connection, machine, project, target, prefix)
}

func prepareEditorRuntimeOnce(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
	project domain.Project,
	target EditorTarget,
	prefix string,
) (editorRuntime, error) {
	level, err := sandboxLevel(ctx, connection, machine)
	if err != nil {
		return editorRuntime{}, err
	}

	paths := PathsForExecution(machine, level, project.Directory, "")
	directory := paths.Workspace
	command := "python3 -u -I -c " + Quote(editorSupervisor) +
		" " + Quote(prefix) + " " + Quote(directory) +
		" " + Quote(vscode.RemoteManifest())

	if level != SandboxMicroVM {
		return editorRuntime{
			command: WithCommitIdentity(command, target.CommitIdentity),
			sandbox: SandboxDisabled,
		}, nil
	}

	if machine.MicroVMCommand != "" {
		return editorRuntime{}, errors.New("VS Code inside a MicroVM requires the managed Fedora runner; this custom runner does not expose an editor tunnel")
	}

	scope := EditorScopeID(project.ID, target.AgentID)
	temporary := target.TemporaryDirectory
	if temporary == "" {
		temporary = "/tmp/zentrale-" + scope
	}

	execution := Execution{
		AgentID:            scope,
		ProjectID:          project.ID,
		Workspace:          project.Directory,
		TemporaryDirectory: temporary,
		Network:            true,
	}
	if _, err := Run(
		ctx,
		connection,
		"mkdir -p -- "+Quote(execution.Workspace)+" "+Quote(execution.TemporaryDirectory)+" && chmod 700 -- "+Quote(execution.TemporaryDirectory),
	); err != nil {
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

	token := rand.Text() + rand.Text()
	configuration, _ := json.Marshal(map[string]string{"token": token})
	execution.Command = WithCommitIdentity(command+" "+Quote(string(configuration)), target.CommitIdentity)
	wrapped, _, err := Sandbox(ctx, connection, machine, execution)

	return editorRuntime{
		command: wrapped,
		address: net.JoinHostPort("127.0.0.1", fmt.Sprint(int(port))),
		token:   token,
		sandbox: SandboxMicroVM,
	}, err
}

func dialEditorPort(ctx context.Context, session *editorSession, port int) (net.Conn, error) {
	if session.runtime.address == "" {
		if port != 0 {
			connection, err := session.connection.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err == nil || ctx.Err() != nil {
				return connection, err
			}
			return session.connection.DialContext(ctx, "tcp", net.JoinHostPort("::1", strconv.Itoa(port)))
		}

		return session.connection.DialContext(ctx, "unix", session.socketPath)
	}

	connection, err := session.connection.DialContext(ctx, "tcp", session.runtime.address)
	if err != nil {
		return nil, err
	}

	// SSH stream channels do not implement socket deadlines. Closing this one
	// stream cancels a stuck authentication without breaking other editor streams.
	authentication, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	stop := context.AfterFunc(authentication, func() { _ = connection.Close() })
	defer stop()

	authenticationLine := session.runtime.token
	if port != 0 {
		authenticationLine += " " + strconv.Itoa(port)
	}

	if _, err := io.Copy(connection, strings.NewReader(authenticationLine+"\n")); err != nil {
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
