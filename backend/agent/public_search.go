package agent

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

type webSearchResult struct {
	Title       string `xml:"title" json:"title"`
	URL         string `xml:"link" json:"url"`
	Description string `xml:"description" json:"snippet,omitempty"`
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
	sources := []searchSource{
		{"Bing", searchRSS},
		{"DuckDuckGo", searchDuckDuckGo},
		{"Wikipedia (de)", wikipediaSearch("de")},
		{"Wikipedia (en)", wikipediaSearch("en")},
	}
	if key := strings.TrimSpace(os.Getenv("ZENTRALE_BRAVE_SEARCH_API_KEY")); key != "" {
		sources = []searchSource{{"Brave", func(ctx context.Context, client *http.Client, terms string) ([]webSearchResult, error) {
			return searchBrave(ctx, client, terms, key)
		}}}
	} else if python := strings.TrimSpace(os.Getenv("ZENTRALE_DDGS_PYTHON")); python != "" {
		sources = append([]searchSource{{"Yahoo (DDGS)", ddgsSearch(python)}}, sources...)
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
			return webSearchResponse{Source: source.name, Results: results}, nil
		}
	}

	if len(checked) == 0 {
		return webSearchResponse{}, fmt.Errorf("public web search unavailable (%s)", strings.Join(failures, "; "))
	}

	return webSearchResponse{
		Source:      strings.Join(checked, ", "),
		Results:     []webSearchResult{},
		Unavailable: failures,
		Note:        "The sources that responded did not return relevant results. This does not mean no public sources exist. Ask for context if needed; do not infer an answer from the empty result.",
	}, nil
}

func normalizedWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
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
					terms[word] = true
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
			host := strings.ToLower(link.Hostname())
			if host != site && !strings.HasSuffix(host, "."+site) {
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
			if terms[word] {
				found[word] = true
			}
		}
		return len(found)*3 >= len(terms)*2
	}
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

func searchRSS(ctx context.Context, client *http.Client, query string) ([]webSearchResult, error) {
	address := "https://www.bing.com/search?format=rss&q=" + url.QueryEscape(query)
	response, err := openPublicWeb(ctx, client, address, "application/rss+xml, application/xml, text/xml")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	var feed struct {
		XMLName xml.Name          `xml:"rss"`
		Items   []webSearchResult `xml:"channel>item"`
	}
	if err := xml.NewDecoder(io.LimitReader(response.Body, webBodyLimit)).Decode(&feed); err != nil {
		return nil, errors.New("the search feed did not return readable RSS")
	}

	return feed.Items, nil
}

func searchDuckDuckGo(ctx context.Context, client *http.Client, query string) ([]webSearchResult, error) {
	address := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	response, err := openPublicWeb(ctx, client, address, "text/html")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	page, err := html.Parse(io.LimitReader(response.Body, webBodyLimit))
	if err != nil {
		return nil, errors.New("the search page could not be read")
	}

	var results []webSearchResult
	for node := range page.Descendants() {
		if !hasHTMLClass(node, "result") {
			continue
		}

		var item webSearchResult
		for element := range node.Descendants() {
			switch {
			case element.Data == "a" && hasHTMLClass(element, "result__a"):
				item.Title = htmlNodeText(element)
				for _, attr := range element.Attr {
					if attr.Key == "href" {
						item.URL = duckDuckGoResultURL(attr.Val)
						break
					}
				}
			case hasHTMLClass(element, "result__snippet"):
				item.Description = htmlNodeText(element)
			}
		}
		if item.URL != "" {
			results = append(results, item)
		}
	}

	return results, nil
}

func duckDuckGoResultURL(raw string) string {
	base := &url.URL{Scheme: "https", Host: "html.duckduckgo.com"}
	link, err := base.Parse(raw)
	if err != nil {
		return ""
	}

	if (link.Hostname() == "duckduckgo.com" || link.Hostname() == base.Host) && link.Path == "/l/" {
		return link.Query().Get("uddg")
	}
	return link.String()
}

func hasHTMLClass(node *html.Node, class string) bool {
	for _, attr := range node.Attr {
		if attr.Key == "class" {
			for _, name := range strings.Fields(attr.Val) {
				if name == class {
					return true
				}
			}
		}
	}
	return false
}

func htmlNodeText(node *html.Node) string {
	var text strings.Builder
	for child := range node.Descendants() {
		if child.Type == html.TextNode {
			text.WriteString(child.Data)
		}
	}
	return strings.Join(strings.Fields(text.String()), " ")
}

func wikipediaSearch(language string) func(context.Context, *http.Client, string) ([]webSearchResult, error) {
	return func(ctx context.Context, client *http.Client, query string) ([]webSearchResult, error) {
		// For an exact-name question, search the encyclopedia for the name,
		// not the repeated chat wording. The original match rules still apply.
		terms := searchQuoteReplacer.Replace(query)
		if parts := strings.Split(terms, `"`); len(parts) == 3 && strings.TrimSpace(parts[1]) != "" {
			terms = `"` + strings.TrimSpace(parts[1]) + `"`
		}

		origin := "https://" + language + ".wikipedia.org"
		address := origin + "/w/rest.php/v1/search/page?limit=8&q=" + url.QueryEscape(terms)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "Zentrale/1.0 (https://github.com/lehlud/zentrale)")
		response, err := doPublicWeb(client, request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()

		var payload struct {
			Pages []struct {
				Key, Title, Excerpt string
			} `json:"pages"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, webBodyLimit)).Decode(&payload); err != nil || payload.Pages == nil {
			return nil, errors.New("the encyclopedia did not return search results in its expected format")
		}

		results := make([]webSearchResult, 0, len(payload.Pages))
		for _, page := range payload.Pages {
			if page.Key == "" {
				continue
			}
			results = append(results, webSearchResult{
				Title:       page.Title,
				URL:         origin + "/wiki/" + url.PathEscape(page.Key),
				Description: wikipediaSnippet(page.Excerpt),
			})
		}
		return results, nil
	}
}

func wikipediaSnippet(excerpt string) string {
	// Wikimedia highlights matched words with markup. Search/match plain text.
	page, err := html.Parse(strings.NewReader(excerpt))
	if err != nil {
		return ""
	}
	return htmlNodeText(page)
}

func searchBrave(ctx context.Context, client *http.Client, query, key string) ([]webSearchResult, error) {
	address := "https://api.search.brave.com/res/v1/web/search?count=10&text_decorations=false&q=" + url.QueryEscape(query)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Subscription-Token", key)

	// Do not forward the subscription token, even if the provider redirects.
	keyedClient := *client
	keyedClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := doPublicWeb(&keyedClient, request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	var payload struct {
		Web struct {
			Results []struct {
				Title, URL, Description string
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, webBodyLimit)).Decode(&payload); err != nil {
		return nil, errors.New("the Brave search API did not return readable JSON")
	}

	results := make([]webSearchResult, 0, len(payload.Web.Results))
	for _, item := range payload.Web.Results {
		results = append(results, webSearchResult{Title: item.Title, URL: item.URL, Description: item.Description})
	}
	return results, nil
}
