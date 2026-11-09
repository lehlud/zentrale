package webapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

func previewCookiePrefix(root string) string {
	digest := sha256.Sum256([]byte(root))
	return "zpreview_" + hex.EncodeToString(digest[:8]) + "_"
}

func previewCookieName(root, name, path string) string {
	var metadata bytes.Buffer
	encoder := json.NewEncoder(&metadata)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode([2]string{name, path})
	return previewCookiePrefix(root) + base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(metadata.Bytes(), []byte("\n")))
}

func previewCookieMetadata(root, encoded string) (name, path string, valid bool) {
	prefix := previewCookiePrefix(root)
	if !strings.HasPrefix(encoded, prefix) {
		return "", "", false
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, prefix))
	if err != nil {
		return "", "", false
	}
	var metadata [2]string
	if json.Unmarshal(data, &metadata) != nil || metadata[0] == "" || !strings.HasPrefix(metadata[1], "/") {
		return "", "", false
	}
	return metadata[0], metadata[1], true
}

func previewCookiePathMatch(path, scope string) bool {
	return path == scope || strings.HasPrefix(path, scope) &&
		(strings.HasSuffix(scope, "/") || len(path) > len(scope) && path[len(scope)] == '/')
}

// Cookies have no port boundary. Keep that behavior across a preview's frontend
// and APIs while isolating their names and logical paths from Zentrale itself.
func forwardPreviewCookies(incoming, outgoing *http.Request, root, path string, omit bool) {
	outgoing.Header.Del("Cookie")
	if omit {
		return
	}
	type scopedCookie struct {
		cookie *http.Cookie
		path   string
	}
	var cookies []scopedCookie
	for _, cookie := range incoming.Cookies() {
		name, scope, valid := previewCookieMetadata(root, cookie.Name)
		if valid && previewCookiePathMatch(path, scope) {
			copy := *cookie
			copy.Name = name
			cookies = append(cookies, scopedCookie{&copy, scope})
		}
	}
	sort.SliceStable(cookies, func(i, j int) bool { return len(cookies[i].path) > len(cookies[j].path) })
	for _, cookie := range cookies {
		outgoing.AddCookie(cookie.cookie)
	}
}

func scopePreviewCookie(cookie *http.Cookie, root, requestPath string, secure bool) {
	path := cookie.Path
	if !strings.HasPrefix(path, "/") {
		path = requestPath[:strings.LastIndex(requestPath, "/")+1]
		if path != "/" {
			path = strings.TrimSuffix(path, "/")
		}
	}
	if path == "" {
		path = "/"
	}
	cookie.Name = previewCookieName(root, cookie.Name, path)
	cookie.Path, cookie.Domain, cookie.Secure = root, "", secure
}
