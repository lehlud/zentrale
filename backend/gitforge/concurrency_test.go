package gitforge

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

type concurrentTransport func(*http.Request) (*http.Response, error)

func (transport concurrentTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestSlowGitAccountDoesNotBlockSnapshotsOrOtherAccounts(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := New(store)
	for _, id := range []string{"github", "gitlab"} {
		if err := service.save(account{Public: domain.GitConnection{ID: id, Kind: id, ClientID: "app", UserName: "user", BaseURL: "https://" + id + ".com"}, AccessToken: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("repository_binding", "binding", "", repositoryBinding{ID: "binding", ConnectionID: "github", KeyID: key.ID}); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "local", Name: "Before", GitConnectionID: "github", RepositoryID: "repository"}
	source := domain.Source{ID: rootSourceID(project.ID), ProjectID: project.ID, ProjectRoot: true, BindingID: "binding", KeyID: key.ID}
	if err := store.SaveProjectRepository(project, &source); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	service.Client.HTTP = &http.Client{Transport: concurrentTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "api.github.com" {
			close(entered)
			select {
			case <-release:
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		}
		body := `[]`
		if strings.HasSuffix(request.URL.Path, "/user") {
			body = `{"id":1,"username":"user","login":"user"}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	slow := make(chan error, 1)
	go func() { _, err := service.Repositories(context.Background(), "github", "", 0); slow <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("slow request did not start")
	}
	fast := make(chan error, 1)
	go func() {
		_, err := service.Connections()
		if err == nil {
			_, err = service.Namespaces(context.Background(), "gitlab")
		}
		if err == nil {
			project.Name = "After"
			_, err = service.SaveProject(context.Background(), project)
		}
		fast <- err
	}()
	select {
	case err := <-fast:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Git snapshots or another account waited for remote request")
	}
	once.Do(func() { close(release) })
	if err := <-slow; err != nil {
		t.Fatal(err)
	}
}
