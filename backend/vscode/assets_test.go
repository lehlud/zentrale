package vscode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func webArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	bundle := tar.NewWriter(compressed)
	for name, content := range files {
		if err := bundle.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := bundle.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := bundle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestWorkbenchInstallsOnceAndServesUIWithoutRemoteTransport(t *testing.T) {
	archive := webArchive(t, map[string]string{
		"vscode-web/" + workbenchModule:  "export const create = () => {};",
		"vscode-web/out/nls.messages.js": "globalThis._VSCODE_NLS_MESSAGES=[];",
		"vscode-web/LICENSE":             "Upstream license",
	})
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)
		_, _ = writer.Write(archive)
	}))
	defer server.Close()
	cache := t.TempDir()
	workbench := NewWorkbench(cache)
	digest := sha256.Sum256(archive)
	workbench.release.Web = Distribution{URL: server.URL, SHA256: hex.EncodeToString(digest[:])}
	var workers sync.WaitGroup
	for range 6 {
		workers.Go(func() {
			if err := workbench.Ensure(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if downloads.Load() != 1 {
		t.Fatalf("downloaded %d times", downloads.Load())
	}
	// A fresh instance reads the installed cache, even with no download service.
	server.Close()
	reopened := NewWorkbench(cache)
	if err := reopened.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	prefix := "/ide/project/session"
	for _, resource := range []string{"/", "/workbench.js", "/static/" + workbenchModule} {
		request := httptest.NewRequest(http.MethodGet, "http://zentrale.example"+prefix+resource, nil)
		response := httptest.NewRecorder()
		if !reopened.Serve(response, request, prefix, "/srv/project \"<script>", "machine-id", "Remote machine") || response.Code != http.StatusOK {
			t.Fatalf("local asset %s: %d %s", resource, response.Code, response.Body.String())
		}
		if resource == "/" && strings.Contains(response.Body.String(), "\"<script>") {
			t.Fatal("workspace path escaped the configuration attribute")
		}
	}
	if reopened.Serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://zentrale.example"+prefix+"/vscode-remote-resource?path=file", nil), prefix, "/srv/project", "machine-id", "remote") {
		t.Fatal("local web handler swallowed workspace protocol traffic")
	}
}

func TestFailedWorkbenchDownloadLeavesNoInstallationAndCanRecover(t *testing.T) {
	archive := webArchive(t, map[string]string{"vscode-web/" + workbenchModule: "module", "vscode-web/out/nls.messages.js": "nls", "vscode-web/LICENSE": "license"})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(archive) }))
	defer server.Close()
	workbench := NewWorkbench(t.TempDir())
	workbench.release.Web = Distribution{URL: server.URL, SHA256: "wrong"}
	if err := workbench.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("checksum: %v", err)
	}
	if workbench.installed() {
		t.Fatal("failed download became visible")
	}
	digest := sha256.Sum256(archive)
	workbench.release.Web.SHA256 = hex.EncodeToString(digest[:])
	if err := workbench.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWebArchiveRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	archive := webArchive(t, map[string]string{"vscode-web/../outside": "must not be written"})
	if err := extractWebArchive(bytes.NewReader(archive), filepath.Join(root, "web")); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "outside")); !os.IsNotExist(err) {
		t.Fatal("archive escaped its directory")
	}
}

func TestPublishedWorkbenchArchive(t *testing.T) {
	name := os.Getenv("ZENTRALE_VSCODE_WEB_ARCHIVE")
	if name == "" {
		t.Skip("set ZENTRALE_VSCODE_WEB_ARCHIVE to test the published archive")
	}
	archive, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, archive); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(digest.Sum(nil)) != CurrentRelease().Web.SHA256 {
		t.Fatal("published archive checksum mismatch")
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err := extractWebArchive(archive, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

type responseTransport func(*http.Request) (*http.Response, error)

func (transport responseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestGermanMessagesRecoverAndStayInCentralCache(t *testing.T) {
	workbench := NewWorkbench(t.TempDir())
	if err := os.MkdirAll(filepath.Join(workbench.directory, "out"), 0700); err != nil {
		t.Fatal(err)
	}
	requests := 0
	workbench.client = &http.Client{Transport: responseTransport(func(request *http.Request) (*http.Response, error) {
		requests++
		if !strings.Contains(request.URL.Path, CurrentRelease().Commit) {
			t.Fatal("unversioned localization request")
		}
		status, content := http.StatusServiceUnavailable, ""
		if requests > 1 {
			status, content = http.StatusOK, "globalThis._VSCODE_NLS_MESSAGES=['Datei'];"
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(content)), Header: make(http.Header)}, nil
	})}
	for attempt := 0; attempt < 3; attempt++ {
		response := httptest.NewRecorder()
		workbench.serveGermanMessages(response, httptest.NewRequest(http.MethodGet, "http://zentrale.example/ide/p/s/nls/de.js", nil))
		if response.Code != http.StatusOK {
			t.Fatal("optional localization blocked startup")
		}
		if attempt == 0 && !strings.Contains(response.Body.String(), "using English") {
			t.Fatal("missing English fallback")
		}
		if attempt > 0 && !strings.Contains(response.Body.String(), "Datei") {
			t.Fatal("German messages missing")
		}
	}
	if requests != 2 {
		t.Fatalf("localization did not use the central cache: %d requests", requests)
	}
}
