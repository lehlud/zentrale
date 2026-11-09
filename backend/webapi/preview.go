package webapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	_ "embed"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/domain"
	xhtml "golang.org/x/net/html"
)

//go:embed preview/runtime.js
var previewRuntime string

//go:embed preview/worker.js
var previewWorker string

type previewCredentialsKey struct{}

func (server *Server) preview(requestContext echo.Context) error {
	request := requestContext.Request()
	if !server.Auth.Valid(server.token(request)) {
		return echo.ErrUnauthorized
	}
	if !server.sameOrigin(request) {
		return echo.ErrForbidden
	}

	var project domain.Project
	if err := server.Store.Load("project", requestContext.Param("project"), &project); err != nil {
		return echo.ErrNotFound
	}
	sessionID := requestContext.Param("session")
	if _, err := server.Engine.Editors.Info(project, sessionID); err != nil {
		return echo.NewHTTPError(http.StatusConflict, "preview session expired; reopen VS Code")
	}
	root := "/preview/" + project.ID + "/" + sessionID + "/"
	asset := requestContext.Param("port") == "_zentrale"
	if asset {
		requestContext.Response().Header().Set("Cache-Control", "no-store")
		requestContext.Response().Header().Set("Content-Type", "text/javascript; charset=utf-8")
		switch requestContext.Param("*") {
		case "runtime.js":
			return requestContext.String(http.StatusOK, previewRuntime)
		case "worker.js":
			requestContext.Response().Header().Set("Service-Worker-Allowed", root)
			return requestContext.String(http.StatusOK, previewWorker)
		default:
			return echo.ErrNotFound
		}
	}

	port, err := strconv.Atoi(requestContext.Param("port"))
	if err != nil || port < 1 || port > 65535 || port == 9842 || strconv.Itoa(port) != requestContext.Param("port") {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid preview port")
	}
	prefix := root + strconv.Itoa(port) + "/"
	if request.URL.Path == strings.TrimSuffix(prefix, "/") {
		return requestContext.Redirect(http.StatusTemporaryRedirect, prefix+querySuffix(request.URL))
	}

	// A scoped worker routes root-relative assets for this tab without touching
	// Zentrale or another preview. Its first navigation enables routing before
	// the application's module graph starts loading.
	if strings.Contains(request.Header.Get("Accept"), "text/html") &&
		request.Header.Get("X-Zentrale-Preview") != "1" && request.Method == http.MethodGet {
		requestContext.Response().Header().Set("Cache-Control", "no-store")
		requestContext.Response().Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; worker-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'self'")
		return requestContext.HTML(http.StatusOK, "<!doctype html><meta charset=utf-8><title>Preview · Zentrale</title><p id=preview-status>Connecting preview…</p>"+
			previewScript(root, port, true, ""))
	}

	transport, release, err := server.Engine.Editors.PortTransport(project, sessionID, port)
	if err != nil {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	defer release()
	defer transport.CloseIdleConnections()

	secure := server.SecureCookie || request.TLS != nil || strings.HasPrefix(server.Origin, "https://")
	previewProxy(transport, root, port, secure).ServeHTTP(requestContext.Response(), request)
	return nil
}

func querySuffix(address *url.URL) string {
	if address.RawQuery != "" {
		return "?" + address.RawQuery
	}
	return ""
}

func previewScript(root string, port int, boot bool, nonce string) string {
	return `<script src="` + html.EscapeString(root+"_zentrale/runtime.js") + `" data-root="` +
		html.EscapeString(root) + `" data-cookies="` + previewCookiePrefix(root) +
		`" data-port="` + strconv.Itoa(port) + `" data-boot="` + strconv.FormatBool(boot) + `" nonce="` + nonce + `"></script>`
}

func previewProxy(transport http.RoundTripper, root string, port int, secure bool) *httputil.ReverseProxy {
	prefix := root + strconv.Itoa(port) + "/"
	target := &url.URL{Scheme: "http", Host: "localhost:" + strconv.Itoa(port)}
	proxy := &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			incoming, outgoing := proxyRequest.In, proxyRequest.Out
			proxyRequest.SetURL(target)
			outgoing.Host = target.Host
			outgoing.URL.Path = "/" + strings.TrimPrefix(incoming.URL.Path, prefix)
			outgoing.URL.RawPath = "/" + strings.TrimPrefix(incoming.URL.EscapedPath(), prefix)
			omitCookies := incoming.Header.Get("X-Zentrale-Preview-Credentials") == "omit"
			outgoing.Header.Del("X-Zentrale-Preview-Credentials")
			outgoing = outgoing.WithContext(context.WithValue(outgoing.Context(), previewCredentialsKey{}, omitCookies))
			proxyRequest.Out = outgoing
			forwardPreviewCookies(incoming, outgoing, root, outgoing.URL.EscapedPath(), omitCookies)
			outgoing.Header.Del("X-Zentrale-Preview")
			originPort := incoming.Header.Get("X-Zentrale-Preview-Origin-Port")
			outgoing.Header.Del("X-Zentrale-Preview-Origin-Port")
			if strings.EqualFold(incoming.Header.Get("Upgrade"), "websocket") {
				query := outgoing.URL.Query()
				originPort = query.Get("__zentrale_preview_origin")
				query.Del("__zentrale_preview_origin")
				outgoing.URL.RawQuery = query.Encode()
			}
			outgoing.Header.Set("Accept-Encoding", "identity")
			outgoing.Header.Del("If-None-Match")
			outgoing.Header.Del("If-Modified-Since")
			proxyRequest.SetXForwarded()
			outgoing.Header.Set("X-Forwarded-Prefix", strings.TrimSuffix(prefix, "/"))
			if secure {
				outgoing.Header.Set("X-Forwarded-Proto", "https")
			}
			if referer, err := url.Parse(outgoing.Header.Get("Referer")); err == nil && strings.HasPrefix(referer.Path, root) {
				portPath := strings.TrimPrefix(referer.Path, root)
				refererPort, path, _ := strings.Cut(portPath, "/")
				if number, err := strconv.Atoi(refererPort); err == nil && number >= 1 && number <= 65535 {
					if originPort == "" {
						originPort = refererPort
					}
					referer.Scheme, referer.Host = target.Scheme, "localhost:"+refererPort
					referer.Path, referer.RawPath = "/"+path, ""
					outgoing.Header.Set("Referer", referer.String())
				} else {
					outgoing.Header.Del("Referer")
				}
			}
			origin := target.String()
			if number, err := strconv.Atoi(originPort); err == nil && number >= 1 && number <= 65535 {
				origin = "http://localhost:" + strconv.Itoa(number)
			}
			if outgoing.Header.Get("Origin") != "" {
				outgoing.Header.Set("Origin", origin)
			}
		},
		ModifyResponse: func(response *http.Response) error {
			response.Header.Set("Cache-Control", "no-store")
			response.Header.Del("Service-Worker-Allowed")
			response.Header.Del("Clear-Site-Data")
			response.Header.Del("Alt-Svc")
			if location := response.Header.Get("Location"); location != "" {
				response.Header.Set("Location", previewURL(location, root, port))
			}
			cookies := response.Cookies()
			response.Header.Del("Set-Cookie")
			omitCookies, _ := response.Request.Context().Value(previewCredentialsKey{}).(bool)
			for _, cookie := range cookies {
				if omitCookies {
					continue
				}
				scopePreviewCookie(cookie, root, response.Request.URL.EscapedPath(), secure)
				response.Header.Add("Set-Cookie", cookie.String())
			}
			if response.StatusCode == http.StatusSwitchingProtocols || response.Request.Method == http.MethodHead ||
				!strings.Contains(response.Header.Get("Content-Type"), "text/html") {
				return nil
			}
			return rewritePreviewHTML(response, root, port)
		},
		ErrorHandler: func(writer http.ResponseWriter, _ *http.Request, _ error) {
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprintf(writer, "Preview port %d is unavailable. Start the HTTP dev server in this VS Code terminal and retry.", port)
		},
	}
	return proxy
}

func previewURL(value, root string, port int) string {
	address, err := url.Parse(value)
	if err != nil {
		return value
	}
	if address.IsAbs() || address.Host != "" {
		host := strings.ToLower(address.Hostname())
		if host != "localhost" && host != "127.0.0.1" && host != "[::1]" && host != "::1" {
			return value
		}
		if address.Scheme != "http" && address.Scheme != "https" && address.Scheme != "" {
			return value
		}
		if number, err := strconv.Atoi(address.Port()); err == nil && number >= 1 && number <= 65535 {
			port = number
		} else if address.Scheme == "https" {
			port = 443
		} else {
			port = 80
		}
		address.Scheme, address.Host = "", ""
		address.User = nil
		if address.Path == "" {
			address.Path = "/"
		}
	}
	if !strings.HasPrefix(address.Path, "/") || strings.HasPrefix(address.Path, root) {
		return value
	}
	escaped := address.EscapedPath()
	address.Path = root + strconv.Itoa(port) + address.Path
	address.RawPath = root + strconv.Itoa(port) + escaped
	return address.String()
}

func rewritePreviewHTML(response *http.Response, root string, port int) error {
	defer response.Body.Close()
	var reader io.Reader = response.Body
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		if encoding != "gzip" {
			return fmt.Errorf("unsupported preview HTML encoding: %s", encoding)
		}
		compressed, err := gzip.NewReader(reader)
		if err != nil {
			return err
		}
		defer compressed.Close()
		reader = compressed
	}
	data, err := io.ReadAll(io.LimitReader(reader, 8*1024*1024+1))
	if err != nil || len(data) > 8*1024*1024 {
		return fmt.Errorf("preview HTML exceeds the size limit or cannot be read")
	}
	var output bytes.Buffer
	nonce := rand.Text()
	tokens := xhtml.NewTokenizer(bytes.NewReader(data))
	injected := false
	for {
		kind := tokens.Next()
		if kind == xhtml.ErrorToken {
			if tokens.Err() != io.EOF {
				return tokens.Err()
			}
			break
		}
		if kind == xhtml.StartTagToken || kind == xhtml.SelfClosingTagToken {
			token := tokens.Token()
			if !injected && (token.Data == "script" || token.Data == "body") {
				output.WriteString(previewScript(root, port, false, nonce))
				injected = true
			}
			for index := range token.Attr {
				attribute := &token.Attr[index]
				switch attribute.Key {
				case "src", "href", "action", "poster", "data":
					attribute.Val = previewURL(attribute.Val, root, port)
				}
			}
			output.WriteString(token.String())
			if !injected && token.Data == "head" {
				output.WriteString(previewScript(root, port, false, nonce))
				injected = true
			}
		} else {
			output.Write(tokens.Raw())
		}
	}
	if !injected {
		output.WriteString(previewScript(root, port, false, nonce))
	}
	response.Body = io.NopCloser(bytes.NewReader(output.Bytes()))
	response.ContentLength = int64(output.Len())
	response.Header.Set("Content-Length", strconv.Itoa(output.Len()))
	response.Header.Del("Content-Encoding")
	response.Header.Del("ETag")
	// Development previews need the bootstrap ahead of the app. Applications
	// retain their other policies; only script loading is extended to this origin.
	for _, header := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		policies := response.Header.Values(header)
		response.Header.Del(header)
		for _, policy := range policies {
			response.Header.Add(header, previewScriptPolicy(policy, nonce))
		}
	}
	return nil
}

func previewScriptPolicy(policy, nonce string) string {
	parts := strings.Split(policy, ";")
	found := false
	var defaults []string
	for index, part := range parts {
		fields := strings.Fields(part)
		if len(fields) > 0 && fields[0] == "default-src" {
			defaults = fields[1:]
		}
		if len(fields) > 0 && (fields[0] == "script-src" || fields[0] == "script-src-elem") {
			parts[index] = strings.TrimSpace(strings.ReplaceAll(part, "'none'", "")) + " 'nonce-" + nonce + "'"
			found = true
		}
	}
	if !found && defaults != nil {
		parts = append(parts, "script-src "+strings.ReplaceAll(strings.Join(defaults, " "), "'none'", "")+" 'nonce-"+nonce+"'")
	}
	return strings.Join(parts, ";")
}
