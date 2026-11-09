package webapi

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
	"github.com/lehlud/zentrale/vscode"
)

func (server *Server) ide(requestContext echo.Context) error {
	if !server.Auth.Valid(server.token(requestContext.Request())) {
		return echo.ErrUnauthorized
	}
	if !server.sameOrigin(requestContext.Request()) {
		return echo.ErrForbidden
	}
	var project domain.Project
	if err := server.Store.Load("project", requestContext.Param("project"), &project); err != nil {
		return echo.ErrNotFound
	}
	session, err := server.Engine.Editors.Info(project, requestContext.Param("session"))
	launchURL := "/#/editor/" + url.PathEscape(project.ID)
	agentID := session.AgentID
	if agentID == "" {
		agentID = requestContext.Request().URL.Query().Get("agent")
	}
	if agentID != "" {
		launchURL += "?agent=" + url.QueryEscape(agentID)
	}
	if err != nil {
		if strings.Contains(requestContext.Request().Header.Get("Accept"), "text/html") {
			return requestContext.Redirect(http.StatusSeeOther, launchURL)
		}
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	prefix := "/ide/" + project.ID + "/" + session.ID
	request := requestContext.Request()
	if request.URL.Path == prefix+"/" && request.Header.Get("Upgrade") == "" && server.Engine.Editors.CurrentFor(project, session.AgentID).ID != session.ID {
		return requestContext.Redirect(http.StatusSeeOther, launchURL)
	}
	authority, machineName := session.MachineID, session.MachineName
	if session.Sandbox == remote.SandboxMicroVM {
		authority += ":" + project.ID + ":" + session.AgentID
		machineName += " · Fedora MicroVM"
	}
	if server.Workbench.Serve(requestContext.Response(), request, prefix, session.Directory, authority, machineName) {
		return nil
	}
	endpoint := strings.TrimPrefix(request.URL.Path, prefix)
	endpoint = strings.TrimPrefix(endpoint, "/stable-"+vscode.CurrentRelease().Commit)
	websocket := strings.EqualFold(request.Header.Get("Upgrade"), "websocket")
	if !((websocket && (endpoint == "" || endpoint == "/")) || endpoint == "/vscode-remote-resource" || endpoint == "/delay-shutdown" || endpoint == "/version") {
		return echo.ErrNotFound
	}
	transport, release, err := server.Engine.Editors.Transport(project, session.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	defer release()
	defer transport.CloseIdleConnections()
	proxy := editorProxy(transport, prefix, server.SecureCookie || requestContext.Request().TLS != nil)
	proxy.ServeHTTP(requestContext.Response(), requestContext.Request())
	return nil
}

func editorProxy(transport http.RoundTripper, prefix string, secure bool) *httputil.ReverseProxy {
	target := &url.URL{Scheme: "http", Host: "vscode"}
	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(request *http.Request) {
		original(request)
		// The headless agent knows this base path. Only its workspace protocol and
		// remote resources arrive here; browser assets are served locally above.
		request.Header.Del("Authorization")
		cookies := request.Cookies()
		request.Header.Del("Cookie")
		for _, cookie := range cookies {
			if strings.HasPrefix(cookie.Name, "vscode-") {
				request.AddCookie(cookie)
			}
		}
		request.Header.Set("X-Forwarded-Host", request.Host)
		request.Header.Set("X-Forwarded-Prefix", prefix)
		if secure {
			request.Header.Set("X-Forwarded-Proto", "https")
		} else {
			request.Header.Set("X-Forwarded-Proto", "http")
		}
	}
	proxy.Transport = transport
	proxy.ModifyResponse = func(response *http.Response) error {
		cookies := response.Cookies()
		response.Header.Del("Set-Cookie")
		for _, cookie := range cookies {
			if !strings.HasPrefix(cookie.Name, "vscode-") {
				continue
			}
			cookie.Path, cookie.Domain, cookie.Secure = prefix+"/", "", secure
			response.Header.Add("Set-Cookie", cookie.String())
		}
		return nil
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(writer).Encode(map[string]string{"message": "remote editor disconnected; reconnect the editor"})
	}
	return proxy
}
