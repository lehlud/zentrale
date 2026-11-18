package webapi

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/remote"
)

func TestWorkspaceFileStreamsOnlyProjectFiles(t *testing.T) {
	fixture := newAPIFixture(t)
	peer, err := testssh.StartWithCommandHandler(func(command string, _ io.Reader, stdout, stderr io.Writer) int {
		if !strings.Contains(command, "Stream a regular project file") {
			return 1
		}
		process := exec.Command("/bin/sh", "-c", command)
		process.Stdout, process.Stderr = stdout, stderr
		if err := process.Run(); err != nil {
			return 1
		}
		return 0
	})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	pdf := []byte("%PDF-1.7\n\x00\xff test bytes")
	for name, data := range map[string][]byte{"workspace/Exposé #1.pdf": pdf, "secret": []byte("private"), "workspace/large.txt": make([]byte, (2<<20)+1)} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(root, filepath.Join(workspace, "outside")); err != nil {
		t.Fatal(err)
	}
	store := fixture.engine.Store
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	public, err := remote.DecodePublicKey(peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}
	machine := domain.Machine{ID: "file-host", Host: peer.Host, Port: peer.Port, User: "test", KeyID: key.ID}
	project := domain.Project{ID: "project", MachineIDs: []string{machine.ID}, Directory: workspace}
	record := domain.Agent{ID: "agent", ProjectID: project.ID, MachineID: machine.ID, Role: "main"}
	if err := store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("agent", record.ID, project.ID, record); err != nil {
		t.Fatal(err)
	}
	get := func(projectID, filename, preview string, status int) (http.Header, []byte) {
		t.Helper()
		query := url.Values{"agent": {record.ID}, "path": {filename}, "preview": {preview}}
		response, err := fixture.client.Get(fixture.server.URL + "/workspace-file/" + projectID + "?" + query.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("%s: status %d, expected %d, error %v", filename, response.StatusCode, status, err)
		}
		return response.Header, data
	}
	get(project.ID, "Exposé #1.pdf", "pdf", http.StatusUnauthorized)
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.test"})}`, nil))
	for _, prefix := range []string{"", remote.ManagedGuestWorkspace + "/"} {
		headers, data := get(project.ID, prefix+"Exposé #1.pdf", "pdf", http.StatusOK)
		if !bytes.Equal(data, pdf) || headers.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(headers.Get("Content-Disposition"), "attachment;") {
			t.Fatalf("file response: %v %q", headers, data)
		}
	}
	get(project.ID, "../secret", "", http.StatusNotFound)
	get(project.ID, "outside/secret", "", http.StatusNotFound)
	get(project.ID, "large.txt", "text", http.StatusRequestEntityTooLarge)
	get("other", "Exposé #1.pdf", "", http.StatusNotFound)
}
