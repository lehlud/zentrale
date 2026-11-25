package datastore

import (
	"fmt"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func newUsageReportStore(t *testing.T) *Instance {
	t.Helper()
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mustSaveUsageResource(t *testing.T, store *Instance, kind, id, parent string, value any) {
	t.Helper()
	if err := store.Save(kind, id, parent, value); err != nil {
		t.Fatal(err)
	}
}

func TestUsageReportAggregatesExactUTCDaysAndKnownCost(t *testing.T) {
	store := newUsageReportStore(t)
	chat := domain.Agent{ID: "chat", ChatOwnerID: "one"}
	start := time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 2)
	usage := domain.Usage{
		AgentID: chat.ID, ProviderID: "provider", ModelID: "model", Purpose: "turn",
		InputTokens: 100, CachedTokens: 20, OutputTokens: 9, CacheWriteTokens: 10, CacheWriteLongTokens: 3,
		TokensKnown: true, CacheReadKnown: true, CacheWriteKnown: true, CostKnown: true, Estimated: true, RealCost: .5,
	}
	save := func(id, createdAt string, usage domain.Usage) {
		t.Helper()
		usage.ID = id
		if err := store.SaveUsage(usage, chat); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`UPDATE resources SET created_at=? WHERE kind='usage' AND id=?`, createdAt, id); err != nil {
			t.Fatal(err)
		}
	}

	for index := range 505 {
		save(fmt.Sprint(index), "2026-04-03 00:00:00.000+00:00", usage)
	}
	unknown := usage
	unknown.TokensKnown, unknown.CacheReadKnown, unknown.CacheWriteKnown, unknown.CostKnown = false, false, false, false
	unknown.RealCost, unknown.FictionalCost = 99, 99
	save("unknown", "2026-04-03 23:59:59.999+00:00", unknown)

	fictional := usage
	fictional.RealCost, fictional.FictionalCost = 0, 2.5
	save("fictional", "2026-04-03 23:59:59.999+00:00", fictional)
	save("day-two", "2026-04-04 00:00:00.000+00:00", usage)
	save("end-exclusive", "2026-04-05 00:00:00.000+00:00", usage)

	otherPurpose := usage
	otherPurpose.Purpose = "summary"
	save("different-purpose", "2026-04-03 00:00:00.000+00:00", otherPurpose)

	rows, err := store.UsageReport(t.Context(), UsageReportFilter{From: start, To: end, UserID: chat.ChatOwnerID})
	if err != nil || len(rows) != 3 {
		t.Fatalf("unexpected report groups: %#v, %v", rows, err)
	}
	if rows[0].Day != "2026-04-04" || rows[1].Purpose != "summary" || rows[2].RequestCount != 507 {
		t.Fatalf("unexpected report grouping/order: %#v", rows)
	}

	want := domain.UsageReportRow{
		Day: "2026-04-03", ProviderID: "provider", ModelID: "model", UserID: "one", Purpose: "turn",
		InputTokens: 50700, CachedTokens: 10140, UncachedTokens: 40560, OutputTokens: 4563,
		CacheWriteTokens: 5070, CacheWriteLongTokens: 1521, RealCost: 252.5, FictionalCost: 2.5,
		UnknownPrices: 1, EstimatedCosts: 506, UnknownTokenCounts: 1, UnknownCacheReads: 1, UnknownCacheWrites: 1, RequestCount: 507,
	}
	if rows[2] != want {
		t.Fatalf("unexpected usage totals:\ngot  %#v\nwant %#v", rows[2], want)
	}
}

func TestUsageReportSnapshotsOwnersAndSafelyResolvesLegacyRows(t *testing.T) {
	store := newUsageReportStore(t)
	project := domain.Project{ID: "project", OwnerID: "old-owner"}
	agent := domain.Agent{ID: "project-agent", ProjectID: project.ID, ChatOwnerID: "not-project-owner"}
	mustSaveUsageResource(t, store, "project", project.ID, "", project)
	mustSaveUsageResource(t, store, "agent", agent.ID, project.ID, agent)

	usage := domain.Usage{ID: "snapshot", AgentID: agent.ID, ModelID: "snapshot"}
	if err := store.SaveUsage(usage, agent); err != nil {
		t.Fatal(err)
	}
	project.OwnerID = "current-owner"
	mustSaveUsageResource(t, store, "project", project.ID, "", project)
	legacyProject := domain.Project{ID: "legacy-project"}
	mustSaveUsageResource(t, store, "project", legacyProject.ID, "", legacyProject)
	mustSaveUsageResource(t, store, "agent", "legacy-agent", legacyProject.ID, domain.Agent{ID: "legacy-agent", ProjectID: legacyProject.ID})
	mustSaveUsageResource(t, store, "agent", "chat", "", domain.Agent{ID: "chat", ChatOwnerID: "chat-owner"})
	mustSaveUsageResource(t, store, "agent", "orphan-agent", "gone-project", domain.Agent{ID: "orphan-agent", ProjectID: "gone-project"})

	for id, parent := range map[string]string{
		"legacy-explicit-owner": agent.ID,
		"legacy-implicit-owner": "legacy-agent",
		"legacy-chat":           "chat",
		"missing-agent":         "gone-agent",
		"missing-project":       "orphan-agent",
	} {
		mustSaveUsageResource(t, store, "usage", id, parent, domain.Usage{ID: id, AgentID: parent, ModelID: id})
	}

	now := time.Now().UTC()
	rows, err := store.UsageReport(t.Context(), UsageReportFilter{From: now.AddDate(0, 0, -1), To: now.AddDate(0, 0, 1)})
	if err != nil || len(rows) != 6 {
		t.Fatalf("unexpected report: %#v, %v", rows, err)
	}
	want := map[string]usageAttribution{
		"snapshot":              {UserID: "old-owner", ProjectID: project.ID},
		"legacy-explicit-owner": {UserID: "current-owner", ProjectID: project.ID},
		"legacy-implicit-owner": {UserID: legacyProject.Owner(), ProjectID: legacyProject.ID},
		"legacy-chat":           {UserID: "chat-owner"},
		"missing-agent":         {},
		"missing-project":       {},
	}
	for _, row := range rows {
		got := usageAttribution{UserID: row.UserID, ProjectID: row.ProjectID}
		if got != want[row.ModelID] {
			t.Errorf("%s attributed to %#v, want %#v", row.ModelID, got, want[row.ModelID])
		}
	}
}

func TestUsageReportSurvivesProjectAndChatDeletion(t *testing.T) {
	store := newUsageReportStore(t)
	project := domain.Project{ID: "project", OwnerID: "project-owner"}
	mustSaveUsageResource(t, store, "project", project.ID, "", project)
	projectAgent := domain.Agent{ID: "project-agent", ProjectID: project.ID}
	chat := domain.Agent{ID: "chat", ChatOwnerID: "chat-owner"}

	for _, agent := range []domain.Agent{projectAgent, chat} {
		mustSaveUsageResource(t, store, "agent", agent.ID, agent.ProjectID, agent)
		mustSaveUsageResource(t, store, "message", agent.ID, agent.ID, domain.Message{Content: "private prompt"})
		usage := domain.Usage{ID: agent.ID, AgentID: agent.ID, TraceID: "trace", InputTokens: 20}
		if agent.ProjectID == "" {
			if err := store.SaveUsage(usage, agent); err != nil {
				t.Fatal(err)
			}
		} else {
			// The existing agent/project can resolve this legacy row at deletion.
			mustSaveUsageResource(t, store, "usage", usage.ID, agent.ID, usage)
		}
	}

	if err := store.RemoveProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveAgents([]string{chat.ID}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for id, owner := range map[string]string{projectAgent.ID: project.Owner(), chat.ID: chat.ChatOwnerID} {
		rows, err := store.UsageReport(t.Context(), UsageReportFilter{From: now.AddDate(0, 0, -1), To: now.AddDate(0, 0, 1), UserID: owner})
		if err != nil || len(rows) != 1 || rows[0].InputTokens != 20 || rows[0].UserID != owner {
			t.Fatalf("usage history for %s: %#v, %v", owner, rows, err)
		}
		var parent string
		var raw map[string]any
		if err := store.Load("usage", id, &raw); err != nil {
			t.Fatal(err)
		}
		if err := store.db.QueryRow(`SELECT parent_id FROM resources WHERE kind='usage' AND id=?`, id).Scan(&parent); err != nil {
			t.Fatal(err)
		}
		if raw["agentId"] != nil || raw["traceId"] != nil || parent != "" || store.Load("message", id, &raw) == nil {
			t.Fatalf("expected usage without a deleted agent, trace, or message: %#v", raw)
		}
	}
}
