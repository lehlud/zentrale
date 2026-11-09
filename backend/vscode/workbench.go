package vscode

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed workbench.html
var workbenchHTML string

//go:embed workbench.js
var workbenchBootstrap string

// Serve handles only Zentrale-owned web resources. Unknown paths never fall
// back to a remote web server; the caller separately routes agent traffic.
func (workbench *Workbench) Serve(writer http.ResponseWriter, request *http.Request, prefix, directory, machineID, machineName string) bool {
	path := strings.TrimPrefix(request.URL.Path, prefix)
	if path == "/nls/de.js" {
		workbench.serveGermanMessages(writer, request)
		return true
	}
	if path == "/workbench.js" {
		writer.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-cache")
		_, _ = writer.Write([]byte(workbenchBootstrap))
		return true
	}
	if path == "/callback" {
		writer.Header().Set("Cache-Control", "no-store")
		http.ServeFile(writer, request, filepath.Join(workbench.directory, "out/vs/code/browser/workbench/callback.html"))
		return true
	}
	if strings.HasPrefix(path, "/static/") {
		name := strings.TrimPrefix(path, "/static/")
		if !filepath.IsLocal(name) || strings.ContainsAny(name, "\\\x00") {
			http.Error(writer, "invalid workbench resource", http.StatusBadRequest)
			return true
		}
		file := filepath.Join(workbench.directory, name)
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(writer, request)
			return true
		}
		writer.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(writer, request, file)
		return true
	}
	if path != "/" && path != "" {
		return false
	}
	if request.Header.Get("Upgrade") != "" {
		return false
	}
	if err := workbench.Ensure(request.Context()); err != nil {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(writer).Encode(map[string]string{"message": fmt.Sprintf("VS Code web workbench unavailable: %v", err)})
		return true
	}
	identity := sha256.Sum256([]byte(machineID))
	configuration, _ := json.Marshal(map[string]string{
		"prefix": prefix, "assets": prefix + "/static", "directory": directory,
		"machineName": machineName, "theme": request.URL.Query().Get("theme"),
		"remoteAuthority": "ssh-remote+" + hex.EncodeToString(identity[:16]),
		"commit":          workbench.release.Commit,
	})
	language, loading := "en", "Loading VS Code…"
	locale := request.URL.Query().Get("locale")
	if cookie, err := request.Cookie("vscode.nls.locale"); err == nil {
		locale = cookie.Value
	}
	nlsOverride := ""
	if locale == "de" {
		language, loading = "de", "VS Code wird geladen …"
		nlsOverride = "<script src=\"" + html.EscapeString(prefix+"/nls/de.js") + "\"></script>"
	}
	nonce := rand.Text()
	page := strings.NewReplacer(
		"{{NONCE}}", nonce,
		"{{PREFIX}}", html.EscapeString(prefix),
		"{{ASSETS}}", html.EscapeString(prefix+"/static"),
		"{{CONFIGURATION}}", html.EscapeString(string(configuration)),
		"{{LANGUAGE}}", language, "{{LOADING}}", loading,
		"{{NLS_OVERRIDE}}", nlsOverride,
	).Replace(workbenchHTML)
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-eval' blob: 'nonce-"+nonce+"'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data: blob:; font-src 'self' blob:; worker-src 'self' blob:; frame-src 'self' https://*.vscode-cdn.net data:; connect-src 'self' https: ws: wss:;")
	_, _ = writer.Write([]byte(page))
	return true
}
