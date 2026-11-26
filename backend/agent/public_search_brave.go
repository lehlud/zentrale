package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
)

func braveSearch(key string) func(context.Context, *http.Client, string) ([]webSearchResult, error) {
	return func(ctx context.Context, client *http.Client, query string) ([]webSearchResult, error) {
		address := "https://api.search.brave.com/res/v1/web/search?count=10&text_decorations=false&q=" + url.QueryEscape(query)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-Subscription-Token", key)

		// Never forward the subscription token to a redirect destination.
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
		reader := io.LimitReader(response.Body, webBodyLimit)
		if err := json.NewDecoder(reader).Decode(&payload); err != nil {
			return nil, errors.New("the Brave search API did not return readable JSON")
		}

		results := make([]webSearchResult, 0, len(payload.Web.Results))
		for _, item := range payload.Web.Results {
			results = append(results, webSearchResult{Title: item.Title, URL: item.URL, Description: item.Description})
		}
		return results, nil
	}
}
