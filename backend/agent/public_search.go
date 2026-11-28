package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
)

const publicSearchQueryHelp = "Use short public search terms, never secrets or private file contents. " +
	"Start with unquoted keywords, including full names. Add one known detail or a known source (site:example.org) to distinguish people. " +
	"Use separate queries for alternative spellings or sources. " +
	"Do not guess which person is meant."

const publicSearchExactHelp = "Use only when the user explicitly asks for an exact wording. Put the required phrase in double quotes."

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

func searchPublicWeb(ctx context.Context, client *http.Client, query string, exact bool) (webSearchResponse, error) {
	query = strings.TrimSpace(searchQuoteReplacer.Replace(query))
	if len(query) == 0 || len(query) > 512 {
		return webSearchResponse{}, errors.New("search requires 1–512 bytes of search terms")
	}
	if quotes := strings.Count(query, `"`); exact && (quotes < 2 || quotes%2 != 0) {
		return webSearchResponse{}, errors.New("exact search requires a phrase in paired double quotes")
	}
	var preferred func(webSearchResult) bool
	if !exact && strings.Contains(query, `"`) {
		preferred = searchMatcher(query)
		query = strings.Join(strings.Fields(strings.ReplaceAll(query, `"`, " ")), " ")
		if query == "" {
			return webSearchResponse{}, errors.New("search requires 1–512 bytes of search terms")
		}
	}

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

	return searchFromSources(ctx, client, query, preferred, sources)
}

func searchFromSources(ctx context.Context, client *http.Client, query string, preferred func(webSearchResult) bool, sources []searchSource) (webSearchResponse, error) {
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
			if preferred != nil {
				sort.SliceStable(results, func(i, j int) bool {
					return preferred(results[i]) && !preferred(results[j])
				})
			}
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
	var phrases [][]string
	terms := make(map[string]bool)
	var sites []string
	stop := " a an the of in at on to and or is who what how der die das ein eine ist wer was wie und oder von für im zu "

	for i, part := range parts {
		if i%2 == 1 && i+1 < len(parts) {
			if words := normalizedWords(part); len(words) > 0 {
				phrase := " " + strings.Join(words, " ") + " "
				if last := len(phrases) - 1; last >= 0 && strings.EqualFold(strings.TrimSpace(parts[i-1]), "or") {
					phrases[last] = append(phrases[last], phrase)
				} else {
					phrases = append(phrases, []string{phrase})
				}
			}
			continue
		}

		for _, field := range strings.Fields(part) {
			if host, ok := strings.CutPrefix(strings.ToLower(field), "site:"); ok && host != "" && !strings.ContainsAny(host, "/:") {
				sites = append(sites, host)
				continue
			}
			if strings.HasPrefix(field, "-") {
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
		if len(sites) != 0 {
			link, err := url.Parse(item.URL)
			if err != nil {
				return false
			}
			inScope := false
			for _, site := range sites {
				if inSearchDomain(link.Hostname(), site) {
					inScope = true
					break
				}
			}
			if !inScope {
				return false
			}
		}

		if len(phrases) > 0 {
			title := " " + strings.Join(normalizedWords(item.Title), " ") + " "
			snippet := " " + strings.Join(normalizedWords(item.Description), " ") + " "
			for _, alternatives := range phrases {
				found := false
				for _, phrase := range alternatives {
					if strings.Contains(title, phrase) || strings.Contains(snippet, phrase) {
						found = true
						break
					}
				}
				if !found {
					return false
				}
			}
			return true
		}

		if len(terms) == 0 {
			return len(sites) != 0
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
