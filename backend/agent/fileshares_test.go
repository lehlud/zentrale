package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestChatFileShareUsesOwnerScopeWithoutRemoteEngine(t *testing.T) {
	store, err := datastore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	account := datastore.Account{User: domain.User{ID: "a", Username: "alice", Role: domain.RoleUser}, PasswordHash: "test-only"}
	if err := store.InsertAccount(account); err != nil {
		t.Fatal(err)
	}
	chat := domain.Agent{ID: "chat", Role: "main", ChatOwnerID: account.ID}
	if err := store.Save("agent", chat.ID, "", chat); err != nil {
		t.Fatal(err)
	}
	bobAccount := datastore.Account{User: domain.User{ID: "b", Username: "bob", Role: domain.RoleUser}, PasswordHash: "test-only"}
	if err := store.InsertAccount(bobAccount); err != nil {
		t.Fatal(err)
	}
	bobChat := domain.Agent{ID: "bob-chat", Role: "main", ChatOwnerID: bobAccount.ID}
	if err := store.Save("agent", bobChat.ID, "", bobChat); err != nil {
		t.Fatal(err)
	}

	project := domain.Agent{ID: "project-agent", Role: "main", ProjectID: "project"}
	if err := store.Save("agent", project.ID, project.ProjectID, project); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	var bobRootChecked atomic.Bool
	dav := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		username, password, _ := r.BasicAuth()
		if username != "service-user" || password != "service-password" {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		path := r.URL.Path
		aliceFile, bobFile := path == "/users/alice/notes.txt", path == "/users/bob/notes.txt"
		if r.Method == "PROPFIND" && (path == "/users/" || path == "/users/alice/" || path == "/users/bob/" || aliceFile || bobFile) {
			if path == "/users/bob/" {
				bobRootChecked.Store(true)
			}
			resource := ""
			if strings.HasSuffix(r.URL.Path, "/") {
				resource = "<d:collection/>"
			}
			w.WriteHeader(http.StatusMultiStatus)
			fmt.Fprintf(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>%s</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype>%s</d:resourcetype></d:prop></d:propstat></d:response></d:multistatus>`, r.URL.Path, resource)
			return
		}
		if r.Method != http.MethodGet || (!aliceFile && !bobFile) {
			t.Errorf("unexpected share request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if aliceFile {
			fmt.Fprint(w, "Hello Alice")
		} else {
			fmt.Fprint(w, "Hello Bob")
		}
	}))
	t.Cleanup(dav.Close)

	// No connector or execution subsystem exists in this fixture.
	engine := &Engine{Store: store}
	password := "service-password"
	share, err := engine.SaveFileShare(FileShareConfig{
		Name: "Documents", Protocol: "webdav", Endpoint: dav.URL, Root: "users/{username}", Username: "service-user",
	}, &password)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("saving share configuration opened a remote connection")
	}

	draft := share
	draft.ID, draft.Name = "", "Unsaved Bob share"
	draft.Restricted, draft.UserIDs = true, []string{bobAccount.ID}
	if _, err := engine.CheckFileShare(t.Context(), draft, &password, account.ID); err == nil || requests.Load() != 0 {
		t.Fatalf("ungranted test user reached the server: %v, requests=%d", err, requests.Load())
	}
	if checked, err := engine.CheckFileShare(t.Context(), draft, &password, bobAccount.ID); err != nil || checked.Entries != 0 || !bobRootChecked.Load() {
		t.Fatalf("draft root check: %+v, %v, requests=%d", checked, err, requests.Load())
	}
	if rows, err := engine.FileShares(); err != nil || len(rows) != 1 || rows[0]["name"] != share.Name {
		t.Fatalf("draft check changed the saved shares: %+v, %v", rows, err)
	}

	changed := share
	changed.Endpoint += "/other"
	before := requests.Load()
	if _, err := engine.CheckFileShare(t.Context(), changed, nil, account.ID); err == nil || requests.Load() != before {
		t.Fatalf("stored password followed a changed endpoint: %v, requests=%d", err, requests.Load())
	}

	if _, err := engine.ReadChatFile(context.Background(), project.ID, share.ID, "notes.txt", 1024); err == nil {
		t.Fatal("project agent accessed a fileshare")
	}
	if _, err := engine.ReadChatFile(context.Background(), chat.ID, share.ID, "../bob/notes.txt", 1024); err == nil {
		t.Fatal("chat accessed another user's folder")
	}
	data, err := engine.ReadChatFile(context.Background(), chat.ID, share.ID, "notes.txt", 1024)
	if err != nil || string(data) != "Hello Alice" {
		t.Fatalf("read from user's share without remote engine: %q, %v", data, err)
	}
	data, err = engine.ReadChatFile(context.Background(), bobChat.ID, share.ID, "notes.txt", 1024)
	if err != nil || string(data) != "Hello Bob" {
		t.Fatalf("second user's share did not get a separate root: %q, %v", data, err)
	}

	public, err := engine.chatFileShares(chat.ID)
	if err != nil || len(public) != 1 || public[0].ID != share.ID || public[0].Protocol != "webdav" {
		t.Fatalf("unexpected public share projection: %+v (%v)", public, err)
	}

	token := "domainshare-fixture-token"
	domainShare, err := engine.SaveFileShare(FileShareConfig{
		Name: "Domain documents", Protocol: "domainshare", Endpoint: "https://files.example.org", Username: "not-the-owner",
	}, &token)
	if err != nil || domainShare.Username != "" {
		t.Fatalf("DomainShare definition must not have a service username: %+v, %v", domainShare, err)
	}
	shareToolNames := func(chatID string) string {
		var names []string
		for _, tool := range engine.shortChatTools(chatID) {
			if strings.HasPrefix(tool.Name, "share_") {
				names = append(names, tool.Name)
			}
		}
		return strings.Join(names, ",")
	}
	for _, owner := range []domain.Agent{chat, bobChat} {
		_, username, err := engine.chatFileShare(owner.ID, domainShare.ID)
		expected := map[string]string{chat.ID: "alice", bobChat.ID: "bob"}[owner.ID]
		if err != nil || username != expected {
			t.Fatalf("DomainShare used a different principal: %q, %v", username, err)
		}
	}
	if _, err := engine.SearchChatFiles(context.Background(), project.ID, domainShare.ID, "notes", ""); err == nil {
		t.Fatal("project agent could search a DomainShare")
	}
	if names := shareToolNames(project.ID); names != "" {
		t.Fatalf("project advertises share tools: %s", names)
	}

	domainShare.Restricted, domainShare.UserIDs = true, []string{account.ID, account.ID}
	domainShare, err = engine.SaveFileShare(domainShare, nil)
	if err != nil || len(domainShare.UserIDs) != 1 {
		t.Fatalf("saving restricted share: %+v, %v", domainShare, err)
	}
	if names := shareToolNames(chat.ID); names != "share_list,share_directory,share_read,share_search" {
		t.Fatalf("DomainShare tools missing in existing chat: %s", names)
	}
	if names := shareToolNames(bobChat.ID); names != "share_list,share_directory,share_read" {
		t.Fatalf("Bob's WebDAV-only tool catalog: %s", names)
	}

	list, err := engine.shortChatTool(t.Context(), bobChat, providers.Call{Name: "share_list"})
	if err != nil || len(list.([]chatFileShareInfo)) != 1 || list.([]chatFileShareInfo)[0].ID != share.ID {
		t.Fatalf("hidden share was discoverable by Bob: %+v, %v", list, err)
	}
	for _, name := range []string{"share_directory", "share_search", "share_read"} {
		forged := providers.Call{Name: name, Arguments: map[string]any{
			"shareId": domainShare.ID, "path": "notes.txt", "query": "notes",
		}}
		if _, err := engine.shortChatTool(t.Context(), bobChat, forged); !errors.Is(err, errChatShareUnavailable) {
			t.Errorf("%s did not reject Bob before connection: %v", name, err)
		}
	}

	account.Disabled = true
	if err := store.UpdateAccount(account, true); err != nil {
		t.Fatal(err)
	}
	if names := shareToolNames(chat.ID); names != "" {
		t.Fatalf("disabled owner has share tools: %s", names)
	}
	if _, err := engine.ReadChatFile(t.Context(), chat.ID, share.ID, "notes.txt", 1024); err == nil {
		t.Fatal("disabled owner could still read files")
	}

	domainShare.Endpoint = "https://other.example.org"
	if _, err := engine.SaveFileShare(domainShare, nil); err == nil {
		t.Fatal("saved token followed a changed endpoint")
	}
}
