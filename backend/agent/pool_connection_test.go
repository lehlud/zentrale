package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/remote"
)

func TestPoolConnectionSampleSize(t *testing.T) {
	for _, example := range []struct{ total, expected int }{
		{0, 0}, {1, 1}, {2, 2}, {3, 3}, {4, 3},
		{29, 3}, {30, 3}, {31, 4}, {40, 4}, {41, 5},
		{70, 7}, {71, 8}, {80, 8}, {81, 8}, {1000, 8},
	} {
		t.Run(fmt.Sprint(example.total), func(t *testing.T) {
			if got := poolConnectionSampleSize(example.total); got != example.expected {
				t.Fatalf("sample of %d members: got %d, want %d", example.total, got, example.expected)
			}
		})
	}
}

func TestPoolConnectionSamplesDistinctMembersAndRemembersSandbox(t *testing.T) {
	engine, _ := testEngine(t, nil)
	var probes atomic.Int32
	peer, err := testssh.StartWithCommandHandler(func(command string, _ io.Reader, stdout, _ io.Writer) int {
		if strings.Contains(command, "test -c /dev/kvm") {
			probes.Add(1)
			_, _ = fmt.Fprintln(stdout, "bwrap")
		}
		return 0
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.Close)
	trustPoolTestPeer(t, engine, peer)

	for _, total := range []int{2, 31, 100} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			pool := domain.Pool{
				ID:            fmt.Sprintf("pool-%d", total),
				Name:          "Probe pool",
				MaxMachines:   1,
				Sandbox:       "auto",
				WorkspaceRoot: "/srv/projects",
			}
			for index := range total {
				pool.Machines = append(pool.Machines, domain.Machine{
					ID:   fmt.Sprintf("worker-%d", index),
					Name: fmt.Sprintf("Worker %d", index),
					Host: peer.Host,
					Port: peer.Port,
					User: "test",
				})
			}
			pool, err := engine.Store.SaveManagedPool(pool)
			if err != nil {
				t.Fatal(err)
			}

			before := probes.Load()
			result, err := engine.CheckPoolConnection(context.Background(), pool.ID)
			if err != nil {
				t.Fatal(err)
			}

			want := poolConnectionSampleSize(total)
			if result.PoolID != pool.ID || result.TotalMachines != total || len(result.Machines) != want {
				t.Fatalf("wrong pool sample: %+v", result)
			}
			if got := int(probes.Load() - before); got != want {
				t.Fatalf("ran %d probes, want %d", got, want)
			}

			seen := make(map[string]bool)
			for _, member := range result.Machines {
				if seen[member.MachineID] {
					t.Fatalf("member probed twice: %s", member.MachineID)
				}
				seen[member.MachineID] = true

				configured, err := engine.Remote.Connector.Machine(member.MachineID)
				if err != nil || configured.Name != member.Name || configured.KeyID != pool.KeyID {
					t.Fatalf("sample escaped the configured pool identity: %+v, %v", member, err)
				}
				if !member.Connected || member.Error != "" || member.Sandbox.Level != "bwrap" {
					t.Fatalf("member was not actually probed: %+v", member)
				}
				if member.Sandbox.MachineID != member.MachineID {
					t.Fatalf("sandbox observation has the wrong member identity: %+v", member)
				}
			}

			statuses, err := engine.SandboxStatuses()
			if err != nil {
				t.Fatal(err)
			}
			remembered := 0
			for _, status := range statuses {
				if seen[status.MachineID] {
					remembered++
				}
			}
			if remembered != want {
				t.Fatalf("remembered %d member observations, want %d", remembered, want)
			}

			var saved domain.Pool
			if err := engine.Store.Load("pool", pool.ID, &saved); err != nil || JSON(saved) != JSON(pool) {
				t.Fatalf("sampling changed the configured member order: %v", err)
			}
		})
	}
}

func TestPoolConnectionKeepsFailuresAndTrustChallengesPerMember(t *testing.T) {
	engine, _ := testEngine(t, nil)
	pool := domain.Pool{ID: "mixed", Name: "Mixed", WorkspaceRoot: "/srv/projects", MaxMachines: 1}
	for _, id := range []string{"connected", "untrusted", "rejected"} {
		peer, err := testssh.Start()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(peer.Close)
		if id != "untrusted" {
			trustPoolTestPeer(t, engine, peer)
		}
		peer.RejectAuthentication(id == "rejected")
		pool.Machines = append(pool.Machines, domain.Machine{
			ID: id, Name: id, Host: peer.Host, Port: peer.Port, User: "test", Sandbox: "none",
		})
	}
	if _, err := engine.Store.SaveManagedPool(pool); err != nil {
		t.Fatal(err)
	}

	result, err := engine.CheckPoolConnection(context.Background(), pool.ID)
	if err != nil || len(result.Machines) != 3 {
		t.Fatalf("one failure hid the remaining results: %+v, %v", result, err)
	}

	for _, member := range result.Machines {
		switch member.Name {
		case "connected":
			if !member.Connected || member.Error != "" {
				t.Fatalf("healthy member failed: %+v", member)
			}
		case "untrusted":
			if member.Connected || member.HostKey.PublicKey == "" || member.Error == "" {
				t.Fatalf("host trust challenge was lost: %+v", member)
			}
		case "rejected":
			if member.Connected || member.Error == "" || member.HostKey.PublicKey != "" {
				t.Fatalf("authentication error was lost: %+v", member)
			}
		default:
			t.Fatalf("unexpected member: %+v", member)
		}
	}
}

func TestPoolConnectionRejectsMissingEmptyAndCancelledChecks(t *testing.T) {
	engine, _ := testEngine(t, nil)
	if _, err := engine.CheckPoolConnection(context.Background(), "missing"); err == nil {
		t.Fatal("unknown pool accepted")
	}

	pool := domain.Pool{ID: "empty"}
	if err := engine.Store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.CheckPoolConnection(context.Background(), pool.ID); err == nil {
		t.Fatal("empty pool accepted")
	}

	pool.Machines = []domain.Machine{{ID: "worker"}}
	if err := engine.Store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.CheckPoolConnection(ctx, pool.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled check started probes: %v", err)
	}
}

func trustPoolTestPeer(t *testing.T, engine *Engine, peer *testssh.Server) {
	t.Helper()
	key, err := remote.DecodePublicKey(peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.TrustSSHKey(key); err != nil {
		t.Fatal(err)
	}
}
