package fileshare

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDomainShareListsReadsAndSearchesForTheCaller(t *testing.T) {
	const id = "d1234567890123456789012345678901"
	const token = "fixture-agent-token"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		username := query.Get("username")
		if r.Method != http.MethodGet || len(query["username"]) != 1 || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("expected a single username and a bearer header on a GET")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if username != "alice" && username != "bob" {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		switch r.URL.Path {
		case domainShareAPIPrefix + "/list":
			source := query.Get("source")
			if source == "" {
				fmt.Fprintf(w, `{"sources":[{"id":"private:%s","label":"Private","kind":"private"}]}`, username)
			} else if source != "private:"+username {
				w.WriteHeader(http.StatusNotFound)
			} else if query.Get("after") == "" {
				fmt.Fprint(w, `{"items":[{"name":"A","type":"directory","size":null}],"nextAfter":"A"}`)
			} else {
				fmt.Fprint(w, `{"items":[{"name":"notes.txt","type":"file","size":9}],"nextAfter":null}`)
			}
		case domainShareAPIPrefix + "/file":
			if query.Get("source") != "private:"+username || query.Get("path") != "A/notes & more.txt" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprint(w, "notes for "+username)
		case domainShareAPIPrefix + "/search":
			if query.Get("q") != "status & budget" {
				t.Error("search query was not encoded correctly")
			}
			if query.Get("cursor") == "next:page" {
				fmt.Fprint(w, `{"items":[],"nextCursor":null}`)
				return
			}
			fmt.Fprintf(w, `{"items":[{"id":"%s","name":"notes.pdf","source":"Private","sourceId":"private:%s","path":"A/notes.pdf","snippet":"Budget details"}],"nextCursor":"next:page"}`, id, username)
		case domainShareAPIPrefix + "/documents/" + id:
			if username != "alice" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprint(w, `{"text":"indexed notes"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	trustDomainShareFixture(t, server)

	share, err := New(Config{Protocol: ProtocolDomainShare, Endpoint: server.URL + domainShareAPIPrefix, Password: token})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	for _, username := range []string{"alice", "bob"} {
		roots, err := share.List(ctx, username, "")
		if err != nil || len(roots) != 1 || roots[0] != (Entry{Name: "private:" + username, Path: "private:" + username, Label: "Private", IsDir: true}) {
			t.Fatalf("unexpected visible roots for %s: %+v, %v", username, roots, err)
		}
		items, err := share.List(ctx, username, roots[0].Name)
		if err != nil || len(items) != 2 || !items[0].IsDir || items[1].Name != "notes.txt" {
			t.Fatalf("expected both directory pages: %+v, %v", items, err)
		}
		data, err := share.Read(ctx, username, roots[0].Name+"/A/notes & more.txt", 64)
		if err != nil || string(data) != "notes for "+username {
			t.Fatalf("incorrect file read: %q, %v", data, err)
		}
	}
	if _, err := share.List(ctx, "carol", ""); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("account outside DomainShare token scope: %v", err)
	}
	if _, err := share.Read(ctx, "bob", "private:alice/A/notes & more.txt", 64); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account file access: %v", err)
	}
	if _, err := share.Read(ctx, "alice", "private:alice/../notes.txt", 64); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("unsafe path accepted: %v", err)
	}
	if _, err := share.Read(ctx, "alice", "private:alice/A/notes & more.txt", 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("file read exceeded limit: %v", err)
	}

	page, err := share.Search(ctx, "alice", "status & budget", "")
	if err != nil || len(page.Items) != 1 || page.NextCursor != "next:page" {
		t.Fatalf("unexpected search page: %+v, %v", page, err)
	}
	next, err := share.Search(ctx, "alice", "status & budget", page.NextCursor)
	if err != nil || len(next.Items) != 0 || next.NextCursor != "" {
		t.Fatalf("search did not forward cursor: %+v, %v", next, err)
	}
	item := page.Items[0]
	path := item.TextPath
	if item.Path != "private:alice/A/notes.pdf" || path != "@document/"+id || item.Snippet != "Budget details" {
		t.Fatalf("incorrect search result: %+v", item)
	}
	data, err := share.Read(ctx, "alice", path, 64)
	if err != nil || string(data) != "indexed notes" {
		t.Fatalf("incorrect indexed text: %q, %v", data, err)
	}
	if _, err := share.Read(ctx, "bob", path, 64); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account indexed document access: %v", err)
	}

	data, err = share.ReadChunk(ctx, "alice", path, 8, 3)
	if err != nil || string(data) != "note" {
		t.Fatalf("incorrect indexed text chunk including lookahead: %q, %v", data, err)
	}
	if _, err := share.ReadChunk(ctx, "bob", path, 8, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account indexed chunk access: %v", err)
	}
}

func TestDomainShareDistinguishesEmptyRootsFromInvalidResponses(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("username") {
		case "alice":
			fmt.Fprint(w, `{"sources":[]}`)
		case "bob":
			fmt.Fprint(w, `{"healthy":true}`)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	t.Cleanup(server.Close)
	trustDomainShareFixture(t, server)
	share, err := New(Config{Protocol: ProtocolDomainShare, Endpoint: server.URL, Password: "test-token"})
	if err != nil {
		t.Fatal(err)
	}

	if entries, err := share.List(t.Context(), "alice", ""); err != nil || len(entries) != 0 {
		t.Fatalf("empty root was not a success: %+v, %v", entries, err)
	}
	if _, err := share.List(t.Context(), "bob", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unexpected JSON passed the root check: %v", err)
	}
	if _, err := share.List(t.Context(), "carol", ""); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("permission failure passed the root check: %v", err)
	}
}

func TestDomainShareRequiresValidTLSAndDoesNotFollowRedirects(t *testing.T) {
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != domainShareAPIPrefix+"/list" {
			t.Error("DomainShare followed an unexpected redirect")
		}
		http.Redirect(w, r, "/unexpected", http.StatusFound)
	}))
	t.Cleanup(redirect.Close)

	config := Config{Protocol: ProtocolDomainShare, Endpoint: "http://domainshare.example", Password: "agent-token"}
	if _, err := New(config); err == nil {
		t.Fatal("accepted unencrypted DomainShare endpoint")
	}
	config.Endpoint = redirect.URL
	share, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := share.List(context.Background(), "alice", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("accepted an untrusted certificate: %v", err)
	}
	trustDomainShareFixture(t, redirect)
	if _, err := share.List(context.Background(), "alice", ""); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("accepted a redirect: %v", err)
	}
}

func trustDomainShareFixture(t *testing.T, server *httptest.Server) {
	t.Helper()
	original := shareHTTPClient
	transport := original.Transport.(*http.Transport).Clone()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	transport.TLSClientConfig = &tls.Config{RootCAs: pool}

	client := *original
	client.Transport = transport
	shareHTTPClient = &client
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		shareHTTPClient = original
	})
}
