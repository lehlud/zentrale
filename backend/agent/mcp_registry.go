package agent

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maximumMCPServers = 8

type mcpServerInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Command   string `json:"command,omitempty"`
	URL       string `json:"url,omitempty"`
	Network   bool   `json:"network"`
	MachineID string `json:"machineId"`
	Sandbox   string `json:"sandbox"`
}

type mcpServer struct {
	info            mcpServerInfo
	agentID         string
	scope           string
	ctx             context.Context
	cancel          context.CancelFunc
	connection      *remote.Connection
	client          *mcp.ClientSession
	closeOnce       sync.Once
	operation       chan struct{}
	tools           map[string]*mcp.Tool
	catalogRevision uint64
	changed         atomic.Uint64
}

func (server *mcpServer) close() {
	server.closeOnce.Do(func() {
		// Cancel remote I/O before waiting for the SDK's graceful shutdown.
		server.cancel()
		_ = server.client.Close()
		server.connection.Close()
	})
}

func (engine *Engine) mcpServersFor(agentID string) []mcpServerInfo {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	result := []mcpServerInfo{}
	for _, server := range engine.mcpServers {
		if server.agentID == agentID && server.ctx.Err() == nil {
			result = append(result, server.info)
		}
	}
	sort.Slice(result, func(a, b int) bool { return result[a].ID < result[b].ID })
	return result
}

func (engine *Engine) closeMCPServers(agentID string) {
	engine.mu.Lock()
	var selected []*mcpServer
	for id, server := range engine.mcpServers {
		if agentID == "" || server.agentID == agentID {
			selected = append(selected, server)
			delete(engine.mcpServers, id)
		}
	}
	engine.mu.Unlock()
	for _, server := range selected {
		server.close()
	}
}

// All transports are opened by the existing SSH/sandbox execution boundary.
// Loopback HTTP therefore means loopback inside this agent's sandbox. The MCP
// server never receives provider credentials or a connection to Zentrale's host.
func (engine *Engine) connectMCP(ctx context.Context, record domain.Agent, info mcpServerInfo) (_ any, failure error) {
	connection, machine, project, err := engine.connect(ctx, record)
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(engine.ctx)
	stopStartupCancellation := context.AfterFunc(ctx, cancel)
	defer stopStartupCancellation()
	server := &mcpServer{
		info: info, agentID: record.ID, scope: JSON(machine) + JSON(project),
		ctx: lifetime, cancel: cancel, connection: connection, operation: make(chan struct{}, 1),
	}
	defer func() {
		if failure != nil {
			cancel()
			if server.client != nil {
				_ = server.client.Close()
			}
			connection.Close()
		}
	}()
	identity, err := engine.ProjectCommitIdentity(project)
	if err != nil {
		return nil, err
	}
	temporary := agentTemporaryDirectory(record.ID)
	if err := prepareRemoteWorkspace(ctx, connection, project.Directory, temporary); err != nil {
		return nil, err
	}
	open := func(requestContext context.Context, command string, tooling bool) (*remote.Process, error) {
		requestContext, stop := context.WithCancel(requestContext)
		stopLifetime := context.AfterFunc(lifetime, stop)
		wrapped, sandbox, err := remote.Sandbox(requestContext, connection, machine, remote.Execution{
			AgentID: record.ID, ProjectID: project.ID, Workspace: project.Directory, TemporaryDirectory: temporary,
			Command: remote.WithCommitIdentity(command, identity), Network: info.Network,
			Tooling: tooling, ProtocolIO: true,
		})
		if err != nil {
			stopLifetime()
			stop()
			return nil, err
		}
		if err := engine.rememberExecutionSandbox(machine, sandbox, false); err != nil {
			stopLifetime()
			stop()
			return nil, err
		}
		if err := engine.rememberMicroVM(record, machine, project, sandbox); err != nil {
			stopLifetime()
			stop()
			return nil, err
		}
		process, err := remote.StartProcess(requestContext, connection.Client, wrapped)
		if err != nil {
			stopLifetime()
			stop()
			return nil, err
		}
		// Both cancellation hooks are detached when the process is closed.
		process.OnClose(func() { stopLifetime(); stop() })
		return process, nil
	}
	var transport mcp.Transport
	if info.Transport == "stdio" {
		process, err := open(lifetime, info.Command, false)
		if err != nil {
			return nil, err
		}
		defer func() {
			if failure != nil {
				_ = process.Close()
			}
		}()
		transport = &mcp.IOTransport{Reader: process, Writer: process, MaxLineLength: 1024 * 1024}
	} else {
		address, err := remote.ValidateMCPEndpoint(info.URL)
		if err != nil {
			return nil, err
		}
		httpClient := &http.Client{
			Transport: &remote.SandboxHTTPTransport{Endpoint: address, Open: func(ctx context.Context, command string) (*remote.Process, error) {
				// Trusted relay code uses the VM's tooling Python. A minimal Fedora
				// guest does not need Python installed just to expose an HTTP MCP.
				return open(ctx, command, true)
			}},
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("MCP endpoint redirects are disabled") },
		}
		if info.Transport == "sse" {
			transport = &mcp.SSEClientTransport{Endpoint: info.URL, HTTPClient: httpClient, MaxEventSize: 1024 * 1024}
		} else {
			transport = &mcp.StreamableClientTransport{Endpoint: info.URL, HTTPClient: httpClient, MaxRetries: -1, MaxEventSize: 1024 * 1024}
		}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "zentrale", Version: "1"}, &mcp.ClientOptions{
		Capabilities:           &mcp.ClientCapabilities{},
		MultiRoundTrip:         &mcp.MultiRoundTripOptions{Disabled: true},
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { server.changed.Add(1) },
	})
	// SSE's initial GET belongs to the session lifetime, not just initialization.
	// A temporary timer bounds startup without cancelling a successful session.
	startupTimer := time.AfterFunc(2*time.Minute, cancel)
	defer startupTimer.Stop()
	server.client, err = client.Connect(lifetime, transport, nil)
	if err != nil {
		return nil, err
	}
	status := remote.InspectSandbox(ctx, connection, machine)
	if status.Error != "" {
		return nil, errors.New(status.Error)
	}
	server.info.MachineID, server.info.Sandbox = machine.ID, status.Level
	engine.mu.Lock()
	if ctx.Err() != nil || lifetime.Err() != nil || engine.stopRequested[record.ID] {
		engine.mu.Unlock()
		return nil, errors.New("MCP connection interrupted")
	}
	count := 0
	for _, active := range engine.mcpServers {
		if active.agentID == record.ID && active.ctx.Err() == nil {
			count++
		}
	}
	if count >= maximumMCPServers {
		engine.mu.Unlock()
		return nil, errors.New("disconnect an MCP server before connecting another (limit 8)")
	}
	engine.mcpServers[info.ID] = server
	engine.workers.Add(1)
	engine.Remote.Hold(record.ID, "mcp:"+info.ID)
	engine.mu.Unlock()
	stopLifetimeClose := context.AfterFunc(lifetime, func() { _ = server.client.Close() })
	go func() {
		defer engine.workers.Done()
		defer stopLifetimeClose()
		_ = server.client.Wait()
		server.close()
		engine.Remote.Release("job:mcp:" + info.ID)
		engine.mu.Lock()
		delete(engine.mcpServers, info.ID)
		engine.mu.Unlock()
	}()
	return server.info, nil
}
