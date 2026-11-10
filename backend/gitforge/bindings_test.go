package gitforge

import (
	"slices"
	"testing"

	"github.com/lehlud/zentrale/datastore"
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
