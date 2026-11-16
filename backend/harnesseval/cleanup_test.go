package harnesseval

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/remote"
)

func TestEvaluationCleanupStopsNativeVMBeforeRemovingFiles(t *testing.T) {
	for _, scenario := range []string{"removed", "running", "foreign"} {
		t.Run(scenario, func(t *testing.T) {
			commands := make(chan string, 4)
			peer, err := testssh.StartWithCommandHandler(func(command string, input io.Reader, output, diagnostic io.Writer) int {
				_, _ = io.Copy(io.Discard, input)
				if strings.Contains(command, "rm -rf -- ") {
					commands <- "files"
					return 0
				}
				commands <- "vm"
				if scenario == "running" {
					_, _ = io.WriteString(diagnostic, "guest is still active")
					return 1
				}
				_, _ = io.WriteString(output, `{}`)
				return 0
			})
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			store, err := datastore.Open("")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			key, err := store.GenerateSSHKey()
			if err != nil {
				t.Fatal(err)
			}
			public, err := remote.DecodePublicKey(peer.PublicKey)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.TrustSSHKey(public); err != nil {
				t.Fatal(err)
			}
			machine := domain.Machine{ID: "machine", Host: peer.Host, Port: peer.Port, User: "test", KeyID: key.ID, Sandbox: remote.SandboxMicroVM}
			project := domain.Project{ID: "project", Directory: "/tmp/zentrale-eval-fixture/project", MachineIDs: []string{machine.ID}}
			record := domain.Agent{ID: "main", ProjectID: project.ID}
			workspace := project.Directory
			if scenario == "foreign" {
				workspace = "/unrelated/project"
			}
			if err := store.SaveResources([]datastore.ResourceWrite{
				{Kind: "machine", ID: machine.ID, Value: machine},
				{Kind: "project", ID: project.ID, Value: project},
				{Kind: "agent", ID: record.ID, Parent: project.ID, Value: record},
				{Kind: "microvm", ID: record.ID + ":" + machine.ID, Parent: record.ID, Value: map[string]any{"agentId": record.ID, "projectId": project.ID, "machineId": machine.ID, "workspace": workspace}},
				{Kind: "project", ID: "other", Value: domain.Project{ID: "other"}},
			}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = cleanupCase(ctx, store, &remote.Connector{Store: store}, machine, project, record, remote.SandboxMicroVM)
			if (err != nil) != (scenario != "removed") {
				t.Fatal(err)
			}
			close(commands)
			var seen []string
			for command := range commands {
				seen = append(seen, command)
			}
			switch scenario {
			case "removed":
				if len(seen) != 2 || seen[0] != "vm" || seen[1] != "files" {
					t.Fatal("wrong cleanup order", seen)
				}
			case "running":
				if len(seen) != 1 || seen[0] != "vm" {
					t.Fatal("removed backing files of active guest", seen)
				}
			case "foreign":
				if len(seen) != 0 {
					t.Fatal("touched another workspace", seen)
				}
			}
			var retained domain.Project
			if err := store.Load("project", "other", &retained); err != nil {
				t.Fatal("removed another project", err)
			}
			loadError := store.Load("project", project.ID, &retained)
			if scenario == "foreign" {
				if loadError != nil {
					t.Fatal("deleted unverified scope", loadError)
				}
			} else if !errors.Is(loadError, sql.ErrNoRows) {
				t.Fatal("retained evaluation scope", loadError)
			}
			pending, err := store.PendingVMCleanup()
			if err != nil {
				t.Fatal(err)
			}
			wanted := 0
			if scenario == "running" {
				wanted = 1
			}
			if len(pending) != wanted {
				t.Fatal("lost or duplicated pending VM removal", pending)
			}
		})
	}
}
