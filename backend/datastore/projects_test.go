package datastore

import "testing"

func TestRemoveProjectCascadesAndPreservesOtherResources(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, item := range []struct {
		kind, id, parent string
		value            any
	}{
		{"project", "project", "", map[string]any{"id": "project"}},
		{"agent", "main", "project", map[string]any{"projectId": "project"}},
		{"agent", "child", "project", map[string]any{"projectId": "project", "parentId": "main"}},
		{"agent", "approver", "project", map[string]any{"projectId": "project", "role": "approver"}},
		{"message", "message", "child", map[string]any{"agentId": "child"}},
		{"plan", "plan", "main", map[string]any{"steps": []string{"work"}}},
		{"source", "source", "project", map[string]any{"projectId": "project"}},
		{"microvm", "vm", "editor-scope", map[string]any{"projectId": "project"}},
		{"project_storage", "storage", "project", map[string]any{}},
		{"project_workspace", "workspace", "project", map[string]any{}},
		{"project", "other", "", map[string]any{"id": "other"}},
		{"agent", "other-agent", "other", map[string]any{"projectId": "other"}},
		{"source", "other-source", "other", map[string]any{"projectId": "other"}},
		{"provider", "shared-provider", "", map[string]any{}},
		{"machine", "shared-machine", "", map[string]any{}},
	} {
		if err := store.Save(item.kind, item.id, item.parent, item.value); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"main", "child", "approver", "other-agent"} {
		if _, err := store.AppendEvent("test", id, "{}"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RemoveProject(""); err == nil {
		t.Fatal("empty project ID accepted")
	}
	if err := store.RemoveProject("project"); err != nil {
		t.Fatal(err)
	}
	rows, err := store.db.Query(`SELECT kind,id FROM resources ORDER BY kind,id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			t.Fatal(err)
		}
		if id != "other" && id != "other-agent" && id != "other-source" && id != "shared-provider" && id != "shared-machine" {
			t.Fatal("orphaned resource", kind, id)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatal("shared or other-project records removed", count)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := store.Events(0, 100)
	if err != nil || len(events) != 1 || events[0].AgentID != "other-agent" {
		t.Fatal("orphaned events", events, err)
	}
}

func TestRemoveProjectRollsBackAgentDeletionOnFailure(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save("project", "project", "", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("agent", "agent", "project", map[string]any{"projectId": "project"}); err != nil {
		t.Fatal(err)
	}
	// Fail after the agent DELETE, proving that it rolls back with the project.
	if _, err := store.db.Exec(`DROP TABLE events`); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveProject("project"); err == nil {
		t.Fatal("expected database failure")
	}
	for _, kind := range []string{"project", "agent"} {
		var value map[string]any
		if err := store.Load(kind, kind, &value); err != nil {
			t.Fatal("partial deletion", kind, err)
		}
	}
}
