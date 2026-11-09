package vscode

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxArchiveBytes = 128 << 20

const maxExtractedBytes = 512 << 20

const workbenchModule = "out/vs/workbench/workbench.web.main.internal.js"

type assetDownload struct {
	done chan struct{}
	err  error
}

type Workbench struct {
	directory     string
	release       Release
	client        *http.Client
	mutex         sync.Mutex
	languageMutex sync.Mutex
	download      *assetDownload
}

func NewWorkbench(cacheDirectory string) *Workbench {
	if cacheDirectory == "" {
		cacheDirectory = os.Getenv("ZENTRALE_VSCODE_CACHE")
	}

	if cacheDirectory == "" {
		cacheDirectory, _ = os.UserCacheDir()
		if cacheDirectory == "" {
			cacheDirectory = os.TempDir()
		}

		cacheDirectory = filepath.Join(cacheDirectory, "zentrale", "vscode-web")
	}

	release := CurrentRelease()

	return &Workbench{
		directory: filepath.Join(cacheDirectory, release.Commit),
		release:   release,
		client:    &http.Client{Timeout: 4 * time.Minute},
	}
}

// Ensure installs browser assets only when an editor is explicitly opened.
// Concurrent callers share one download; waiting callers can still cancel.
func (workbench *Workbench) Ensure(ctx context.Context) error {
	workbench.mutex.Lock()

	if workbench.installed() {
		workbench.mutex.Unlock()
		return nil
	}

	if pending := workbench.download; pending != nil {
		workbench.mutex.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-pending.done:
			return pending.err
		}
	}

	pending := &assetDownload{done: make(chan struct{})}
	workbench.download = pending
	workbench.mutex.Unlock()
	err := workbench.install(ctx)
	workbench.mutex.Lock()
	pending.err = err
	workbench.download = nil
	close(pending.done)
	workbench.mutex.Unlock()

	return err
}

func (workbench *Workbench) installed() bool {
	for _, name := range []string{workbenchModule, "out/nls.messages.js", "LICENSE"} {
		info, err := os.Stat(filepath.Join(workbench.directory, name))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}

	return true
}

func (workbench *Workbench) install(ctx context.Context) error {
	parent := filepath.Dir(workbench.directory)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}

	temporary, err := os.MkdirTemp(parent, ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, workbench.release.Web.URL, nil)
	if err != nil {
		return err
	}

	response, err := workbench.client.Do(request)
	if err != nil {
		return fmt.Errorf("download VS Code web workbench: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download VS Code web workbench: HTTP %d", response.StatusCode)
	}

	archivePath := filepath.Join(temporary, "web.tar.gz")
	archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer archive.Close()

	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(archive, digest), io.LimitReader(response.Body, maxArchiveBytes+1))
	if err != nil {
		return err
	}

	if size > maxArchiveBytes {
		return errors.New("VS Code web archive exceeds the size limit")
	}

	if hex.EncodeToString(digest.Sum(nil)) != workbench.release.Web.SHA256 {
		return errors.New("VS Code web archive checksum mismatch")
	}

	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return err
	}

	destination := filepath.Join(temporary, "workbench")
	if err := extractWebArchive(archive, destination); err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(destination, workbenchModule)); err != nil {
		return errors.New("VS Code web archive has no workbench module")
	}

	if err := os.Rename(destination, workbench.directory); err != nil {
		// Another Zentrale process may have installed the same pinned release.
		if !workbench.installed() {
			return err
		}
	}

	return nil
}

func extractWebArchive(archive io.Reader, directory string) error {
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}
	defer compressed.Close()

	reader := tar.NewReader(compressed)
	var total int64

	for {
		entry, err := reader.Next()
		if err == io.EOF {
			return nil
		}

		if err != nil {
			return err
		}

		name := strings.TrimPrefix(entry.Name, "vscode-web/")

		if entry.Name == "vscode-web" || entry.Name == "vscode-web/" {
			continue
		}

		if name == entry.Name || !filepath.IsLocal(name) || strings.Contains(name, "\\") {
			return errors.New("invalid path in VS Code web archive")
		}

		target := filepath.Join(directory, name)

		switch entry.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			total += entry.Size
			if entry.Size < 0 || total > maxExtractedBytes {
				return errors.New("VS Code web archive expands beyond the size limit")
			}

			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}

			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}

			_, copyError := io.Copy(file, reader)
			closeError := file.Close()

			if copyError != nil {
				return copyError
			}

			if closeError != nil {
				return closeError
			}
		default:
			return errors.New("unsupported link or special file in VS Code web archive")
		}
	}
}
