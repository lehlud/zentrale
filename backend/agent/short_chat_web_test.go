package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
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
	t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "fixture-key")
	client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.search.brave.com" || request.URL.Query().Get("q") != "Go docs & examples" || request.Header.Get("X-Subscription-Token") != "fixture-key" {
			t.Fatalf("unexpected search request: %s", request.URL)
		}
		return webFixture(request, 200, "application/json", `{"web":{"results":[
		  {"title":"Go docs and examples","url":"https://go.dev/doc/","description":"Language documentation"},
		  {"title":"Go docs and examples","url":"file:///etc/hosts"}
		]}}`)
	})}

	result, err := searchPublicWeb(t.Context(), client, " Go docs & examples ")
	if err != nil {
		t.Fatal(err)
	}
	results := result.Results
	if result.Source != "Brave Search API" || len(results) != 1 || results[0].URL != "https://go.dev/doc/" || results[0].Description != "Language documentation" {
		t.Fatalf("unexpected search result: %#v", result)
	}
}

func TestPublicWebSearchQueriesAtMostOneFallback(t *testing.T) {
	const query = `site:example.org "Mira Rosenfeld"`
	const matching = `[{"title":"Mira Rosenfeld","url":"https://example.org/mira"}]`
	const namesake = `[{"title":"Mira Rosenfeld","url":"https://other.example/mira"}]`
	const braveHit = `{"web":{"results":[{"title":"Mira Rosenfeld","url":"https://other.example/mira"},{"title":"Mira Rosenfeld","url":"https://example.org/speaker"}]}}`
	const braveMiss = `{"web":{"results":[{"title":"Parcel tracking","url":"https://example.org/tracking"}]}}`

	for _, tc := range []struct {
		name, braveBody, yahoo, wantSource, wantURL string
		braveStatus, wantFallbackCalls              int
		wantError, wantUnavailable                  bool
	}{
		{name: "Yahoo hit", yahoo: matching, wantSource: "Yahoo (DDGS)", wantURL: "https://example.org/mira"},
		{name: "Yahoo failure", braveStatus: 200, braveBody: braveHit, wantFallbackCalls: 1, wantUnavailable: true, wantSource: "Brave Search API", wantURL: "https://example.org/speaker"},
		{name: "qualified name rejects a namesake outside the source", yahoo: namesake, braveStatus: 200, braveBody: braveHit, wantFallbackCalls: 1, wantSource: "Brave Search API", wantURL: "https://example.org/speaker"},
		{name: "clean empty responses", yahoo: `[]`, braveStatus: 200, braveBody: braveMiss, wantFallbackCalls: 1, wantSource: "Yahoo (DDGS), Brave Search API"},
		{name: "both unavailable", braveStatus: 503, wantFallbackCalls: 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			callsFile := filepath.Join(dir, "calls")
			fixture := filepath.Join(dir, "python")
			script := "#!/bin/sh\nprintf x >> " + strconv.Quote(callsFile) + "\n"
			script += "IFS= read -r query || :\n[ \"$query\" = '" + query + "' ] || exit 2\n"
			if tc.yahoo == "" {
				script += "exit 1\n"
			} else {
				script += "printf '%s' '" + tc.yahoo + "'\n"
			}
			if err := os.WriteFile(fixture, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("ZENTRALE_DDGS_PYTHON", fixture)
			t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "fixture-key")

			braveRequests := 0
			client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host != "api.search.brave.com" || request.URL.Query().Get("q") != query {
					t.Fatalf("unexpected HTTP request: %s", request.URL)
				}
				if calls, err := os.ReadFile(callsFile); err != nil || string(calls) != "x" {
					t.Fatal("the API fallback must follow exactly one Yahoo attempt")
				}
				braveRequests++
				return webFixture(request, tc.braveStatus, "application/json", tc.braveBody)
			})}

			result, err := searchPublicWeb(t.Context(), client, query)
			calls, readErr := os.ReadFile(callsFile)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(calls) != 1 || braveRequests != tc.wantFallbackCalls {
				t.Fatalf("provider call counts: DDGS=%d, Brave=%d", len(calls), braveRequests)
			}
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "Brave Search API: public web page returned HTTP 503") || !strings.Contains(err.Error(), "Yahoo (DDGS)") {
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

func TestPublicWebQuoteRecoveryPreservesExactPhraseAndDomain(t *testing.T) {
	const quoted = `site:example.org "Mira Rosenfeld"`
	const plain = `site:example.org Mira Rosenfeld`
	const expectedURL = "https://example.org/mira"
	var queries []string
	results, err := searchWithQuoteRecovery(quoted, func(query string) ([]webSearchResult, error) {
		queries = append(queries, query)
		if query == quoted {
			return nil, nil
		}

		return []webSearchResult{
			{Title: "Mira meets Rosenfeld", URL: "https://example.org/unrelated"},
			{Title: "Mira Rosenfeld", URL: "https://other.example/mira"},
			{Title: "Mira Rosenfeld", URL: expectedURL},
		}, nil
	})
	if err != nil || strings.Join(queries, "\n") != quoted+"\n"+plain {
		t.Fatalf("expected one rephrased search after the miss: %v, %v", queries, err)
	}
	if kept := relevantResults(results, searchMatcher(quoted)); len(kept) != 1 || kept[0].URL != expectedURL {
		t.Fatalf("rephrasing must preserve the original relevance filter: %+v", kept)
	}
}

func TestPublicWebQuoteRecoveryDoesNotRetryHitsOrFailures(t *testing.T) {
	problem := errors.New("provider unavailable")
	for _, tc := range []struct {
		name, query string
		results     []webSearchResult
		err         error
	}{
		{name: "hit", query: `"Mira Rosenfeld"`, results: []webSearchResult{{Title: "Mira Rosenfeld", URL: "https://example.org/mira"}}},
		{name: "unquoted", query: "Mira Rosenfeld"},
		{name: "provider error", query: `"Mira Rosenfeld"`, err: problem},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			_, err := searchWithQuoteRecovery(tc.query, func(string) ([]webSearchResult, error) {
				calls++
				return tc.results, tc.err
			})
			if calls != 1 || err != tc.err {
				t.Fatalf("unexpected calls or error: %d, %v", calls, err)
			}
		})
	}
}

func TestPublicWebSearchAPIKeyIsNotForwardedThroughRedirect(t *testing.T) {
	t.Setenv("ZENTRALE_DDGS_PYTHON", "")
	t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "fixture-key")
	var hosts []string
	client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
		hosts = append(hosts, request.URL.Host)
		if request.URL.Host != "api.search.brave.com" {
			return webFixture(request, 200, "application/json", `{}`)
		}

		response, err := webFixture(request, http.StatusFound, "text/plain", "redirect")
		response.Header.Set("Location", "https://other.example/search")
		return response, err
	})}

	_, err := searchPublicWeb(t.Context(), client, "Go documentation")
	if strings.Join(hosts, ",") != "api.search.brave.com" || err == nil || strings.Contains(err.Error(), "fixture-key") {
		t.Fatalf("the redirect must not be followed: %v, %v", hosts, err)
	}
}

func TestPublicWebSearchFallsBackToTheRequestedSitesLinks(t *testing.T) {
	const query = "Go documentation site:example.org"
	const homepage = `<a href="https://other.example/doc">Go documentation</a>
<a href="/blog">Go news</a><a href="/doc">Go <span>User Manual</span></a>`
	t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "")

	dir := t.TempDir()
	callsPath := filepath.Join(dir, "calls")
	py := filepath.Join(dir, "python")
	fixture := "#!/bin/sh\nIFS= read -r query || :\nprintf '%s' \"$query\" > " + strconv.Quote(callsPath) + "\nprintf '[]'\n"
	if err := os.WriteFile(py, []byte(fixture), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZENTRALE_DDGS_PYTHON", py)

	calls := 0
	client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.String() != "https://example.org/" {
			t.Errorf("must read only the requested homepage, got %s", request.URL)
		}
		if previous, err := os.ReadFile(callsPath); err != nil || string(previous) != query {
			t.Errorf("the site must be read after Yahoo's empty result: %q, %v", previous, err)
		}
		return webFixture(request, http.StatusOK, "text/html", homepage)
	})}

	result, err := searchPublicWeb(t.Context(), client, query)
	if calls != 1 || err != nil || result.Source != "example.org (site links)" || len(result.Results) != 1 || result.Results[0].URL != "https://example.org/doc" {
		t.Fatalf("expected one relevant same-site link, got %v with %d requests (%v)", result, calls, err)
	}
}

func TestPublicWebSiteFallbackRejectsCrossSiteRedirects(t *testing.T) {
	t.Setenv("ZENTRALE_DDGS_PYTHON", "")
	t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "")

	var hosts []string
	client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
		hosts = append(hosts, request.URL.Host)
		response, err := webFixture(request, http.StatusFound, "text/plain", "redirect")
		response.Header.Set("Location", "https://elsewhere.example/doc")
		return response, err
	})}
	_, err := searchPublicWeb(t.Context(), client, "docs site:example.org")
	if strings.Join(hosts, ",") != "example.org" || err == nil {
		t.Fatalf("the fallback must not follow cross-site redirects: %v, %v", hosts, err)
	}
	for _, invalid := range []string{"site:127.0.0.1", "site:localhost", "site:example.org:443", "site:example.org@other.example", "site:first.example site:second.example"} {
		if domain := siteNavigationDomain(invalid); domain != "" {
			t.Errorf("accepted invalid site in %q: %q", invalid, domain)
		}
	}
}

func TestPublicWebDDGSHelperDistinguishesEmptyResultsFromFailures(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python is required to test the embedded DDGS helper")
	}

	// Stub only DDGS, then run the same isolated program used by Zentrale.
	const fixture = `
import sys
from types import ModuleType

class DDGSException(Exception):
    pass

class TimeoutException(DDGSException):
    pass

class DDGS:
    def __init__(self, timeout):
        pass

    def text(self, query, *, backend, **kwargs):
        assert backend == "yahoo"
        if query == "empty":
            raise DDGSException("No results found.")
        raise TimeoutException("No results found.")

sys.modules["ddgs"] = ModuleType("ddgs")
sys.modules["ddgs.exceptions"] = ModuleType("ddgs.exceptions")
sys.modules["ddgs"].DDGS = DDGS
sys.modules["ddgs.exceptions"].DDGSException = DDGSException
`

	for _, query := range []string{"empty", "timeout"} {
		t.Run(query, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), python, "-I", "-c", fixture+ddgsSearchScript)
			command.Stdin = strings.NewReader(query)
			output, err := command.Output()
			if query == "empty" && (err != nil || string(output) != "[]\n") {
				t.Fatalf("expected an empty JSON result, got %q (%v)", output, err)
			}
			if query == "timeout" && err == nil {
				t.Fatal("a provider failure must not become an empty result")
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

func TestPublicWebSiteLiveSmoke(t *testing.T) {
	if os.Getenv("ZENTRALE_TEST_PUBLIC_WEB") != "1" {
		t.Skip("set ZENTRALE_TEST_PUBLIC_WEB=1 to test a real site without a search key")
	}
	t.Setenv("ZENTRALE_DDGS_PYTHON", "")
	t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "")

	result, err := searchPublicWeb(t.Context(), chatWebClient, "Go documentation site:go.dev")
	if err != nil || result.Source != "go.dev (site links)" {
		t.Fatalf("site fallback failed: %+v, %v", result, err)
	}
	for _, item := range result.Results {
		if strings.TrimSuffix(item.URL, "/") == "https://go.dev/doc" {
			t.Logf("found the actual %s link without a search provider", item.URL)
			return
		}
	}
	t.Fatalf("the site fallback did not find the Go documentation index: %+v", result)
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
