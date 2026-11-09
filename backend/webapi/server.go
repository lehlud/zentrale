package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/graphql-go/graphql"
	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/auth"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/vscode"
	"golang.org/x/crypto/ssh"
)

type Server struct {
	Store  *datastore.Instance
	Auth   *auth.Service
	Engine *agent.Engine

	Frontend     string
	Origin       string
	SecureCookie bool
	Workbench    *vscode.Workbench
}

func hostPublic(raw []byte) string {
	key, err := ssh.ParsePublicKey(raw)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

func (server *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	if server.Origin != "" {
		return origin == server.Origin
	}

	return (u.Scheme == "https" || u.Scheme == "http") && u.Host == r.Host
}

func (server *Server) token(r *http.Request) string {
	cookie, err := r.Cookie("zentrale_session")
	if err != nil {
		return ""
	}

	return cookie.Value
}

func (server *Server) Handler() (*echo.Echo, error) {
	if server.Workbench == nil {
		server.Workbench = vscode.NewWorkbench("")
	}

	schema, err := server.Schema()
	if err != nil {
		return nil, err
	}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(requestContext echo.Context) error {
			requestContext.Response().Header().Set("X-Content-Type-Options", "nosniff")
			requestContext.Response().Header().Set("Referrer-Policy", "same-origin")
			requestContext.Response().Header().Set("X-Frame-Options", "SAMEORIGIN")

			if strings.HasPrefix(requestContext.Request().URL.Path, "/graphql") ||
				strings.HasPrefix(requestContext.Request().URL.Path, "/events") ||
				strings.HasPrefix(requestContext.Request().URL.Path, "/mcp") {
				requestContext.Response().Header().Set("Cache-Control", "no-store")
			}

			return next(requestContext)
		}
	})

	e.POST("/graphql", func(requestContext echo.Context) error {
		if !server.sameOrigin(requestContext.Request()) {
			return echo.NewHTTPError(403, "origin rejected")
		}

		if !strings.HasPrefix(requestContext.Request().Header.Get("Content-Type"), "application/json") {
			return echo.NewHTTPError(415, "application/json required")
		}

		var request struct {
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
			OperationName string         `json:"operationName"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(requestContext.Response(), requestContext.Request().Body, 128*1024)).Decode(&request); err != nil {
			return echo.NewHTTPError(400, "invalid GraphQL request")
		}

		valid := server.Auth.Valid(server.token(requestContext.Request()))
		if err := ValidateQuery(request.Query, request.OperationName, valid); err != nil {
			return requestContext.JSON(400, map[string]any{
				"errors": []any{
					map[string]string{
						"message": err.Error(),
					},
				},
			})
		}

		secure := server.SecureCookie || requestContext.Request().TLS != nil
		setCookie := func(value string, maxAge int) {
			http.SetCookie(requestContext.Response(), &http.Cookie{
				Name:     "zentrale_session",
				Value:    value,
				Path:     "/",
				HttpOnly: true,
				Secure:   secure,
				SameSite: http.SameSiteStrictMode,
				MaxAge:   maxAge,
			})
		}

		session := &requestState{authenticated: valid, client: clientAddress(requestContext.Request())}
		session.login = func(password string) (bool, error) {
			token, err := server.Auth.Login(password, clientAddress(requestContext.Request()))
			if err != nil {
				return false, err
			}

			setCookie(token, 86400)
			session.authenticated = true

			return true, nil
		}

		session.logout = func() error {
			err := server.Auth.Logout(server.token(requestContext.Request()))
			setCookie("", -1)
			session.authenticated = false

			return err
		}

		ctx := context.WithValue(requestContext.Request().Context(), requestKey{}, session)
		result := graphql.Do(graphql.Params{
			Schema:         schema,
			RequestString:  request.Query,
			VariableValues: request.Variables,
			OperationName:  request.OperationName,
			Context:        ctx,
		})

		return requestContext.JSON(200, result)
	})

	e.GET("/events", server.events)
	e.POST("/mcp", server.mcp)
	e.Any("/ide/:project/:session/*", server.ide)
	e.Any("/preview/:project/:session/:port/*", server.preview)
	e.Any("/preview/:project/:session/:port", server.preview)

	e.GET("/*", func(requestContext echo.Context) error {
		root, err := filepath.Abs(server.Frontend)
		if err != nil {
			return err
		}

		params := filepath.Join(root, filepath.Clean("/"+requestContext.Request().URL.Path))
		if params != root && !strings.HasPrefix(params, root+string(filepath.Separator)) {
			return echo.ErrNotFound
		}

		if stat, err := os.Stat(params); err == nil && !stat.IsDir() {
			return requestContext.File(params)
		}

		if strings.HasPrefix(requestContext.Request().URL.Path, "/assets/") {
			return echo.ErrNotFound
		}

		requestContext.Response().Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:; img-src 'self' data:; frame-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'",
		)

		return requestContext.File(filepath.Join(root, "index.html"))
	})

	return e, nil
}

func (server *Server) events(requestContext echo.Context) error {
	token := server.token(requestContext.Request())
	if !server.Auth.Valid(token) {
		return echo.ErrUnauthorized
	}

	if !server.sameOrigin(requestContext.Request()) {
		return echo.ErrForbidden
	}

	upgrader := websocket.Upgrader{
		CheckOrigin:     server.sameOrigin,
		ReadBufferSize:  1024,
		WriteBufferSize: 4096,
	}

	conn, err := upgrader.Upgrade(requestContext.Response(), requestContext.Request(), nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})
	done := make(chan struct{})
	go func() {
		defer close(done)

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	events, unsubscribe := server.Engine.Subscribe()
	defer unsubscribe()

	after, _ := strconv.ParseInt(requestContext.QueryParam("after"), 10, 64)
	send := func(event domain.Event) error {
		if event.ID != 0 && event.ID <= after {
			return nil
		}

		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

		if err := conn.WriteJSON(event); err != nil {
			return err
		}

		if event.ID != 0 {
			after = event.ID
		}

		return nil
	}

	for {
		past, err := server.Store.Events(after, 500)
		if err != nil {
			return err
		}

		for _, event := range past {
			if err := send(event); err != nil {
				return nil
			}
		}

		if len(past) < 500 {
			break
		}
	}

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return nil
		case event, ok := <-events:
			if !ok {
				return nil
			}

			if err := send(event); err != nil {
				return nil
			}
		case <-ticker.C:
			if !server.Auth.Valid(token) {
				return nil
			}

			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
				return nil
			}
		case <-requestContext.Request().Context().Done():
			return nil
		}
	}
}

func (server *Server) mcp(requestContext echo.Context) error {
	if !server.Auth.Valid(server.token(requestContext.Request())) {
		return echo.ErrUnauthorized
	}

	if !server.sameOrigin(requestContext.Request()) {
		return echo.ErrForbidden
	}

	var req struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      any            `json:"id"`
		Method  string         `json:"method"`
		Params  map[string]any `json:"params"`
	}
	if err := json.NewDecoder(io.LimitReader(requestContext.Request().Body, 128*1024)).Decode(&req); err != nil {
		return echo.ErrBadRequest
	}

	var result any
	var rpcErr error

	switch req.Method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "zentrale-inspector", "version": "1.0.0"},
		}
	case "notifications/initialized":
		return requestContext.NoContent(202)
	case "tools/list":
		result = map[string]any{
			"tools": []any{
				map[string]any{
					"name":        "inspect",
					"description": "Read-only project files, instructions, histories and grants",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"agentId":   map[string]string{"type": "string"},
							"action":    map[string]string{"type": "string"},
							"arguments": map[string]string{"type": "object"},
						},
						"required": []string{"agentId", "action"},
					},
				},
			},
		}
	case "tools/call":
		name, _ := req.Params["name"].(string)
		args, _ := req.Params["arguments"].(map[string]any)

		if name != "inspect" {
			rpcErr = errors.New("read-only inspect tool expected")
			break
		}

		id, _ := args["agentId"].(string)
		action, _ := args["action"].(string)
		input, _ := args["arguments"].(map[string]any)
		var a domain.Agent
		if rpcErr = server.Store.Load("agent", id, &a); rpcErr == nil {
			var value any
			value, rpcErr = server.Engine.Inspect(requestContext.Request().Context(), a, action, input)
			result = map[string]any{
				"content": []any{
					map[string]string{
						"type": "text",
						"text": agent.JSON(value),
					},
				},
			}
		}
	default:
		rpcErr = errors.New("method not found")
	}

	if rpcErr != nil {
		return requestContext.JSON(
			200,
			map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]any{
					"code":    -32602,
					"message": rpcErr.Error(),
				},
			},
		)
	}

	return requestContext.JSON(200, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

// Use the actual peer for login throttling. Forwarded headers are untrusted
// unless a deployment explicitly implements a trusted reverse-proxy boundary.
func clientAddress(request *http.Request) string {
	address, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}

	return address
}
