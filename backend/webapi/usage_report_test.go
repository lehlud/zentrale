package webapi

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

func TestUsageReportViewerScope(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, project := range []domain.Project{
		{ID: "bob-project", OwnerID: "bob", MemberIDs: []string{"alice"}},
		{ID: "bob-private", OwnerID: "bob"},
	} {
		if err := store.Save("project", project.ID, "", project); err != nil {
			t.Fatal(err)
		}
	}
	for _, agent := range []domain.Agent{
		{ID: "alice-chat", ChatOwnerID: "alice"},
		{ID: "bob-chat", ChatOwnerID: "bob"},
		{ID: "bob-agent", ProjectID: "bob-project"},
	} {
		usage := domain.Usage{ID: agent.ID, AgentID: agent.ID, ModelID: agent.ID, InputTokens: 42}
		if err := store.SaveUsage(usage, agent); err != nil {
			t.Fatal(err)
		}
	}

	builder := newSchemaBuilder(&Server{Store: store})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{Query: graphql.NewObject(graphql.ObjectConfig{
		Name: "UsageReportTestQuery", Fields: graphql.Fields{"usageReport": builder.usageReportField()},
	})})
	if err != nil {
		t.Fatal(err)
	}

	alice := domain.User{ID: "alice", Role: domain.RoleUser}
	admin := domain.User{ID: "admin", Role: domain.RoleAdmin}
	disabled := domain.User{ID: "alice", Role: domain.RoleUser, Disabled: true}
	tests := []struct {
		name       string
		user       domain.User
		argument   string
		wantModels []string
		wantError  error
	}{
		{name: "anonymous", wantError: errAuthenticationRequired},
		{name: "disabled", user: disabled, wantError: errAuthenticationRequired},
		{name: "regular account", user: alice, wantError: errAdministratorRequired},
		{name: "other account", user: alice, argument: `(userId: "bob")`, wantError: errAdministratorRequired},
		{name: "shared project does not grant dashboard access", user: alice, argument: `(projectId: "bob-project")`, wantError: errAdministratorRequired},
		{name: "nonmember project", user: alice, argument: `(projectId: "bob-private")`, wantError: errAdministratorRequired},
		{name: "admin all usage", user: admin, wantModels: []string{"alice-chat", "bob-agent", "bob-chat"}},
		{name: "admin billing metadata without membership", user: admin, argument: `(userId: "bob", projectId: "bob-project")`, wantModels: []string{"bob-agent"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.WithValue(t.Context(), requestKey{}, &requestState{authenticated: test.user.ID != "", user: test.user})
			result := graphql.Do(graphql.Params{
				Context: ctx, Schema: schema,
				RequestString: `{ usageReport` + test.argument + ` { modelId inputTokens } }`,
			})
			if test.wantError != nil {
				if len(result.Errors) != 1 || result.Errors[0].Message != test.wantError.Error() {
					t.Fatalf("got GraphQL errors %v, want %v", result.Errors, test.wantError)
				}
				return
			}
			if len(result.Errors) != 0 {
				t.Fatalf("GraphQL errors: %v", result.Errors)
			}

			rows := result.Data.(map[string]any)["usageReport"].([]any)
			models := make([]string, 0, len(rows))
			for _, item := range rows {
				row := item.(map[string]any)
				if row["inputTokens"] != 42 {
					t.Errorf("unexpected token count: %#v", row)
				}
				models = append(models, row["modelId"].(string))
			}
			if !reflect.DeepEqual(models, test.wantModels) {
				t.Fatalf("visible models: %v; want %v", models, test.wantModels)
			}
		})
	}
}

func TestUsageDateRangeUsesUTCAndEnforcesBounds(t *testing.T) {
	now := time.Date(2026, time.April, 2, 23, 15, 0, 0, time.FixedZone("west", -7*60*60))
	from, to, err := usageDateRange(now, "", "")
	if err != nil || from.Format(time.DateOnly) != "2026-03-05" || to.Format(time.DateOnly) != "2026-04-04" {
		t.Fatalf("unexpected default UTC dates: %v %v %v", from, to, err)
	}

	for _, dates := range [][2]string{
		{"2026-04-03T00:00:00Z", "2026-04-04"},
		{"2026-04-03", "2026-04-03"},
		{"2026-04-04", "2026-04-03"},
		{"2025-04-01", "2026-04-03"},
	} {
		if _, _, err := usageDateRange(now, dates[0], dates[1]); err != errUsageDateRange {
			t.Errorf("accepted invalid date range %q: %v", dates, err)
		}
	}
}
