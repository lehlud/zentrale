package datastore

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestRecoverySelectsDurableWorkInExistingDatabase(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "existing.sqlite")
	store, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Simulate a database created before the recovery index was introduced.
	if _, err := store.db.Exec("DROP INDEX resource_recovery"); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []domain.Agent{
		{ID: "interrupted", Role: "main", Status: "interrupted"},
		{ID: "open-input", Role: "main", Status: "idle"},
		{ID: "active-goal", Role: "main", Status: "idle"},
		{ID: "explicit-stop", Role: "main", Status: "stopped"},
		{ID: "finished", Role: "main", Status: "idle"},
		{ID: "reviewer", Role: "approver", Status: "interrupted"},
	} {
		if err := store.Save("agent", agent.ID, agent.ProjectID, agent); err != nil {
			t.Fatal(err)
		}
	}
	writes := []ResourceWrite{
		{Kind: "goal", ID: "active-goal", Value: domain.Goal{Status: "active"}},
		{Kind: "agent_stop", ID: "explicit-stop", Value: map[string]bool{"requested": true}},
		{Kind: "agent_stop", ID: "finished", Value: map[string]bool{"requested": false}},
		{Kind: "agent_input", ID: "pending", Parent: "open-input", Value: domain.AgentInput{ID: "pending", Status: "pending"}},
		{Kind: "agent_input", ID: "paused", Parent: "open-input", Value: domain.AgentInput{ID: "paused", Status: "paused"}},
		{Kind: "agent_input", ID: "completed", Parent: "finished", Value: domain.AgentInput{Status: "completed"}},
		{Kind: "job", ID: "running", Parent: "open-input", Value: domain.Job{ID: "running", Status: "running", Output: "retained"}},
		{Kind: "job", ID: "done", Parent: "finished", Value: domain.Job{ID: "done", Status: "completed", Output: "retained"}},
	}
	if err := store.SaveResources(writes); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err := reopened.AgentsForRecovery()
	want := []string{"interrupted", "open-input", "active-goal", "explicit-stop"}
	var ids []string
	for _, row := range rows {
		ids = append(ids, row["id"].(string))
	}
	if err != nil || !slices.Equal(ids, want) {
		t.Fatal(ids, err)
	}
	inputs, err := reopened.RecoverableAgentInputs("open-input")
	if err != nil || len(inputs) != 1 || inputs[0].ID != "pending" {
		t.Fatal(inputs, err)
	}
	inputs, err = reopened.AgentInputs("open-input")
	if err != nil || len(inputs) != 2 || inputs[1].ID != "paused" {
		t.Fatal(inputs, err)
	}
	jobs, err := reopened.UnfinishedWork("job")
	if err != nil || len(jobs) != 1 || jobs[0]["id"] != "running" || jobs[0]["status"] != "running" {
		t.Fatal(jobs, err)
	}
	for id, status := range map[string]string{"running": "interrupted", "done": "completed"} {
		if err := reopened.InterruptJob(id); err != nil {
			t.Fatal(err)
		}
		var job domain.Job
		if err := reopened.Load("job", id, &job); err != nil || job.Status != status || job.Output != "retained" {
			t.Fatal(job, err)
		}
	}
}
