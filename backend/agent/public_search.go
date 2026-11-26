package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"
)

const publicSearchQueryHelp = "Use short public search terms, never secrets or private file contents. " +
	"Quote full names. If several people match, narrow the query with a known detail or a known source (site:example.org), or ask for context. " +
	"Do not guess which person is meant."

type webSearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"snippet,omitempty"`
}

type webSearchResponse struct {
	Source      string            `json:"source"`
	Results     []webSearchResult `json:"results"`
	Unavailable []string          `json:"unavailableSources,omitempty"`
	Note        string            `json:"note,omitempty"`
}

type searchSource struct {
	name string
	find func(context.Context, *http.Client, string) ([]webSearchResult, error)
}

func searchPublicWeb(ctx context.Context, client *http.Client, query string) (webSearchResponse, error) {
	var sources []searchSource
	braveKey := strings.TrimSpace(os.Getenv("ZENTRALE_BRAVE_SEARCH_API_KEY"))
	site := siteNavigationDomain(query)
	if python := strings.TrimSpace(os.Getenv("ZENTRALE_DDGS_PYTHON")); python != "" {
		sources = append(sources, searchSource{"Yahoo (DDGS)", ddgsSearch(python, braveKey == "" && site == "")})
	}
	if braveKey != "" {
		sources = append(sources, searchSource{"Brave Search API", braveSearch(braveKey)})
	} else if site != "" {
		sources = append(sources, searchSource{site + " (site links)", siteNavigationSearch(site)})
	}
	if len(sources) == 0 {
		return webSearchResponse{}, errors.New("public web search is not configured; set ZENTRALE_DDGS_PYTHON or ZENTRALE_BRAVE_SEARCH_API_KEY")
	}

	return searchFromSources(ctx, client, query, sources)
}

func searchFromSources(ctx context.Context, client *http.Client, query string, sources []searchSource) (webSearchResponse, error) {
	query = strings.TrimSpace(query)
	if len(query) == 0 || len(query) > 512 {
		return webSearchResponse{}, errors.New("search requires 1–512 bytes of search terms")
	}

	matches := searchMatcher(query)
	var failures, checked []string

	for _, source := range sources {
		searchCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		items, err := source.find(searchCtx, client, query)
		cancel()

		if err := ctx.Err(); err != nil {
			return webSearchResponse{}, err
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", source.name, err))
			continue
		}

		checked = append(checked, source.name)
		if results := relevantResults(items, matches); len(results) > 0 {
			return webSearchResponse{Source: source.name, Results: results, Unavailable: failures}, nil
		}
	}

	if len(checked) == 0 {
		return webSearchResponse{}, fmt.Errorf("public web search unavailable (%s)", strings.Join(failures, "; "))
	}

	return webSearchResponse{
		Source:      strings.Join(checked, ", "),
		Results:     []webSearchResult{},
		Unavailable: failures,
		Note:        "The sources that responded did not return relevant results. This does not mean no public sources exist. Try fewer search terms or narrow to a known source with site:example.org. Ask for context if needed; do not infer an answer from an empty result.",
	}, nil
}

func normalizedWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

func searchWord(word string) string {
	switch word {
	case "docs", "documentation", "dokumentation":
		return "doc"
	default:
		return word
	}
}

var searchQuoteReplacer = strings.NewReplacer("“", `"`, "”", `"`, "„", `"`, "«", `"`, "»", `"`)

func searchMatcher(query string) func(webSearchResult) bool {
	parts := strings.Split(searchQuoteReplacer.Replace(query), `"`)
	var phrases []string
	terms := make(map[string]bool)
	var site string
	stop := " a an the of in at on to and is who what how der die das ein eine ist wer was wie und von für im zu "

	for i, part := range parts {
		if i%2 == 1 && i+1 < len(parts) {
			if words := normalizedWords(part); len(words) > 0 {
				phrases = append(phrases, " "+strings.Join(words, " ")+" ")
			}
			continue
		}

		for _, field := range strings.Fields(part) {
			if host, ok := strings.CutPrefix(strings.ToLower(field), "site:"); ok && host != "" && !strings.ContainsAny(host, "/:") {
				site = host
				continue
			}
			for _, word := range normalizedWords(field) {
				if !strings.Contains(stop, " "+word+" ") {
					terms[searchWord(word)] = true
				}
			}
		}
	}

	return func(item webSearchResult) bool {
		if site != "" {
			link, err := url.Parse(item.URL)
			if err != nil {
				return false
			}
			if !inSearchDomain(link.Hostname(), site) {
				return false
			}
		}

		if len(phrases) > 0 {
			for _, phrase := range phrases {
				title := " " + strings.Join(normalizedWords(item.Title), " ") + " "
				snippet := " " + strings.Join(normalizedWords(item.Description), " ") + " "
				if !strings.Contains(title, phrase) && !strings.Contains(snippet, phrase) {
					return false
				}
			}
			return true
		}

		if len(terms) == 0 {
			return site != ""
		}
		found := map[string]bool{}
		for _, word := range normalizedWords(item.Title + " " + item.Description + " " + item.URL) {
			word = searchWord(word)
			if terms[word] {
				found[word] = true
			}
		}
		return len(found)*3 >= len(terms)*2
	}
}

func inSearchDomain(host, site string) bool {
	host = strings.ToLower(host)
	return host == site || strings.HasSuffix(host, "."+site)
}

func relevantResults(items []webSearchResult, matches func(webSearchResult) bool) []webSearchResult {
	results := make([]webSearchResult, 0, min(10, len(items)))
	seen := map[string]bool{}

	for _, item := range items {
		item.URL = strings.TrimSpace(item.URL)
		item.Title = strings.TrimSpace(item.Title)
		link, err := url.Parse(item.URL)
		if err != nil || validateChatWebURL(link) != nil || item.Title == "" || seen[item.URL] || !matches(item) {
			continue
		}

		seen[item.URL] = true
		results = append(results, item)
		if len(results) == 10 {
			break
		}
	}
	return results
}
