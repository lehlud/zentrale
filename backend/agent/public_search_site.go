package agent

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var searchDomainPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,62}$`)

// Use the homepage for explicit site navigation queries. Quoted searches often
// target individual articles and instead keep the inexpensive Yahoo retry.
func siteNavigationDomain(query string) string {
	if len(query) > 512 || strings.Contains(searchQuoteReplacer.Replace(query), `"`) {
		return ""
	}

	var site string
	for _, field := range strings.Fields(strings.ToLower(query)) {
		if host, ok := strings.CutPrefix(field, "site:"); ok {
			if site != "" || len(host) > 253 || !searchDomainPattern.MatchString(host) {
				return ""
			}
			site = host
		}
	}

	return site
}

func siteNavigationSearch(site string) func(context.Context, *http.Client, string) ([]webSearchResult, error) {
	return func(ctx context.Context, client *http.Client, _ string) ([]webSearchResult, error) {
		siteClient := *client
		siteClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 || request.URL.Scheme != "https" || !inSearchDomain(request.URL.Hostname(), site) {
				return http.ErrUseLastResponse
			}
			return validateChatWebURL(request.URL)
		}

		// Retain the guarded transport. Only the homepage is read; links are
		// returned to the model but never visited by this fallback.
		response, err := openPublicWeb(ctx, &siteClient, "https://"+site+"/", "text/html, application/xhtml+xml")
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()

		mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if mediaType != "text/html" && mediaType != "application/xhtml+xml" {
			return nil, errors.New("the site homepage is not HTML")
		}
		page, err := html.Parse(io.LimitReader(response.Body, webBodyLimit))
		if err != nil {
			return nil, errors.New("the site homepage could not be read")
		}

		var results []webSearchResult
		for node := range page.Descendants() {
			if node.Type != html.ElementNode || node.Data != "a" {
				continue
			}
			for _, attr := range node.Attr {
				if attr.Key != "href" || attr.Val == "" || strings.HasPrefix(attr.Val, "#") {
					continue
				}
				link, err := response.Request.URL.Parse(attr.Val)
				if err != nil || link.Scheme != "https" || !inSearchDomain(link.Hostname(), site) || validateChatWebURL(link) != nil {
					break
				}

				link.Fragment = ""
				title := navigationLinkText(node)
				results = append(results, webSearchResult{Title: title, URL: link.String()})
				break
			}
		}

		return results, nil
	}
}

func navigationLinkText(link *html.Node) string {
	var parts []string
	for child := range link.Descendants() {
		if child.Type == html.TextNode {
			parts = append(parts, strings.Fields(child.Data)...)
		}
	}

	title := strings.Join(parts, " ")
	return strings.ToValidUTF8(title[:min(400, len(title))], "")
}
