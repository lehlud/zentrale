package fileshare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

const (
	domainShareAPIPrefix    = "/api/agent/v1"
	domainShareDocumentRoot = "@document"
	maxDomainShareMetadata  = 2 << 20
)

var domainShareDocumentID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (s *Share) domainShareGet(ctx context.Context, username, operation string, query url.Values, window readWindow) ([]byte, error) {
	if username == "" || len(username) > 256 || strings.ContainsAny(username, "/@") || strings.ContainsFunc(username, unicode.IsControl) {
		return nil, ErrAccessDenied
	}
	query.Set("username", username)
	endpoint := s.endpoint
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	if !strings.HasSuffix(endpoint.Path, domainShareAPIPrefix) {
		endpoint.Path += domainShareAPIPrefix
	}
	endpoint.Path += operation
	endpoint.RawPath = ""
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+s.password)
	response, err := shareHTTPClient.Do(request)
	if err != nil {
		return nil, operationError(ctx)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, davStatusError(response.StatusCode)
	}
	if window.exceeds(response.ContentLength) {
		return nil, ErrTooLarge
	}
	return window.read(ctx, response.Body)
}

func (s *Share) domainShareJSON(ctx context.Context, username, operation string, query url.Values, out any) error {
	body, err := s.domainShareGet(ctx, username, operation, query, readWindow{limit: maxDomainShareMetadata})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Share) listDomainShare(ctx context.Context, username string, path []string) ([]Entry, error) {
	entries := []Entry{}
	if len(path) == 0 {
		var response struct {
			Sources []struct{ ID, Label string }
		}
		if err := s.domainShareJSON(ctx, username, "/list", url.Values{}, &response); err != nil {
			return nil, err
		}
		if response.Sources == nil {
			return nil, ErrUnavailable
		}

		for _, root := range response.Sources {
			if root.ID != domainShareDocumentRoot && validSegment(root.ID, s.protocol) {
				entries = append(entries, Entry{Name: root.ID, Label: root.Label, IsDir: true})
			}
		}
		return entries, nil
	}

	query := url.Values{"source": {path[0]}, "path": {strings.Join(path[1:], "/")}, "limit": {"200"}}
	count := 0
	for {
		var page struct {
			Items []struct {
				Name, Type string
				Size       int64
			}
			NextAfter string
		}
		if err := s.domainShareJSON(ctx, username, "/list", query, &page); err != nil {
			return nil, err
		}
		count += len(page.Items)
		if len(page.Items) > 200 || count > maxListEntries {
			return nil, ErrTooLarge
		}
		for _, item := range page.Items {
			if validSegment(item.Name, s.protocol) && (item.Type == "file" || item.Type == "directory") {
				entries = append(entries, Entry{Name: item.Name, IsDir: item.Type == "directory", Size: item.Size})
			}
		}
		if page.NextAfter == "" {
			return entries, nil
		}
		if page.NextAfter <= query.Get("after") || len(page.Items) == 0 || len(page.NextAfter) > 1024 {
			return nil, ErrUnavailable
		}
		query.Set("after", page.NextAfter)
	}
}

func (s *Share) readDomainShare(ctx context.Context, username string, path []string, window readWindow) ([]byte, error) {
	if len(path) < 2 {
		return nil, ErrInvalidPath
	}
	if path[0] == domainShareDocumentRoot {
		if len(path) != 2 || !domainShareDocumentID.MatchString(path[1]) {
			return nil, ErrInvalidPath
		}
		var document struct{ Text string }
		if err := s.domainShareJSON(ctx, username, "/documents/"+path[1], url.Values{}, &document); err != nil {
			return nil, err
		}
		if document.Text == "" {
			return nil, ErrNotFound
		}
		return window.read(ctx, strings.NewReader(document.Text))
	}

	query := url.Values{"source": {path[0]}, "path": {strings.Join(path[1:], "/")}}
	return s.domainShareGet(ctx, username, "/file", query, window)
}

type ShareSearchResult struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Path     string `json:"path"`
	TextPath string `json:"textPath,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
}

type ShareSearchPage struct {
	Items      []ShareSearchResult `json:"items"`
	NextCursor string              `json:"nextCursor,omitempty"`
	Incomplete bool                `json:"incomplete,omitempty"`
}

// Search queries the DomainShare index within the same account scope as List and Read.
func (s *Share) Search(ctx context.Context, username, terms, cursor string) (ShareSearchPage, error) {
	if s.protocol != ProtocolDomainShare {
		return ShareSearchPage{}, ErrUnavailable
	}
	terms = strings.TrimSpace(terms)
	if terms == "" || len(terms) > 1024 || len(cursor) > 8192 {
		return ShareSearchPage{}, ErrInvalidPath
	}

	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	query := url.Values{"q": {terms}, "limit": {"12"}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var response struct {
		Items            []struct{ ID, Name, Source, SourceID, Path, Snippet string }
		NextCursor       string
		SearchIncomplete bool
	}
	if err := s.domainShareJSON(ctx, username, "/search", query, &response); err != nil {
		return ShareSearchPage{}, err
	}
	if len(response.Items) > 12 || len(response.NextCursor) > 8192 || (cursor != "" && response.NextCursor == cursor) {
		return ShareSearchPage{}, ErrUnavailable
	}
	result := ShareSearchPage{Items: []ShareSearchResult{}, NextCursor: response.NextCursor, Incomplete: response.SearchIncomplete}
	for _, item := range response.Items {
		if !domainShareDocumentID.MatchString(item.ID) {
			continue
		}

		resultItem := ShareSearchResult{
			Name: item.Name, Source: item.Source, Path: domainShareDocumentRoot + "/" + item.ID,
		}
		if item.Path != "" && item.SourceID != domainShareDocumentRoot && validSegment(item.SourceID, s.protocol) {
			path := item.SourceID + "/" + item.Path
			if _, _, err := s.resolve(username, path); err == nil {
				resultItem.Path, resultItem.TextPath = path, resultItem.Path
			}
		}
		for i := range item.Snippet {
			if i >= 640 {
				item.Snippet = item.Snippet[:i]
				break
			}
		}
		resultItem.Snippet = item.Snippet
		result.Items = append(result.Items, resultItem)
	}
	return result, nil
}
