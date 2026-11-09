package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func TestBackgroundGPUJobsOverlapOnDifferentHostsWithoutChangingDefault(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{
			Text:       `{"allowed":true,"reason":"test task","blanket":false}`,
			UsageKnown: true,
		}, nil
	}))

	var original domain.Machine
	if err := engine.Store.Load("machine", "test-worker", &original); err != nil {
		t.Fatal(err)
	}

	var project domain.Project
	if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
		t.Fatal(err)
	}

	started := make(chan string, 3)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })

	pool := domain.Pool{
		ID:            "gpu",
		Name:          "GPU",
		MaxMachines:   2,
		SharedHome:    true,
		WorkspaceRoot: "/srv/projects",
		Sandbox:       "auto",
	}

	for index := range 3 {
		id := fmt.Sprintf("gpu-%d", index)
		peer, err := testssh.StartWithCommandHandler(func(command string, input io.Reader, stdout, stderr io.Writer) int {
			plain := strings.ReplaceAll(command, `'"'"'`, `'`)

			switch {
			case strings.Contains(plain, " 'ensure' "):
				fmt.Fprintln(stdout, "microvm")
			case strings.Contains(plain, " 'stage' "):
				if id == "gpu-0" {
					fmt.Fprintln(stderr, "VFIO group is held by another persistent VM")
					return 1
				}

				var payload struct {
					Execution map[string]any `json:"execution"`
				}
				if err := json.NewDecoder(input).Decode(&payload); err != nil ||
					payload.Execution["command"] == "" {
					t.Error("missing guest command", err)
					return 1
				}

				fmt.Fprint(stdout, "/var/tmp/zentrale-test/requests/0123456789abcdef0123456789abcdef")
			case strings.Contains(plain, " 'run' "):
				started <- id
				<-release
				fmt.Fprintln(stdout, "finished "+id)
			}

			return 0
		})
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(peer.Close)
		key, err := remote.DecodePublicKey(peer.PublicKey)
		if err != nil {
			t.Fatal(err)
		}

		if err := engine.Store.TrustSSHKey(key); err != nil {
			t.Fatal(err)
		}

		pool.Machines = append(
			pool.Machines,
			domain.Machine{
				ID:      id,
				Name:    id,
				Host:    peer.Host,
				Port:    peer.Port,
				User:    "developer",
				KeyID:   original.KeyID,
				Sandbox: "auto",
				VMType:  "q35",
				GPUDevices: []string{
					"0000:01:00.0",
				},
			},
		)
	}

	if err := engine.Store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}

	project.MachineIDs, project.PoolIDs = nil, []string{pool.ID}

	if err := engine.Store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}

	call := func() (map[string]any, error) {
		result, err := engine.dispatch(
			context.Background(),
			record,
			providers.Call{
				Name: "exec_command",
				Arguments: map[string]any{
					"cmd":        "training shard",
					"gpu":        true,
					"background": true,
				},
			},
		)
		if err != nil {
			return nil, err
		}

		return result.(map[string]any), nil
	}

	first, err := call()
	if err != nil {
		t.Fatal(err)
	}

	second, err := call()
	if err != nil {
		t.Fatal(err)
	}

	if first["machineId"] == second["machineId"] {
		t.Fatal("independent GPU jobs used the same host", first, second)
	}

	snapshots, err := engine.captureRuntimeContext(record, nil)
	if err != nil ||
		len(snapshots) == 0 ||
		!strings.Contains(snapshots[len(snapshots)-1].Content, first["jobId"].(string)) ||
		!strings.Contains(snapshots[len(snapshots)-1].Content, second["jobId"].(string)) {
		t.Fatal("agent runtime lost its parallel jobs", snapshots, err)
	}

	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("GPU jobs did not overlap")
		}
	}

	if _, err := call(); err == nil {
		t.Fatal("third job exceeded the two-machine pool limit")
	}

	var current domain.Agent
	if err := engine.Store.Load("agent", record.ID, &current); err != nil ||
		current.MachineID != record.MachineID {
		t.Fatal("job placement changed the default machine", current, err)
	}

	once.Do(func() { close(release) })

	for _, result := range []map[string]any{first, second} {
		engine.mu.Lock()
		job := engine.jobs[result["jobId"].(string)]
		engine.mu.Unlock()

		if job != nil {
			select {
			case <-job.done:
			case <-time.After(3 * time.Second):
				t.Fatal("job did not finish")
			}
		}
	}

	deadline := time.Now().Add(2 * time.Second)

	for {
		records, err := engine.listAll("job_completion", &record.ID)
		if err != nil {
			t.Fatal(err)
		}

		if len(records) == 2 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("background completions were not delivered", records)
		}

		time.Sleep(10 * time.Millisecond)
	}
}
