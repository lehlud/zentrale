package webapi

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

// Match the lazy output query used by frontend/src/jobs-panel.tsx.
const jobDetailsQuery = `query($id:String!) { job(id:$id) { id output } }`

func TestJobDetailsQueryAuthenticatesAndLoadsByID(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	schema, err := (&Server{Store: store}).Schema()
	if err != nil {
		t.Fatal(err)
	}
	query := func(id string, authenticated bool) *graphql.Result {
		ctx := context.WithValue(context.Background(), requestKey{}, &requestState{authenticated: authenticated})
		return graphql.Do(graphql.Params{
			Schema: schema, RequestString: jobDetailsQuery, Context: ctx,
			VariableValues: map[string]any{"id": id},
		})
	}
	response := query("running-job", false)
	if !strings.Contains(fmt.Sprint(response.Errors), "authentication required") {
		t.Fatalf("expected authentication error, got %v", response)
	}

	jobs := []domain.Job{
		{ID: "running-job", AgentID: "agent", Status: "running", Output: "step 1/10\n"},
		{ID: "completed-job", AgentID: "other-agent", Status: "completed", Output: "finished\n"},
	}
	for _, job := range jobs {
		if err := store.Save("job", job.ID, job.AgentID, job); err != nil {
			t.Fatal(err)
		}
	}

	for _, job := range jobs {
		t.Run(job.Status, func(t *testing.T) {
			result := query(job.ID, true)
			if len(result.Errors) != 0 {
				t.Fatalf("job details query failed: %v", result.Errors)
			}
			details := result.Data.(map[string]any)["job"].(map[string]any)
			if len(details) != 2 || details["id"] != job.ID || details["output"] != job.Output {
				t.Fatalf("unexpected job details: %v", details)
			}
		})
	}

	t.Run("updated output", func(t *testing.T) {
		job := jobs[0]
		job.Output += "step 2/10\n"
		if err := store.Save("job", job.ID, job.AgentID, job); err != nil {
			t.Fatal(err)
		}
		result := query(job.ID, true)
		if len(result.Errors) != 0 {
			t.Fatalf("updated job query failed: %v", result.Errors)
		}
		if details := result.Data.(map[string]any)["job"].(map[string]any); details["output"] != job.Output {
			t.Fatalf("job query returned stale output: %v", details)
		}
	})
}
