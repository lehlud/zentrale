package remote

import (
	"context"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

func TestJumpOnlyHostChecksAndForwardsWithoutExecutingAnything(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	jump, target := newSSHFixture(t, ""), newSSHFixture(t, "")
	bastion := jump.machine("jump", key.ID)
	bastion.JumpHostOnly = true
	bastion.WorkspaceRoot = ""
	bastion.Sandbox = SandboxAutomatic
	worker := target.machine("worker", key.ID)
	worker.Jumps = []string{bastion.ID}
	for _, machine := range []domain.Machine{bastion, worker} {
		if err := store.Save("machine", machine.ID, "", machine); err != nil {
			t.Fatal(err)
		}
	}
	connector := &Connector{Store: store, NativeSessionsForTests: true}
	ctx := context.Background()
	challenge := connector.Check(ctx, bastion.ID)
	if challenge.Connected || challenge.HostKey.PublicKey == "" {
		t.Fatal("jump host skipped explicit host-key trust")
	}
	for _, fixture := range []*sshFixture{jump, target} {
		if err := store.TrustSSHKey(fixture.hostKey.Marshal()); err != nil {
			t.Fatal(err)
		}
	}
	checked := connector.Check(ctx, bastion.ID)
	if !checked.Connected || checked.Error != "" || checked.Sandbox.Level != "" {
		t.Fatalf("SSH-only check: %+v", checked)
	}
	if _, err := (&Connector{Store: store}).Dial(ctx, bastion.ID); err == nil || !strings.Contains(err.Error(), "jump host only") {
		t.Fatalf("jump-only work target accepted: %v", err)
	}
	raw, err := connector.dialNative(ctx, bastion.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	retained, err := raw.Retain()
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if _, err := Run(ctx, retained, "must never execute"); err == nil {
		t.Fatal("retained jump handle executed a command")
	}
	if _, err := StartProcess(ctx, raw, "must never execute"); err == nil {
		t.Fatal("jump handle started a process")
	}
	if _, err := raw.DialContext(ctx, "tcp", "127.0.0.1:1"); err == nil {
		t.Fatal("jump handle opened an application tunnel")
	}
	connection, err := connector.Dial(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := Run(ctx, connection, "work on the actual destination"); err != nil {
		t.Fatal(err)
	}
	if err := store.Patch("machine", worker.ID, map[string]any{"jumpHostOnly": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, connection, "cached handles must respect the new role"); err == nil {
		t.Fatal("cached work connection bypassed the jump-only role")
	}
	jump.mutex.Lock()
	executions := jump.executions
	jump.mutex.Unlock()
	if executions != 0 {
		t.Fatalf("executed %d commands on jump-only host", executions)
	}
}
