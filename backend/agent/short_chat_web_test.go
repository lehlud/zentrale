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
	t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "")
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

func TestPublicWebSearchFallsBackAndReportsUnavailableProviders(t *testing.T) {
	t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "")
	t.Setenv("ZENTRALE_DDGS_PYTHON", "")
	for _, status := range []int{http.StatusOK, http.StatusAccepted} {
		client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Host {
			case "www.bing.com":
				return webFixture(request, 503, "text/plain", "Unavailable")
			case "html.duckduckgo.com":
				return webFixture(request, status, "text/html", `<div class="result"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F">Go docs</a></div>`)
			default:
				return nil, errors.New("unexpected origin")
			}
		})}

		result, err := searchPublicWeb(t.Context(), client, "Go docs")
		if status == http.StatusOK {
			if err != nil || result.Source != "DuckDuckGo" || len(result.Results) != 1 || result.Results[0].URL != "https://go.dev/doc/" {
				t.Fatalf("fallback: %#v, %v", result, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "HTTP 202") {
			t.Fatalf("expected both provider statuses, got %v", err)
		}
	}
}

func TestPublicWebSearchRejectsUnrelatedHitsEvenOnHTTP200(t *testing.T) {
	t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "")
	t.Setenv("ZENTRALE_DDGS_PYTHON", "")
	for _, matching := range []bool{true, false} {
		client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Host {
			case "www.bing.com":
				return webFixture(request, 200, "text/xml", `<rss><channel>
				  <item><title>Public tracking</title><link>https://example.com/tracking</link><description>Track your package</description></item>
				  <item><title>Mira</title><link>https://example.com/names</link><description>A list of first names including Mira</description></item>
				</channel></rss>`)
			case "html.duckduckgo.com":
				if matching {
					return webFixture(request, 200, "text/html", `<div class="result"><a class="result__a" href="https://example.org/speaker">Conference speakers</a><p class="result__snippet">Meet <b>Mira</b> Rosenfeld, one of our speakers.</p></div>`)
				}
				return webFixture(request, 202, "text/html", "Search unavailable")
			default:
				return nil, errors.New("unexpected origin")
			}
		})}

		result, err := searchPublicWeb(t.Context(), client, `"Mira Rosenfeld" wer ist Mira Rosenfeld`)
		if err != nil {
			t.Fatal(err)
		}
		if matching {
			if result.Source != "DuckDuckGo" || len(result.Results) != 1 || result.Results[0].Title != "Conference speakers" {
				t.Fatalf("expected matching fallback result, got %#v", result)
			}
		} else if len(result.Results) != 0 || !strings.Contains(result.Note, "does not mean no public sources exist") || !strings.Contains(strings.Join(result.Unavailable, ";"), "DuckDuckGo: public web page returned HTTP 202") {
			t.Fatalf("unrelated tracking hits must not be used as evidence: %#v", result)
		}
	}
}

func TestPublicWebSearchContinuesWithEncyclopediaAfterMissingWebHits(t *testing.T) {
	t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "")
	t.Setenv("ZENTRALE_DDGS_PYTHON", "")
	client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "www.bing.com":
			return webFixture(request, 200, "text/xml", `<rss><channel><item><title>Parcel tracking</title><link>https://example.com/</link></item></channel></rss>`)
		case "html.duckduckgo.com":
			return webFixture(request, http.StatusAccepted, "text/html", "No search results returned")
		case "de.wikipedia.org":
			if request.URL.Query().Get("q") != `"Mira Rosenfeld"` || request.URL.Path != "/w/rest.php/v1/search/page" {
				t.Fatalf("unexpected encyclopedia request: %s", request.URL)
			}
			return webFixture(request, 200, "application/json", `{"pages":[{"key":"History_of_Optics","title":"Geschichte der Optik","excerpt":"<span class=\"searchmatch\">Mira</span> <span class=\"searchmatch\">Rosenfeld</span> erforschte die Brechung."}]}`)
		default:
			t.Fatalf("unexpected fallback: %s", request.URL.Host)
			return nil, nil
		}
	})}

	result, err := searchPublicWeb(t.Context(), client, `„Mira Rosenfeld“ wer ist Mira Rosenfeld`)
	if err != nil || result.Source != "Wikipedia (de)" || len(result.Results) != 1 {
		t.Fatalf("expected encyclopedia fallback, got %#v, %v", result, err)
	}
	item := result.Results[0]
	if item.URL != "https://de.wikipedia.org/wiki/History_of_Optics" || item.Description != "Mira Rosenfeld erforschte die Brechung." {
		t.Fatalf("unexpected source text: %#v", item)
	}
}

func TestPublicWebSearchUsesConfiguredBraveAndRejectsRedirects(t *testing.T) {
	t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "fixture-only-secret")
	for _, status := range []int{http.StatusOK, http.StatusTemporaryRedirect} {
		client := &http.Client{Transport: webTransportFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host != "api.search.brave.com" || request.Header.Get("X-Subscription-Token") != "fixture-only-secret" {
				t.Fatal("unexpected endpoint or missing provider authentication")
			}
			response, err := webFixture(request, status, "application/json", `{"web":{"results":[{"title":"Go documentation","url":"https://go.dev/doc/"}]}}`)
			if status != http.StatusOK {
				response.Header.Set("Location", "https://example.org/redirected")
			}
			return response, err
		})}

		result, err := searchPublicWeb(t.Context(), client, "Go documentation")
		if status == http.StatusOK {
			if err != nil || result.Source != "Brave" || len(result.Results) != 1 {
				t.Fatalf("Brave search failed: %#v, %v", result, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "HTTP 307") || strings.Contains(err.Error(), "fixture-only-secret") {
			t.Fatalf("expected redirect to be rejected, got %v", err)
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
	t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "")
	t.Setenv("ZENTRALE_DDGS_PYTHON", "")
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

	result, err = searchPublicWeb(ctx, chatWebClient, `"Franz Lehnert" wer ist Franz Lehnert`)
	if err != nil || len(result.Results) == 0 {
		t.Fatalf("no usable sources for the example historical name: %#v, %v", result, err)
	}
	t.Logf("historical-name search: %s, %d results", result.Source, len(result.Results))

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

	t.Setenv("ZENTRALE_BRAVE_SEARCH_API_KEY", "")
	result, err := searchPublicWeb(t.Context(), chatWebClient, query)
	if err != nil || result.Source != "Yahoo (DDGS)" {
		t.Fatalf("expected the configured DDGS source, got %s, %v", result.Source, err)
	}

	for _, item := range result.Results {
		if item.URL == expectedURL {
			return
		}
	}
	t.Fatalf("DDGS did not return the expected URL %s among %d relevant results", expectedURL, len(result.Results))
}
