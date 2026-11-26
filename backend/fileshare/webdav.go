package fileshare

import (
	"context"
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	maxDAVMetadataBytes = 2 << 20
	davPropfindBody     = `<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getcontentlength/></d:prop></d:propfind>`
)

var shareHTTPClient = &http.Client{
	Transport: &http.Transport{
		// Do not send configured credentials through environment proxies.
		DialContext:            (&net.Dialer{Timeout: dialTimeout}).DialContext,
		TLSHandshakeTimeout:    dialTimeout,
		ResponseHeaderTimeout:  10 * time.Second,
		IdleConnTimeout:        30 * time.Second,
		MaxIdleConns:           16,
		MaxResponseHeaderBytes: 32 << 10,
		DisableCompression:     true,
		ForceAttemptHTTP2:      true,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type davMultistatus struct {
	XMLName   xml.Name      `xml:"DAV: multistatus"`
	Responses []davResponse `xml:"DAV: response"`
}

type davResponse struct {
	Hrefs     []string      `xml:"DAV: href"`
	Status    string        `xml:"DAV: status"`
	PropStats []davPropStat `xml:"DAV: propstat"`
}

type davPropStat struct {
	Status string `xml:"DAV: status"`
	Prop   struct {
		Types []struct {
			Children []struct{ XMLName xml.Name } `xml:",any"`
		} `xml:"DAV: resourcetype"`
		Size int64 `xml:"DAV: getcontentlength"`
	} `xml:"DAV: prop"`
}

// The DAV server must itself confine backing-filesystem lookups to the
// configured location. WebDAV exposes redirect references but cannot identify
// OS-level symlinks a server chooses to present as ordinary DAV resources.
// We never follow redirects or DAV redirect references, even within the origin.
func (s *Share) listDAV(ctx context.Context, root, path []string) ([]Entry, error) {
	if err := s.verifyDAVAncestors(ctx, root, path); err != nil {
		return nil, err
	}
	return s.propfind(ctx, path, true, true)
}

func (s *Share) readDAV(ctx context.Context, root, path []string, maxBytes int64) ([]byte, error) {
	if err := s.verifyDAVAncestors(ctx, root, path); err != nil {
		return nil, err
	}
	if _, err := s.propfind(ctx, path, false, false); err != nil {
		return nil, err
	}

	response, err := s.davRequest(ctx, http.MethodGet, path, false, false)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, davStatusError(response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		return nil, ErrTooLarge
	}
	return readBounded(ctx, response.Body, maxBytes)
}

func (s *Share) verifyDAVAncestors(ctx context.Context, root, path []string) error {
	// The endpoint path is the trusted DAV base. Check every configured and
	// user-provided subdirectory below it before accessing the final resource.
	first := 1
	if len(root) == 0 && len(path) != 0 {
		first = 0
	}
	for i := first; i < len(path); i++ {
		if _, err := s.propfind(ctx, path[:i], true, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *Share) propfind(ctx context.Context, path []string, isDir, listChildren bool) ([]Entry, error) {
	response, err := s.davRequest(ctx, "PROPFIND", path, isDir, listChildren)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusMultiStatus {
		return nil, davStatusError(response.StatusCode)
	}
	data, err := readBounded(ctx, response.Body, maxDAVMetadataBytes)
	if err != nil {
		return nil, err
	}

	var status davMultistatus
	if err := xml.Unmarshal(data, &status); err != nil {
		return nil, ErrUnavailable
	}
	if len(status.Responses) > maxListEntries+1 {
		return nil, ErrTooLarge
	}

	requested := slices.Concat(s.base, path)
	matched := false
	var entries []Entry
	seen := make(map[string]int)
	for _, item := range status.Responses {
		if len(item.Hrefs) != 1 {
			continue
		}
		parts, ok := s.davHrefParts(item.Hrefs[0])
		if !ok || len(parts) < len(requested) || !slices.Equal(parts[:len(requested)], requested) {
			continue
		}

		entry, valid := item.entry()
		if len(parts) == len(requested) {
			if !valid || matched || entry.IsDir != isDir {
				return nil, ErrUnsafePath
			}
			matched = true
			continue
		}
		if !listChildren || len(parts) != len(requested)+1 {
			continue
		}

		entry.Name = parts[len(requested)]
		if idx, duplicate := seen[entry.Name]; duplicate {
			entries[idx].Name = ""
			continue
		}
		seen[entry.Name] = len(entries)
		if !valid {
			entry.Name = ""
		}
		entries = append(entries, entry)
	}
	if !matched {
		return nil, ErrUnsafePath
	}
	return slices.DeleteFunc(entries, func(e Entry) bool { return e.Name == "" }), nil
}

func (s *Share) davRequest(ctx context.Context, method string, path []string, isDir, listChildren bool) (*http.Response, error) {
	endpoint := s.endpoint
	endpoint.Path = "/" + strings.Join(slices.Concat(s.base, path), "/")
	if isDir && endpoint.Path != "/" {
		endpoint.Path += "/"
	}
	endpoint.RawPath = ""

	var body io.Reader
	if method == "PROPFIND" {
		body = strings.NewReader(davPropfindBody)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, operationError(ctx)
	}
	if s.username != "" || s.password != "" {
		request.SetBasicAuth(s.username, s.password)
	}
	request.Header.Set("Apply-To-Redirect-Ref", "T")
	if method == "PROPFIND" {
		request.Header.Set("Content-Type", "application/xml; charset=utf-8")
		if listChildren {
			request.Header.Set("Depth", "1")
		} else {
			request.Header.Set("Depth", "0")
		}
	}

	response, err := shareHTTPClient.Do(request)
	if err != nil {
		return nil, operationError(ctx)
	}
	return response, nil
}

func (s *Share) davHrefParts(href string) ([]string, bool) {
	href = strings.TrimSpace(href)
	u, err := url.Parse(href)
	if err != nil || u.Opaque != "" || u.User != nil || u.ForceQuery || u.RawQuery != "" ||
		strings.Contains(href, "#") || !strings.HasPrefix(u.EscapedPath(), "/") {
		return nil, false
	}
	if u.IsAbs() {
		if !strings.EqualFold(u.Scheme, s.endpoint.Scheme) ||
			!strings.EqualFold(u.Hostname(), s.endpoint.Hostname()) || davPort(u) != davPort(&s.endpoint) {
			return nil, false
		}
	} else if u.Host != "" || strings.HasPrefix(href, "//") {
		return nil, false
	}

	parts, err := urlPathSegments(u)
	return parts, err == nil && validSegments(parts, ProtocolWebDAV, false)
}

func davPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func (item davResponse) entry() (Entry, bool) {
	if item.Status != "" && !davStatusOK(item.Status) {
		return Entry{}, false
	}
	var entry Entry
	found := false
	for _, props := range item.PropStats {
		if !davStatusOK(props.Status) || len(props.Prop.Types) == 0 {
			continue
		}
		if found || len(props.Prop.Types) != 1 {
			return Entry{}, false
		}
		found = true
		for _, child := range props.Prop.Types[0].Children {
			if child.XMLName != (xml.Name{Space: "DAV:", Local: "collection"}) {
				// Reject redirect references and unknown resource kinds.
				return Entry{}, false
			}
			entry.IsDir = true
		}
		entry.Size = max(props.Prop.Size, 0)
	}
	return entry, found
}

func davStatusOK(status string) bool {
	parts := strings.Fields(status)
	return len(parts) >= 2 && strings.HasPrefix(parts[0], "HTTP/") && parts[1] == "200"
}

func davStatusError(status int) error {
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		return ErrNotFound
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrAccessDenied
	case status >= 300 && status < 400:
		return ErrUnsafePath
	default:
		return ErrUnavailable
	}
}
