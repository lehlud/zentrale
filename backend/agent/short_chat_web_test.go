package agent

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	t.Setenv("ZENTRALE_DDGS_PYTHON", "")
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
	results := result.Results
	if result.Source != "Bing" || len(results) != 1 || results[0].URL != "https://go.dev/doc/" || results[0].Description != "Language documentation" {
		t.Fatalf("unexpected search result: %#v", result)
	}
}

func TestPublicWebSearchQueriesAtMostOneFallback(t *testing.T) {
	const query = "Mira Rosenfeld"
	const matching = `<rss><channel><item><title>Mira Rosenfeld</title><link>https://example.org/mira</link></item></channel></rss>`
	const irrelevant = `<rss><channel><item><title>Mira</title><link>https://example.org/names</link></item></channel></rss>`
	const yahooHit = `[{"title":"Mira Rosenfeld","url":"https://example.org/speaker"}]`
	const yahooMiss = `[{"title":"Parcel tracking","url":"https://example.org/tracking"}]`

	for _, tc := range []struct {
		name, bingBody, yahoo, wantSource, wantURL string
		bingStatus, wantFallbackCalls              int
		wantError, wantUnavailable                 bool
	}{
		{name: "Bing hit", bingBody: matching, yahoo: yahooHit, bingStatus: 200, wantSource: "Bing", wantURL: "https://example.org/mira"},
		{name: "Bing failure", bingStatus: 503, yahoo: yahooHit, wantFallbackCalls: 1, wantUnavailable: true, wantSource: "Yahoo (DDGS)", wantURL: "https://example.org/speaker"},
		{name: "Bing irrelevant", bingStatus: 200, bingBody: irrelevant, yahoo: yahooHit, wantFallbackCalls: 1, wantSource: "Yahoo (DDGS)", wantURL: "https://example.org/speaker"},
		{name: "no relevant hits", bingStatus: 200, bingBody: irrelevant, yahoo: yahooMiss, wantFallbackCalls: 1, wantSource: "Bing, Yahoo (DDGS)"},
		{name: "both unavailable", bingStatus: 503, wantFallbackCalls: 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			callsFile := filepath.Join(dir, "calls")
			fixture := filepath.Join(dir, "python")
			script := "#!/bin/sh\nprintf x >> " + strconv.Quote(callsFile) + "\n"
			script += "IFS= read -r query || :\n[ \"$query\" = 'Mira Rosenfeld' ] || exit 2\n"
			if tc.yahoo == "" {
				script += "exit 1\n"
			} else {
				script += "printf '%s' '" + tc.yahoo + "'\n"
			}
			if err := os.WriteFile(fixture, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("ZENTRALE_DDGS_PYTHON", fixture)
			t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "legacy-config-not-used")

			bingRequests := 0
			client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host != "www.bing.com" || request.URL.Query().Get("q") != query {
					t.Fatalf("unexpected HTTP request: %s", request.URL)
				}
				if _, err := os.Stat(callsFile); !os.IsNotExist(err) {
					t.Fatal("DDGS ran before Bing")
				}
				bingRequests++
				return webFixture(request, tc.bingStatus, "text/xml", tc.bingBody)
			})}

			result, err := searchPublicWeb(t.Context(), client, query)
			calls, readErr := os.ReadFile(callsFile)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if bingRequests != 1 || len(calls) != tc.wantFallbackCalls {
				t.Fatalf("provider call counts: Bing=%d, DDGS=%d", bingRequests, len(calls))
			}
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "Bing: public web page returned HTTP 503") || !strings.Contains(err.Error(), "Yahoo (DDGS)") {
					t.Fatalf("expected both failures: %v", err)
				}
				return
			}
			if err != nil || result.Source != tc.wantSource || (len(result.Unavailable) != 0) != tc.wantUnavailable {
				t.Fatalf("unexpected source or provider status: %+v, %v", result, err)
			}
			if tc.wantURL == "" {
				if len(result.Results) != 0 || !strings.Contains(result.Note, "does not mean no public sources exist") {
					t.Fatalf("unrelated results must not be treated as evidence: %+v", result)
				}
			} else if len(result.Results) != 1 || result.Results[0].URL != tc.wantURL {
				t.Fatalf("unexpected results: %+v", result.Results)
			}
		})
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
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	result, err := searchPublicWeb(ctx, chatWebClient, "Go documentation site:go.dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) == 0 || !strings.HasPrefix(result.Results[0].URL, "https://go.dev/") {
		t.Fatalf("the public search did not find relevant Go documentation: %#v", result)
	}

	result, err = searchPublicWeb(ctx, chatWebClient, `"Nonexistent Marzipan Observatory QVX7049"`)
	if err != nil || len(result.Results) != 0 || result.Note == "" {
		t.Fatalf("expected no exact hits: %#v, %v", result, err)
	}

	page, readErr := fetchChatWeb(ctx, "https://go.dev/doc/")
	if readErr != nil || !strings.Contains(page.(map[string]string)["text"], "Documentation") {
		t.Fatalf("documentation page read failed: %v", readErr)
	}
}

func TestPublicWebDDGSLiveSmoke(t *testing.T) {
	query := os.Getenv("ZENTRALE_TEST_DDGS_QUERY")
	expectedURL := os.Getenv("ZENTRALE_TEST_DDGS_URL")
	if query == "" || expectedURL == "" || os.Getenv("ZENTRALE_DDGS_PYTHON") == "" {
		t.Skip("set ZENTRALE_TEST_DDGS_QUERY, ZENTRALE_TEST_DDGS_URL, and ZENTRALE_DDGS_PYTHON to test a real DDGS result")
	}

	result, err := searchPublicWeb(t.Context(), chatWebClient, query)
	if err != nil {
		t.Fatal(err)
	}

	for _, item := range result.Results {
		if item.URL == expectedURL {
			t.Logf("expected public result found via %s", result.Source)
			return
		}
	}
	t.Fatalf("%s did not return %s among %d relevant results (unavailable: %v)", result.Source, expectedURL, len(result.Results), result.Unavailable)
}
