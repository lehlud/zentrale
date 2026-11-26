package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type webTransportFunc func(*http.Request) (*http.Response, error)

func (transport webTransportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func webFixture(request *http.Request, status int, contentType, body string) (*http.Response, error) {
	return &http.Response{
		Request: request, StatusCode: status,
		Header: http.Header{"Content-Type": {contentType}},
		Body:   io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestPublicWebSearchReturnsStructuredResults(t *testing.T) {
	client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "www.bing.com" || request.URL.Query().Get("q") != "Go docs & examples" || request.URL.Query().Get("format") != "rss" {
			t.Fatalf("unexpected search request: %s", request.URL)
		}
		return webFixture(request, 200, "text/xml", `<rss version="2.0"><channel>
		  <item><title>Go docs</title><link>https://go.dev/doc/</link><description>Language documentation</description></item>
		  <item><title>Invalid</title><link>file:///etc/hosts</link></item>
		</channel></rss>`)
	})}

	result, err := searchPublicWeb(t.Context(), client, " Go docs & examples ")
	if err != nil {
		t.Fatal(err)
	}
	response := result.(map[string]any)
	results := response["results"].([]webSearchResult)
	if response["source"] != "Bing" || len(results) != 1 || results[0].URL != "https://go.dev/doc/" || results[0].Description != "Language documentation" {
		t.Fatalf("unexpected search result: %#v", result)
	}
}

func TestPublicWebSearchFallsBackAndReportsUnavailableProviders(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusAccepted} {
		client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Host {
			case "www.bing.com":
				return webFixture(request, 503, "text/plain", "Unavailable")
			case "html.duckduckgo.com":
				return webFixture(request, status, "text/html", `<p><a href="https://go.dev/doc/">Go documentation</a></p>`)
			default:
				return nil, errors.New("unexpected origin")
			}
		})}

		result, err := searchPublicWeb(t.Context(), client, "Go docs")
		if status == http.StatusOK {
			if err != nil || !strings.Contains(result.(map[string]string)["text"], "https://go.dev/doc/") {
				t.Fatalf("fallback: %#v, %v", result, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "HTTP 202") {
			t.Fatalf("expected both provider statuses, got %v", err)
		}
	}
}

func TestPublicWebReadRejectsEmptyPages(t *testing.T) {
	client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
		return webFixture(request, 200, "application/xhtml+xml", `<html><script>content unavailable</script></html>`)
	})}

	if _, err := readPublicWeb(t.Context(), client, "https://example.com"); err == nil || !strings.Contains(err.Error(), "no readable text") {
		t.Fatalf("expected actionable empty page error, got %v", err)
	}
}

func TestPublicWebLiveSmoke(t *testing.T) {
	if os.Getenv("ZENTRALE_TEST_PUBLIC_WEB") != "1" {
		t.Skip("set ZENTRALE_TEST_PUBLIC_WEB=1 for an opt-in public search and page read")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()

	result, err := searchPublicWeb(ctx, chatWebClient, "Go programming language documentation")
	if err != nil {
		t.Fatal(err)
	}
	if response, ok := result.(map[string]any); !ok || len(response["results"].([]webSearchResult)) == 0 {
		t.Fatal("the primary public search did not return ranked results")
	}
	result, err = fetchChatWeb(ctx, "https://go.dev/doc/")
	if err != nil || !strings.Contains(result.(map[string]string)["text"], "Documentation") {
		t.Fatalf("documentation page read failed: %v", err)
	}
}
