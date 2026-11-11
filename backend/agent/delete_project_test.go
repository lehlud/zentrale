package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestProjectDeletionIncludesAllAgentFamiliesAndSources(t *testing.T) {
	engine, main := testEngine(t, nil)
	child, err := engine.Create(main.ProjectID, "Child", main.ProviderID, "approver-provider", main.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Create(main.ProjectID, "Another conversation", main.ProviderID, "approver-provider", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.appendMessage(child.ID, "user", "private history", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Save("source", "root-source", main.ProjectID, domain.Source{ID: "root-source", ProjectID: main.ProjectID, ProjectRoot: true}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Save("source", "extra-source", main.ProjectID, domain.Source{ID: "extra-source", ProjectID: main.ProjectID}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Save("project", "unrelated", "", domain.Project{ID: "unrelated"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Delete("project", main.ProjectID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{main.ID, main.ApproverID, child.ID, child.ApproverID, second.ID, second.ApproverID} {
		var record domain.Agent
		if err := engine.Store.Load("agent", id, &record); err == nil {
			t.Fatal("orphaned agent", id)
		}
		history, err := engine.loadHistory(id)
		if err != nil || len(history) != 0 {
			t.Fatal("orphaned history", id, err)
		}
	}
	for _, kind := range []string{"project", "source"} {
		if kind == "project" {
			var record domain.Project
			if err := engine.Store.Load(kind, main.ProjectID, &record); err == nil {
				t.Fatal("project retained")
			}
		} else {
			items, err := engine.listAll(kind, &main.ProjectID)
			if err != nil || len(items) != 0 {
				t.Fatal("orphaned sources", items, err)
			}
		}
	}
	for _, item := range []struct{ kind, id string }{{"project", "unrelated"}, {"machine", "test-worker"}, {"provider", main.ProviderID}} {
		var value map[string]any
		if err := engine.Store.Load(item.kind, item.id, &value); err != nil {
			t.Fatal("shared record removed", item, err)
		}
	}
}

func TestProjectDeletionRejectsBusyWorkBeforeDeletingAnyFamily(t *testing.T) {
	for _, busy := range []string{"worker", "job", "preparation", "editor"} {
		t.Run(busy, func(t *testing.T) {
			engine, main := testEngine(t, nil)
			other, err := engine.Create(main.ProjectID, "Idle", main.ProviderID, "approver-provider", "")
			if err != nil {
				t.Fatal(err)
			}
			engine.mu.Lock()
			switch busy {
			case "worker":
				engine.running[main.ID] = context.CancelFunc(func() {})
			case "job":
				engine.jobs["job"] = &runningJob{job: domain.Job{AgentID: main.ID}, cancel: func() {}}
			case "preparation":
				engine.preparing[main.ID] = 1
			case "editor":
				engine.editorPreparing[main.ProjectID] = 1
			}
			engine.mu.Unlock()
			err = engine.Delete("project", main.ProjectID)
			engine.mu.Lock()
			delete(engine.running, main.ID)
			delete(engine.jobs, "job")
			delete(engine.preparing, main.ID)
			delete(engine.editorPreparing, main.ProjectID)
			engine.mu.Unlock()
			if err == nil || (!strings.Contains(err.Error(), "stop the project") && !strings.Contains(err.Error(), "editor is still starting")) {
				t.Fatal("busy project was removed", err)
			}
			for _, id := range []string{main.ID, other.ID, main.ApproverID, other.ApproverID} {
				var record domain.Agent
				if err := engine.Store.Load("agent", id, &record); err != nil {
					t.Fatal("partial deletion", id, err)
				}
			}
			if err := engine.Delete("project", main.ProjectID); err != nil {
				t.Fatal("idle retry failed", err)
			}
		})
	}
}
