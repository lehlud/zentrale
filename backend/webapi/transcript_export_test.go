package webapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func TestTranscriptExportAuthorizationAndCompleteTree(t *testing.T) {
	admin, member, memberID := settingsAccounts(t)
	store := admin.engine.Store
	save := func(kind, id, parent string, value any) {
		t.Helper()
		if err := store.Save(kind, id, parent, value); err != nil {
			t.Fatal(err)
		}
	}
	project := domain.Project{ID: "project", Name: "Archived project", OwnerID: domain.InitialUserID, MemberIDs: []string{memberID}, Archived: true}
	save("project", project.ID, "", project)
	threads := []domain.Agent{
		{ID: "root", Name: "Main", ProjectID: project.ID, Role: "main"},
		{ID: "child", Name: "Child", ProjectID: project.ID, ParentID: "root", Role: "subagent"},
		{ID: "grandchild", Name: "Grandchild", ProjectID: project.ID, ParentID: "child", Role: "subagent"},
		{ID: "review", Name: "Reviewer", ProjectID: project.ID, ParentID: "root", Role: "approver"},
		{ID: "other", Name: "Separate thread", ProjectID: project.ID, Role: "main"},
		{ID: "outside", Name: "Other project", ProjectID: "different-project", ParentID: "root", Role: "subagent"},
		{ID: "chat", Name: "Private chat", ChatOwnerID: memberID, Role: "main"},
	}
	for _, thread := range threads {
		save("agent", thread.ID, thread.ProjectID, thread)
	}

	for n := range 503 {
		id := fmt.Sprintf("message-%04d", n)
		save("message", id, "root", domain.Message{ID: id, AgentID: "root", Role: "user", Content: id, CreatedAt: time.Unix(int64(n), 0).UTC()})
	}

	attachment := domain.Attachment{ID: "notes", AgentID: "grandchild", ProjectID: project.ID, Name: "notes.txt", MimeType: "text/plain", Size: 5}
	if err := store.SaveAttachment(attachment, []byte("notes")); err != nil {
		t.Fatal(err)
	}
	missing := domain.Attachment{ID: "legacy", AgentID: "grandchild", Name: "legacy.txt"}
	save("message", "nested-message", "grandchild", domain.Message{
		ID: "nested-message", AgentID: "grandchild", Role: "user", Content: "Nested transcript",
		Attachments: []domain.Attachment{attachment, missing},
	})
	save("file_change", "diff-1", "grandchild", map[string]any{"id": "diff-1", "messageId": "nested-message", "path": "notes.txt", "patch": "shown diff"})
	save("tool_activity", "nested-message", "grandchild", map[string]any{"id": "nested-message", "activities": []map[string]any{{"name": "example", "content": "activity"}}})

	get := func(client *http.Client, id string, status int) transcriptArchive {
		t.Helper()
		response, err := client.Get(admin.server.URL + "/transcript-export/" + id)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != status || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("export %s: HTTP %d, cache %q, expected %d", id, response.StatusCode, response.Header.Get("Cache-Control"), status)
		}

		var archive transcriptArchive
		if status == http.StatusOK {
			if err := json.NewDecoder(response.Body).Decode(&archive); err != nil {
				t.Fatal(err)
			}
		}
		return archive
	}

	get(http.DefaultClient, "root", http.StatusUnauthorized)
	get(admin.client, "chat", http.StatusNotFound)
	chat := get(member.client, "chat", http.StatusOK)
	if len(chat.Threads) != 1 || chat.Threads[0].ID != "chat" || chat.ProjectID != "" {
		t.Fatalf("standalone archive has unexpected scope: %+v", chat)
	}

	archive := get(member.client, "root", http.StatusOK)
	ids := make([]string, 0, len(archive.Threads))
	for _, thread := range archive.Threads {
		ids = append(ids, thread.ID)
	}
	if !slices.Equal(ids, []string{"root", "child", "grandchild", "review"}) {
		t.Fatalf("unexpected descendant tree: %v", ids)
	}
	messages := archive.Threads[0].Messages
	if len(messages) != 503 || messages[0]["content"] != "message-0000" || messages[502]["content"] != "message-0502" {
		t.Fatalf("export did not retain the full ordered transcript: %d messages", len(messages))
	}

	nested := archive.Threads[2].Messages[0]
	if nested["content"] != "Nested transcript" || nested["change"].(map[string]any)["patch"] != "shown diff" || nested["activities"].([]any)[0].(map[string]any)["content"] != "activity" {
		t.Fatalf("missing saved diff or activity: %+v", nested)
	}
	files := archive.Attachments["grandchild"]
	if legacy, exists := files["legacy"]; string(files["notes"]) != "notes" || !exists || legacy != nil {
		t.Fatalf("unexpected archived attachments: %v", files)
	}

	project.MemberIDs = nil
	save("project", project.ID, "", project)
	get(member.client, "root", http.StatusNotFound)
	get(member.client, "grandchild", http.StatusNotFound)
}
