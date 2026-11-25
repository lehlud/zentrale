package webapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPWAEntryFilesAreServedWithoutSPAFallback(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"index.html":           "<title>Zentrale</title>",
		"manifest.webmanifest": `{"name":"Zentrale"}`,
		"pwa-sw.js":            "self.addEventListener('fetch', () => {})",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	handler, err := (&Server{Frontend: root}).Handler()
	if err != nil {
		t.Fatal(err)
	}

	get := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response
	}

	for path, mimeType := range map[string]string{
		"/manifest.webmanifest": "application/manifest+json",
		"/pwa-sw.js":            "text/javascript",
	} {
		response := get(path)
		if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), mimeType) ||
			response.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: HTTP %d, headers %v", path, response.Code, response.Header())
		}

		if err := os.Remove(filepath.Join(root, path)); err != nil {
			t.Fatal(err)
		}
		if missing := get(path); missing.Code != http.StatusNotFound {
			t.Fatalf("missing %s returned status %d", path, missing.Code)
		}
	}

	if missing := get("/icons/missing.png"); missing.Code != http.StatusNotFound {
		t.Fatalf("missing icon returned status %d", missing.Code)
	}
	if index := get("/"); index.Code != http.StatusOK || index.Body.String() != "<title>Zentrale</title>" {
		t.Fatalf("app shell not served: HTTP %d, %q", index.Code, index.Body.String())
	}
}
