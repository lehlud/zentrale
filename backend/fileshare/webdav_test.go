package fileshare

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	davRoot = "/dav/users/alice/"
	davFile = davRoot + "a note.txt"
)

func TestWebDAVListsAndReadsOnlyScopedResources(t *testing.T) {
	var gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "service" || pass != "backend-only" || r.Header.Get("Apply-To-Redirect-Ref") != "T" {
			t.Error("expected configured credentials and redirect-reference header")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		switch {
		case r.Method == "PROPFIND" && r.URL.Path == "/dav/users/" && r.Header.Get("Depth") == "0":
			writeDAVStatus(w, davItem(r.URL.EscapedPath(), "<d:collection/>"))
		case r.Method == "PROPFIND" && r.URL.Path == davRoot:
			items := []string{davItem(davRoot, "<d:collection/>")}
			if r.Header.Get("Depth") == "1" {
				items = append(items,
					davItem(davRoot+"a%20note.txt", ""),
					davItem("http://"+r.Host+davRoot+"folder/", "<d:collection/>"),
					davItem(davRoot+"link", "<d:redirectref/>"),
					davItem(davRoot+"duplicate.txt", ""),
					davItem(davRoot+"duplicate.txt", "<d:redirectref/>"),
					davItem(davRoot+"sub%2fescape", ""),
					davItem(davRoot+"sub%5cescape", ""),
					davItem(davRoot+"%2e%2e", ""),
					davItem(davRoot+"%252e%252e", ""),
					davItem("/dav/users/bob/outside", ""),
					davItem("/dav/users/alice-other/outside", ""),
					davItem("http://foreign.example"+davRoot+"foreign.txt", ""),
					davItem("//"+r.Host+davRoot+"foreign-form.txt", ""),
					davItem(davRoot+"grand/child.txt", ""),
				)
			}
			writeDAVStatus(w, items...)
		case r.Method == "PROPFIND" && r.URL.Path == davFile:
			writeDAVStatus(w, davItem(davRoot+"a%20note.txt", ""))
		case r.Method == http.MethodGet && r.URL.Path == davFile:
			gets.Add(1)
			w.Header().Set("Content-Type", "text/plain")
			w.(http.Flusher).Flush() // Exercise bounds without a known Content-Length.
			fmt.Fprint(w, "abc")
		default:
			t.Errorf("unexpected request: %s %q", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	share, err := New(Config{Protocol: ProtocolWebDAV, Endpoint: server.URL + "/dav", Root: "users/{username}", Username: "service", Password: "backend-only"})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := share.List(context.Background(), "alice", "")
	if err != nil || len(entries) != 2 || entries[0] != (Entry{Name: "a note.txt", Size: 3}) ||
		entries[1].Name != "folder" || !entries[1].IsDir || gets.Load() != 0 {
		t.Fatalf("entries=%+v GETs=%d err=%v", entries, gets.Load(), err)
	}

	data, err := share.Read(context.Background(), "alice", "a note.txt", 3)
	if err != nil || string(data) != "abc" {
		t.Fatalf("data=%q err=%v", data, err)
	}

	data, err = share.Read(context.Background(), "alice", "a note.txt", 2)
	if !errors.Is(err, ErrTooLarge) || data != nil {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestWebDAVNeverFollowsRedirectsOrReferences(t *testing.T) {
	var outsideHits atomic.Int32
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outsideHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer outside.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" && r.URL.Path == "/root/" {
			writeDAVStatus(w, davItem(r.URL.EscapedPath(), "<d:collection/>"))
			return
		}
		switch r.URL.Path {
		case "/root/foreign/":
			http.Redirect(w, r, outside.URL+"/foreign/", http.StatusTemporaryRedirect)
		case "/root/linked/":
			writeDAVStatus(w, davItem(r.URL.EscapedPath(), "<d:redirectref/>"))
		case "/root/internal.txt":
			if r.Method == "PROPFIND" {
				writeDAVStatus(w, davItem(r.URL.EscapedPath(), ""))
			} else {
				http.Redirect(w, r, "/outside.txt", http.StatusFound)
			}
		default:
			outsideHits.Add(1)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	share, err := New(Config{Protocol: ProtocolWebDAV, Endpoint: server.URL, Root: "root"})
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"foreign/x.txt", "linked/x.txt", "internal.txt"} {
		data, err := share.Read(context.Background(), "alice", path, 8)
		if !errors.Is(err, ErrUnsafePath) || data != nil {
			t.Errorf("%q: data=%q err=%v", path, data, err)
		}
	}
	if hits := outsideHits.Load(); hits != 0 {
		t.Errorf("out-of-scope requests: %d", hits)
	}
}

func TestWebDAVCancelsStalledRead(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	share, err := New(Config{Protocol: ProtocolWebDAV, Endpoint: server.URL, Root: "/"})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, err := share.List(ctx, "alice", ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected error %v", err)
	}
}

func davItem(href, resourceType string) string {
	return `<d:response><d:href>` + html.EscapeString(href) + `</d:href><d:propstat><d:prop>` +
		`<d:resourcetype>` + resourceType + `</d:resourcetype><d:getcontentlength>3</d:getcontentlength>` +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func writeDAVStatus(w http.ResponseWriter, items ...string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusMultiStatus)
	fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:">`, strings.Join(items, ""), `</d:multistatus>`)
}
