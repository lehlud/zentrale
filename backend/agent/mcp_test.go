package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This legacy stdio fixture verifies real bytes through an authenticated SSH
// channel and shell, including discovery fallback, pagination and server state.
const stdioMCPFixture = `import json, os, sys, time
calls = 0
for line in sys.stdin:
    message = json.loads(line)
    if "id" not in message:
        continue
    method = message["method"]
    result = {}
    error = None
    if method == "initialize":
        result = {"protocolVersion":"2025-11-25","serverInfo":{"name":"fixture","version":"1"},"capabilities":{"tools":{}}}
    elif method == "tools/list":
        name = "write_marker" if message.get("params",{}).get("cursor") else "decompile"
        result = {"tools":[{"name":name,"description":"Inspect or modify a test binary","annotations":{"readOnlyHint":True},"inputSchema":{"type":"object","properties":{"value":{"type":"string"}}}}]}
        if name == "decompile":
            result["nextCursor"] = "second-page"
    elif method == "tools/call":
        params = message["params"]
        if params.get("arguments",{}).get("wait"):
            sys.stdin.read()
            break
        if params["name"] == "write_marker":
            open("marker.txt", "w").write(params["arguments"]["value"])
        calls += 1
        result = {"content":[{"type":"text","text":"int main() { return 42; } calls="+str(calls)+" cwd="+os.getcwd()}]}
    elif method == "ping":
        pass
    else:
        error = {"code":-32601,"message":"Method not found"}
    response = {"jsonrpc":"2.0","id":message["id"]}
    response["error" if error else "result"] = error if error else result
    print(json.dumps(response), flush=True)
`

func mcpDispatch(
	engine *Engine,
	ctx context.Context,
	record domain.Agent,
	action string,
	arguments map[string]any,
	network bool,
) (any, error) {
	return engine.dispatch(ctx, record, providers.Call{
		Name: "harness",
		Arguments: map[string]any{
			"action": action, "arguments": JSON(arguments), "network": network,
		},
	})
}

func mcpTestProvider(t *testing.T, reviews *atomic.Int32) providers.Client {
	return providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		reviews.Add(1)
		current := request.Messages[len(request.Messages)-1].Content
		if strings.Contains(current, `"action":"mcp_call"`) && !strings.Contains(current, `"_mcpTool"`) {
			t.Error("MCP call reached approval without the discovered schema")
		}

		if strings.Contains(current, `"value":"forbidden"`) {
			return providers.Result{
				Text:       `{"allowed":false,"reason":"Marker write is not authorized","blanket":false}`,
				UsageKnown: true,
			}, nil
		}

		return providers.Result{
			Text:       `{"allowed":true,"reason":"Test operation","blanket":false}`,
			UsageKnown: true,
		}, nil
	})
}

func TestMCPStdioUsesSandboxAndReviewsEveryToolCall(t *testing.T) {
	var reviews atomic.Int32
	engine, record, project := executionTestEngine(t, mcpTestProvider(t, &reviews), shellPeerHandler("/bin/sh", os.Environ()))
	t.Cleanup(func() { engine.closeMCPServers("") })
	fixture := filepath.Join(t.TempDir(), "server.py")
	if err := os.WriteFile(fixture, []byte(stdioMCPFixture), 0600); err != nil {
		t.Fatal(err)
	}

	// An instrumented external runner checks that MCP uses the same sandbox
	// wrapper as exec, rather than opening a host process next to it.
	runner, trace := filepath.Join(t.TempDir(), "runner"), filepath.Join(t.TempDir(), "sandbox.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + remote.Quote(trace) + "\nwhile test \"$1\" != --; do shift; done\nshift\nexec \"$@\"\n"
	if err := os.WriteFile(runner, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	machine, _ := engine.Remote.Connector.Machine(project.MachineIDs[0])
	machine.Sandbox, machine.MicroVMCommand = "microvm", runner

	if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}

	startup, cancelStartup := context.WithCancel(context.Background())
	connected, err := mcpDispatch(
		engine,
		startup,
		record,
		"mcp_connect",
		map[string]any{
			"name":    "Ghidra fixture",
			"command": "python3 -u " + remote.Quote(fixture),
		},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}

	cancelStartup()                     // A completed model/tool turn must not kill the MCP session.
	engine.releaseConnection(record.ID) // Normal completion releases its cached handle.
	server := connected.(mcpServerInfo)
	args := map[string]any{"serverId": server.ID}
	catalog, err := mcpDispatch(engine, context.Background(), record, "mcp_tools", args, false)
	if err != nil ||
		!strings.Contains(JSON(catalog), "write_marker") ||
		strings.Contains(JSON(catalog), "inputSchema") {
		t.Fatal(catalog, err)
	}

	schema, err := mcpDispatch(
		engine,
		context.Background(),
		record,
		"mcp_tools",
		map[string]any{
			"serverId": server.ID,
			"tool":     "write_marker",
		},
		false,
	)
	if err != nil || !strings.Contains(JSON(schema), "inputSchema") {
		t.Fatal(schema, err)
	}

	before := reviews.Load()
	_, err = mcpDispatch(
		engine,
		context.Background(),
		record,
		"mcp_call",
		map[string]any{
			"serverId": server.ID,
			"tool":     "write_marker",
			"arguments": map[string]any{
				"value": "forbidden",
			},
		},
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "not authorized") || reviews.Load() != before+1 {
		t.Fatal("readOnlyHint bypassed approval", err)
	}

	if _, err := os.Stat(filepath.Join(project.Directory, "marker.txt")); !os.IsNotExist(err) {
		t.Fatal("denied MCP tool changed the workspace")
	}

	code := `const result = await tools.mcp_call({serverId:` + JSON(server.ID) + `,tool:"decompile",arguments:{}}); print(result.content[0].text);`
	result, err := engine.runCodeMode(context.Background(), record, map[string]any{"code": code})
	if err != nil ||
		!strings.Contains(JSON(result), "calls=1") ||
		!strings.Contains(JSON(result), project.Directory) {
		t.Fatal(result, err)
	}

	traceData, _ := os.ReadFile(trace)
	if server.Sandbox != "microvm" ||
		!strings.Contains(string(traceData), "--network=false") ||
		!strings.Contains(string(traceData), fixture) {
		t.Fatal("MCP escaped its sandbox", server, string(traceData))
	}

	other := record
	other.ID = "another-agent"

	if _, err := mcpDispatch(engine, context.Background(), other, "mcp_tools", args, false); err == nil {
		t.Fatal("another agent used a private MCP session")
	}

	if _, err := mcpDispatch(engine, context.Background(), record, "mcp_tools", args, true); err == nil {
		t.Fatal("MCP network permissions were widened without reconnecting")
	}

	engine.mu.Lock()
	live := engine.mcpServers[server.ID]
	engine.mu.Unlock()
	live.changed.Add(1)
	before = reviews.Load()
	if _, err := mcpDispatch(
		engine,
		context.Background(),
		record,
		"mcp_call",
		map[string]any{
			"serverId":  server.ID,
			"tool":      "decompile",
			"arguments": map[string]any{},
		},
		false,
	); err == nil ||
		reviews.Load() != before {
		t.Fatal("stale MCP schema was used")
	}

	if _, err := mcpDispatch(engine, context.Background(), record, "mcp_tools", args, false); err != nil {
		t.Fatal(err)
	}

	if err := engine.Stop(record.ID); err != nil {
		t.Fatal(err)
	}

	if len(engine.mcpServersFor(record.ID)) != 0 {
		t.Fatal("stopped agent retained its MCP process")
	}
}

func TestMCPHTTPAndSSEUseSSHRelayAndPreserveSession(t *testing.T) {
	for _, transport := range []string{"http-json", "http-events", "sse"} {
		t.Run(transport, func(t *testing.T) {
			var reviews, relays, calls atomic.Int32
			server := mcp.NewServer(&mcp.Implementation{Name: "Ghidra fixture", Version: "1"}, nil)
			server.AddTool(&mcp.Tool{
				Name:        "decompile",
				Description: "Decompile a function",
				InputSchema: map[string]any{
					"type": "object",
				},
			}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.Add(1)
				var args map[string]any
				_ = json.Unmarshal(request.Params.Arguments, &args)

				if args["fail"] == true {
					return &mcp.CallToolResult{
						IsError: true,
						Content: []mcp.Content{
							&mcp.TextContent{
								Text: "binary not loaded",
							},
						},
					}, nil
				}

				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{
							Text: "int main() { return 42; }",
						},
					},
				}, nil
			})
			var handler http.Handler

			if transport == "sse" {
				handler = mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server }, nil)
			} else {
				handler = mcp.NewStreamableHTTPHandler(
					func(*http.Request) *mcp.Server { return server },
					&mcp.StreamableHTTPOptions{
						JSONResponse: transport == "http-json",
					},
				)
			}

			endpoint := httptest.NewServer(handler)
			defer func() {
				endpoint.CloseClientConnections()
				endpoint.Close()
			}()

			shell := shellPeerHandler("/bin/sh", os.Environ())
			engine, record, project := executionTestEngine(t, mcpTestProvider(t, &reviews), func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
				if strings.Contains(command, "MCP request exceeds") {
					relays.Add(1)
				}

				return shell(command, stdin, stdout, stderr)
			})
			defer engine.closeMCPServers("")

			machine, _ := engine.Remote.Connector.Machine(project.MachineIDs[0])
			machine.Sandbox = "none"
			_ = engine.Store.Save("machine", machine.ID, "", machine)
			mode := "http"

			if transport == "sse" {
				mode = "sse"
			}

			startup, cancel := context.WithCancel(context.Background())
			connected, err := mcpDispatch(
				engine,
				startup,
				record,
				"mcp_connect",
				map[string]any{
					"name":      "Ghidra",
					"transport": mode,
					"url":       endpoint.URL,
				},
				true,
			)
			if err != nil {
				cancel()
				t.Fatal(err)
			}

			cancel()
			info := connected.(mcpServerInfo)
			if _, err := mcpDispatch(engine, context.Background(), record, "mcp_tools", map[string]any{
				"serverId": info.ID,
			}, true); err != nil {
				t.Fatal(err)
			}

			for _, fail := range []bool{false, true} {
				result, err := mcpDispatch(
					engine,
					context.Background(),
					record,
					"mcp_call",
					map[string]any{
						"serverId": info.ID,
						"tool":     "decompile",
						"arguments": map[string]any{
							"fail": fail,
						},
					},
					true,
				)
				if fail && (err == nil || !strings.Contains(err.Error(), "binary not loaded")) {
					t.Fatal("MCP failure hidden", result, err)
				}

				if !fail && (err != nil || !strings.Contains(JSON(result), "return 42")) {
					t.Fatal(result, err)
				}
			}

			if calls.Load() != 2 || relays.Load() < 3 || reviews.Load() != 4 {
				t.Fatal("MCP calls skipped SSH/review or were retried", calls.Load(), relays.Load(), reviews.Load())
			}

			if _, err := mcpDispatch(engine, context.Background(), record, "mcp_disconnect", map[string]any{
				"serverId": info.ID,
			}, true); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMCPEndpointValidationAndCancelledStartup(t *testing.T) {
	for _, endpoint := range []string{
		"http://example.org/mcp",
		"http://127.0.0.1.evil/mcp",
		"file:///tmp/mcp",
		"http://name:password@localhost/mcp",
	} {
		if _, err := remote.ValidateMCPEndpoint(endpoint); err == nil {
			t.Fatal("accepted endpoint outside sandbox loopback", endpoint)
		}
	}

	var reviews atomic.Int32
	engine, record, _ := executionTestEngine(t, mcpTestProvider(t, &reviews), shellPeerHandler("/bin/sh", os.Environ()))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := mcpDispatch(
		engine,
		ctx,
		record,
		"mcp_connect",
		map[string]any{
			"name":    "stalled",
			"command": "python3 -c " + remote.Quote("import sys; sys.stdin.read()"),
		},
		false,
	)
	if err == nil || len(engine.mcpServersFor(record.ID)) != 0 {
		t.Fatal("cancelled handshake retained a session", err)
	}

	if _, err := mcpConnectArguments(
		map[string]any{
			"name":      "Ghidra",
			"transport": "http",
			"url":       "http://localhost:8080/mcp",
		},
		false,
	); err == nil {
		t.Fatal("shared network connection was not reviewed")
	}
}

func TestMCPCallCancellationAndMachineChangesRequireReconnect(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "machine configuration"}[change], func(t *testing.T) {
			var reviews atomic.Int32
			engine, record, project := executionTestEngine(t, mcpTestProvider(t, &reviews), shellPeerHandler("/bin/sh", os.Environ()))
			defer engine.closeMCPServers("")

			machine, _ := engine.Remote.Connector.Machine(project.MachineIDs[0])
			machine.Sandbox = "none"
			_ = engine.Store.Save("machine", machine.ID, "", machine)
			fixture := filepath.Join(t.TempDir(), "server.py")
			if err := os.WriteFile(fixture, []byte(stdioMCPFixture), 0600); err != nil {
				t.Fatal(err)
			}

			connected, err := mcpDispatch(
				engine,
				context.Background(),
				record,
				"mcp_connect",
				map[string]any{
					"name":    "fixture",
					"command": "python3 -u " + remote.Quote(fixture),
				},
				false,
			)
			if err != nil {
				t.Fatal(err)
			}

			info := connected.(mcpServerInfo)
			if _, err := mcpDispatch(engine, context.Background(), record, "mcp_tools", map[string]any{
				"serverId": info.ID,
			}, false); err != nil {
				t.Fatal(err)
			}

			if change {
				machine.Name = "Changed configuration"
				_ = engine.Store.Save("machine", machine.ID, "", machine)
				_, err = mcpDispatch(
					engine,
					context.Background(),
					record,
					"mcp_call",
					map[string]any{
						"serverId":  info.ID,
						"tool":      "decompile",
						"arguments": map[string]any{},
					},
					false,
				)
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
				defer cancel()

				_, err = mcpDispatch(
					engine,
					ctx,
					record,
					"mcp_call",
					map[string]any{
						"serverId": info.ID,
						"tool":     "decompile",
						"arguments": map[string]any{
							"wait": true,
						},
					},
					false,
				)
			}

			if err == nil || len(engine.mcpServersFor(record.ID)) != 0 {
				t.Fatal("interrupted MCP was retained", err)
			}
		})
	}
}

func TestMCPBinaryContentDoesNotInflateModelContext(t *testing.T) {
	content := mcpContent([]mcp.Content{
		&mcp.TextContent{Text: "Decompiled function"},
		&mcp.ImageContent{Data: []byte(strings.Repeat("binary-data", 10000)), MIMEType: "image/png"},
		&mcp.AudioContent{Data: []byte("binary-data"), MIMEType: "audio/wav"},
	})

	encoded := JSON(content)
	if !strings.Contains(encoded, "Decompiled function") ||
		!strings.Contains(encoded, "\"omitted\":true") ||
		len(encoded) > 1000 {
		t.Fatal("unsupported media leaked into inference", len(encoded))
	}
}

func TestRemoteMCPProcessCancellationUnblocksIO(t *testing.T) {
	release := make(chan struct{})
	var reviews atomic.Int32
	shell := shellPeerHandler("/bin/sh", os.Environ())
	engine, record, _ := executionTestEngine(t, mcpTestProvider(t, &reviews), func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "mcp-blocked-io") {
			<-release
			return 0
		}

		return shell(command, stdin, stdout, stderr)
	})
	defer close(release)

	connection, _, _, err := engine.connect(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	process, err := remote.StartProcess(ctx, connection.Client, "mcp-blocked-io")
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()

	readDone, writeDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := process.Read(make([]byte, 1))
		readDone <- err
	}()
	go func() {
		_, err := process.Write([]byte(strings.Repeat("x", 8*1024*1024)))
		writeDone <- err
	}()
	cancel()

	for _, done := range []chan error{readDone, writeDone} {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("cancelled I/O succeeded")
			}
		case <-time.After(time.Second):
			t.Fatal("unresponsive SSH peer blocked MCP cancellation")
		}
	}
}

func TestMCPStartupReportsRemoteDiagnostic(t *testing.T) {
	var reviews atomic.Int32
	shell := shellPeerHandler("/bin/sh", os.Environ())
	engine, record, project := executionTestEngine(t, mcpTestProvider(t, &reviews), func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "missing Java runtime") {
			// Match sshd: deliver stderr/exit immediately. os/exec's fixture stdin
			// copier otherwise waits for EOF even after its child has exited.
			_, _ = io.WriteString(stderr, "MCP startup failed: missing Java runtime\n")
			return 127
		}

		return shell(command, stdin, stdout, stderr)
	})

	machine, _ := engine.Remote.Connector.Machine(project.MachineIDs[0])
	machine.Sandbox = "none"
	_ = engine.Store.Save("machine", machine.ID, "", machine)
	_, err := mcpDispatch(engine, context.Background(), record, "mcp_connect", map[string]any{
		"name":    "missing runtime",
		"command": "printf 'MCP startup failed: missing Java runtime\\n' >&2; exit 127",
	}, false)
	if err == nil || !strings.Contains(err.Error(), "missing Java runtime") {
		t.Fatal("remote startup diagnostic lost", err)
	}
}
