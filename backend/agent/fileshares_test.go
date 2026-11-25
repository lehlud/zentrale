package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
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
	project := domain.Agent{ID: "project-agent", Role: "main", ProjectID: "project"}
	if err := store.Save("agent", project.ID, project.ProjectID, project); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	dav := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		username, password, _ := r.BasicAuth()
		if username != "service-user" || password != "service-password" {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		const file = "/users/alice/notes.txt"
		if r.Method == "PROPFIND" && (r.URL.Path == "/users/" || r.URL.Path == "/users/alice/" || r.URL.Path == file) {
			resource := ""
			if strings.HasSuffix(r.URL.Path, "/") {
				resource = "<d:collection/>"
			}
			w.WriteHeader(http.StatusMultiStatus)
			fmt.Fprintf(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>%s</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype>%s</d:resourcetype></d:prop></d:propstat></d:response></d:multistatus>`, r.URL.Path, resource)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != file {
			t.Errorf("unexpected share request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "Hello Alice")
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
	public, err := engine.FileShares(false)
	if err != nil || len(public) != 1 || len(public[0]) != 2 || public[0]["id"] != share.ID {
		t.Fatalf("unexpected public share projection: %+v (%v)", public, err)
	}
}
