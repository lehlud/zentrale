package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
	"golang.org/x/crypto/ssh"
)

// Only test code executes a local shell. The application under test still sends
// every operation through an authenticated SSH session to this isolated peer.
func shellPeerHandler(shell string, environment []string) testssh.CommandHandler {
	return func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		arguments := []string{"-c", command}

		if filepath.Base(shell) == "fish" {
			arguments = append([]string{"--no-config"}, arguments...)
		}

		process := exec.CommandContext(ctx, shell, arguments...)
		process.Env = environment
		process.Stdout, process.Stderr = stdout, stderr
		input, err := process.StdinPipe()
		if err != nil {
			return 1
		}

		// Match sshd: a child's exit must not wait for a still-open SSH stdin.
		go func() {
			_, _ = io.Copy(input, stdin)
			_ = input.Close()
		}()

		if err := process.Run(); err != nil {
			if status, ok := err.(*exec.ExitError); ok {
				return status.ExitCode()
			}

			_, _ = fmt.Fprintln(stderr, err)

			return 1
		}

		return 0
	}
}

func executionTestEngine(
	t *testing.T,
	provider providers.Client,
	handler testssh.CommandHandler,
) (*Engine, domain.Agent, domain.Project) {
	t.Helper()
	engine, record := testEngine(t, provider)
	server, err := testssh.StartWithCommandHandler(handler)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(server.Close)
	public, err := remote.DecodePublicKey(server.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}

	var project domain.Project
	if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
		t.Fatal(err)
	}

	project.Directory = filepath.Join(t.TempDir(), "project 'quotes' $literal; space")

	if err := engine.Store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}

	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}

	machine.Host, machine.Port, machine.Sandbox = server.Host, server.Port, "auto"

	if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}

	return engine, record, project
}

func fishExecutable(t *testing.T) string {
	t.Helper()
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is required for the alternate-login-shell integration tests")
	}

	return fish
}

func TestRemotePOSIXScriptsAndDiagnosticsWithFishLoginShell(t *testing.T) {
	fish := fishExecutable(t)
	shell := shellPeerHandler(fish, os.Environ())
	handler := func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "test -c /dev/kvm") {
			_, _ = fmt.Fprintln(stderr, "sandbox detection unavailable")
			return 127
		}

		return shell(command, stdin, stdout, stderr)
	}

	engine, record, project := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Error("direct remote command test must not call a provider")
		return providers.Result{}, nil
	}), handler)

	connection, err := engine.Remote.Connector.Dial(context.Background(), project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	// This is the shape of the old auto-sandbox probe. It fails before any user
	// command is reached when SSH sends it directly to fish.
	// `if ...; then ...; fi` is accidentally accepted by some fish versions.
	// POSIX case syntax is incompatible across supported fish versions, while
	// remaining valid in the wrapper. Debian's older fish prints the diagnostic
	// but exits zero for this parser error; current fish exits 127.
	probe := "case ready in ready) printf 'ready';; esac"
	session, err := connection.Client.NewSession()
	if err != nil {
		t.Fatal(err)
	}

	directOutput, directError := session.CombinedOutput(probe)
	_ = session.Close()
	var exitStatus *ssh.ExitError
	if !strings.Contains(string(directOutput), "not inside of switch block") ||
		(directError != nil && (!errors.As(directError, &exitStatus) || exitStatus.ExitStatus() != 127)) {
		t.Fatalf("expected the original fish failure using %s, got %v, output=%q", fish, directError, directOutput)
	}

	if output, err := remote.Run(context.Background(), connection.Client, probe); err != nil ||
		output != "ready" {
		t.Fatalf("POSIX script over fish SSH: %q, %v", output, err)
	}

	_, err = remote.Run(context.Background(), connection.Client, "printf 'specific remote failure' >&2; exit 127")
	if !errors.As(err, &exitStatus) ||
		exitStatus.ExitStatus() != 127 ||
		!strings.Contains(err.Error(), "specific remote failure") {
		t.Fatalf("exit status or remote stderr was lost: %v", err)
	}

	result, err := engine.execute(context.Background(), record, "/bin/ls -d .", false, true)
	if err != nil {
		t.Fatal(err)
	}

	job := result.(map[string]any)["job"].(domain.Job)
	if job.Status != "completed" ||
		!strings.Contains(job.Output, ".") ||
		result.(map[string]any)["sandbox"] != "none" {
		t.Fatalf("approved sandbox bypass failed: %+v", result)
	}
}

func TestMissingRemotePythonBlocksInferenceAndKeepsInstructionUnconsumed(t *testing.T) {
	bin := t.TempDir()

	for _, name := range []string{"mkdir", "chmod"} {
		executable, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(executable, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}

	var calls atomic.Int32
	engine, record, _ := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		calls.Add(1)
		return providers.Result{Text: "must not answer"}, nil
	}), shellPeerHandler("/bin/sh", []string{"PATH=" + bin}))
	if err := engine.Prompt(record.ID, "Create the quicksort project"); err != nil {
		t.Fatal(err)
	}

	final := waitForAgent(t, engine, record.ID)
	if final.Status != "error" ||
		!strings.Contains(final.Error, "Required remote command is missing from PATH: python3") {
		t.Fatal(final)
	}

	messages, historyError := engine.loadHistory(record.ID)
	if calls.Load() != 0 || historyError != nil || len(messages) != 1 {
		t.Fatalf(
			"unusable runtime consumed the instruction or called the provider: calls=%d messages=%v error=%v",
			calls.Load(),
			messages,
			historyError,
		)
	}
}

func TestAgentCreatesAndTestsPythonProjectOverSSH(t *testing.T) {
	shells := []string{"/bin/sh"}

	if fish, err := exec.LookPath("fish"); err == nil {
		shells = append(shells, fish)
	}

	for _, shell := range shells {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			mainTurns := 0
			provider := providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
				if request.Config.ID == "approver-provider" {
					return providers.Result{
						Text:       `{"allowed":true,"reason":"","blanket":false}`,
						UsageKnown: true,
					}, nil
				}

				mainTurns++

				if mainTurns == 1 {
					actions := []struct {
						action    string
						arguments map[string]any
					}{
						{"list_files", map[string]any{"path": "."}},
						{"write_file", map[string]any{"path": "quicksort.py", "content": quicksortExample}},
						{
							"write_file",
							map[string]any{
								"path":    "test_quicksort.py",
								"content": quicksortTests,
							},
						},
						{
							"write_file",
							map[string]any{
								"path":    "README.md",
								"content": "# Quicksort demo\n\nRun python3 quicksort.py and python3 -m unittest -v.\n",
							},
						},
						{"exec", map[string]any{"command": "python3 -m unittest -v && python3 quicksort.py", "timeout_ms": float64(30000)}},
					}

					calls := make([]providers.Call, 0, len(actions))

					for index, action := range actions {
						calls = append(
							calls,
							providers.Call{
								ID:   fmt.Sprint(index),
								Name: "harness",
								Arguments: map[string]any{
									"action":    action.action,
									"arguments": JSON(action.arguments),
								},
							},
						)
					}

					return providers.Result{Calls: calls, UsageKnown: true}, nil
				}

				for _, message := range request.Messages {
					if strings.Contains(message.Content, "_change") ||
						strings.Contains(message.Content, "--- /dev/null") {
						return providers.Result{}, errors.New("UI-only file diff leaked into inference")
					}

					if message.Role == "tool" {
						var response struct {
							Error string `json:"error"`
						}
						if err := json.Unmarshal([]byte(message.Content), &response); err != nil {
							return providers.Result{}, err
						}

						if response.Error != "" {
							return providers.Result{}, fmt.Errorf("remote tool failed: %s", response.Error)
						}
					}
				}

				return providers.Result{Text: "Project created and tested.", UsageKnown: true}, nil
			})

			engine, record, project := executionTestEngine(t, provider, shellPeerHandler(shell, os.Environ()))
			if err := engine.Prompt(record.ID, "Create a dummy Python project with a quicksort example"); err != nil {
				t.Fatal(err)
			}

			if final := waitForAgent(t, engine, record.ID); final.Status != "idle" {
				t.Fatal(final.Error)
			}

			for _, name := range []string{"quicksort.py", "test_quicksort.py", "README.md"} {
				if _, err := os.Stat(filepath.Join(project.Directory, name)); err != nil {
					t.Fatal(err)
				}
			}

			changes, err := engine.listAll("file_change", &record.ID)
			if err != nil || len(changes) != 3 {
				t.Fatalf("expected three actual file changes: %v %v", changes, err)
			}

			for _, change := range changes {
				var message domain.Message
				if err := engine.Store.Load("message", stringArg(change, "id"), &message); err != nil ||
					message.Role != "tool" ||
					strings.Contains(message.Content, "patch") ||
					!strings.Contains(stringArg(change, "patch"), "+++ b/") {
					t.Fatalf("diff is not separate from its linked tool result: %v %v", change, err)
				}
			}

			jobs, err := engine.listAll("job", &record.ID)
			if err != nil ||
				len(jobs) != 1 ||
				jobs[0]["status"] != "completed" ||
				!strings.Contains(stringArg(jobs[0], "output"), "OK") ||
				!strings.Contains(stringArg(jobs[0], "output"), "[0, 1, 2, 3, 3, 7, 8, 10]") {
				t.Fatalf("quicksort execution: %+v %v", jobs, err)
			}
		})
	}
}

const quicksortExample = `def quicksort(values):
    if len(values) <= 1:
        return list(values)
    pivot = values[len(values) // 2]
    return (quicksort([v for v in values if v < pivot])
            + [v for v in values if v == pivot]
            + quicksort([v for v in values if v > pivot]))

if __name__ == "__main__":
    print(quicksort([8, 3, 1, 7, 0, 10, 2, 3]))
`

const quicksortTests = `import unittest
from quicksort import quicksort

class QuicksortTests(unittest.TestCase):
    def test_sorting(self):
        for values in [[], [1], [3, 1, 2], [4, 4, 2, 4], [-3, 0, -1, 2]]:
            with self.subTest(values=values):
                self.assertEqual(quicksort(values), sorted(values))

    def test_preserves_input(self):
        values = [3, 1, 2]
        quicksort(values)
        self.assertEqual(values, [3, 1, 2])
`
