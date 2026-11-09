package vscode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func (workbench *Workbench) serveGermanMessages(writer http.ResponseWriter, request *http.Request) {
	workbench.languageMutex.Lock()
	defer workbench.languageMutex.Unlock()
	writer.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	name := filepath.Join(workbench.directory, "out", "nls.messages.de.js")
	if data, err := os.ReadFile(name); err == nil {
		writer.Header().Set("Cache-Control", "private, max-age=86400")
		_, _ = writer.Write(data)
		return
	}
	data, err := workbench.downloadGermanMessages(request.Context())
	if err != nil {
		// English was loaded first. Optional localization must not block startup.
		writer.Header().Set("Cache-Control", "no-store")
		_, _ = writer.Write([]byte("/* German messages unavailable; using English. */"))
		return
	}
	// A cache error must not prevent this editor from using the downloaded text.
	_ = cacheTranslation(name, data)
	writer.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = writer.Write(data)
}

func (workbench *Workbench) downloadGermanMessages(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	url := fmt.Sprintf("https://www.vscode-unpkg.net/nls/%s/%s/de/nls.messages.js", workbench.release.Commit, workbench.release.Version)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := workbench.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download German messages: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) >= 8<<20 {
		return nil, fmt.Errorf("invalid German message bundle size: %d", len(data))
	}
	return append([]byte("globalThis._VSCODE_NLS_LANGUAGE='de';\n"), data...), nil
}

func cacheTranslation(name string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(name), ".nls-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, writeError := temporary.Write(data)
	closeError := temporary.Close()
	if writeError != nil {
		return writeError
	}
	if closeError != nil {
		return closeError
	}
	return os.Rename(temporary.Name(), name)
}
