package gitforge

import (
	"context"
	"slices"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

func TestRepositoryHTTPURLsStayBoundToTheSelectedSSHRepository(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := New(store)
	binding := repositoryBinding{
		ID: "selected", BaseURL: "https://forge.example:8443/gitlab/",
		FullName: "team/demo", SSHURL: "ssh://git@ssh.example:2222/team/demo.git",
	}
	if err := store.Save("repository_binding", binding.ID, "", binding); err != nil {
		t.Fatal(err)
	}

	urls, err := service.RepositoryHTTPURLs(binding.ID, binding.SSHURL)
	if err != nil || !slices.Equal(urls, []string{"https://forge.example:8443/gitlab/team/demo"}) {
		t.Fatal(urls, err)
	}
	if _, err := service.RepositoryHTTPURLs(binding.ID, "ssh://git@ssh.example:2222/team/other.git"); err == nil {
		t.Fatal("binding aliases were assigned to another SSH repository")
	}
	if _, err := service.RepositoryHTTPURLs("missing", binding.SSHURL); err == nil {
		t.Fatal("missing repository binding was accepted")
	}
	if urls, err := service.RepositoryHTTPURLs("", binding.SSHURL); err != nil || len(urls) != 0 {
		t.Fatal("manual sources acquired unconfigured aliases", urls, err)
	}
}

func TestSelectingMovedRepositoryRetainsThePreviousCheckoutOrigin(t *testing.T) {
	fixture := newForgeFixture(t, "github")
	fixture.connect(t)
	project := domain.Project{ID: "one", Name: "One", GitConnectionID: "github", RepositoryID: "team/demo"}
	project, err := fixture.service.SaveProject(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	var before, after domain.Source
	if err := fixture.service.Store.Load("source", rootSourceID(project.ID), &before); err != nil {
		t.Fatal(err)
	}
	fixture.repositoryOwner = "moved"
	project.RepositoryID = "moved/demo"
	if _, err := fixture.service.SaveProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Store.Load("source", rootSourceID(project.ID), &after); err != nil {
		t.Fatal(err)
	}
	httpURLs, movedFrom, err := fixture.service.RepositoryCheckoutURLs(after.BindingID, after.URL)
	if err != nil || before.BindingID != after.BindingID || before.KeyID != after.KeyID ||
		!slices.Equal(httpURLs, []string{"https://github.com/moved/demo"}) ||
		!slices.Equal(movedFrom, []string{before.URL, "https://github.com/team/demo"}) {
		t.Fatalf("checkout relocation: before=%+v after=%+v current=%v former=%v error=%v", before, after, httpURLs, movedFrom, err)
	}
}
