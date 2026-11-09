package webapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPreviewCookiesPreservePathsAndSharePortsWithoutLeakingOtherSessions(t *testing.T) {
	root := "/preview/project/session/"
	incoming := httptest.NewRequest("GET", "https://zentrale.example"+root+"1235/api/example", nil)
	for _, cookie := range []*http.Cookie{
		{Name: "zentrale_session", Value: "private-login"},
		{Name: previewCookieName(root, "zentrale_session", "/"), Value: "app-login"},
		{Name: previewCookieName(root, "token", "/"), Value: "default"},
		{Name: previewCookieName(root, "token", "/api"), Value: "specific"},
		{Name: previewCookieName(root, "hidden", "/apis"), Value: "wrong-path"},
		{Name: previewCookieName("/preview/other/session/", "token", "/"), Value: "other-project"},
	} {
		incoming.AddCookie(cookie)
	}
	outgoing := httptest.NewRequest("GET", "http://localhost:1235/api/example", nil)
	forwardPreviewCookies(incoming, outgoing, root, "/api/example", false)
	cookies := outgoing.Cookies()
	if len(cookies) != 3 || cookies[0].Name != "token" || cookies[0].Value != "specific" {
		t.Fatal("cookie precedence or path confinement was lost", cookies)
	}
	if cookie, err := outgoing.Cookie("zentrale_session"); err != nil || cookie.Value != "app-login" {
		t.Fatal("application login collided with Zentrale", cookie, err)
	}
	forwardPreviewCookies(incoming, outgoing, root, "/api/example", true)
	if len(outgoing.Cookies()) != 0 {
		t.Fatal("credentials: omit still sent application cookies")
	}

	cookie := &http.Cookie{Name: "session", Value: "value"}
	scopePreviewCookie(cookie, root, "/a%2Fb/login", true)
	name, path, valid := previewCookieMetadata(root, cookie.Name)
	if !valid || name != "session" || path != "/a%2Fb" || cookie.Path != root || !cookie.Secure {
		t.Fatal("default cookie path changed", cookie, name, path)
	}
}
