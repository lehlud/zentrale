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
	"github.com/lehlud/zentrale/auth/oidc"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/vscode"
	"golang.org/x/crypto/ssh"
)

type Server struct {
	Store  *datastore.Instance
	Auth   *auth.Service
	OIDC   *oidc.Client
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
	if server.OIDC != nil {
		server.registerOIDC(e)
	}

	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(requestContext echo.Context) error {
			requestContext.Response().Header().Set("X-Content-Type-Options", "nosniff")
			requestContext.Response().Header().Set("Referrer-Policy", "same-origin")
			requestContext.Response().Header().Set("X-Frame-Options", "SAMEORIGIN")

			if strings.HasSuffix(requestContext.Request().URL.Path, "/oauth/callback") {
				requestContext.Response().Header().Set("Cache-Control", "no-store")
				requestContext.Response().Header().Set("Referrer-Policy", "no-referrer")
			}

			if strings.HasPrefix(requestContext.Request().URL.Path, "/graphql") ||
				strings.HasPrefix(requestContext.Request().URL.Path, "/events") ||
				strings.HasPrefix(requestContext.Request().URL.Path, "/terminal/") ||
				strings.HasPrefix(requestContext.Request().URL.Path, "/side/") ||
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

		requestToken := server.token(requestContext.Request())
		user, valid := server.Auth.User(requestToken)
		if err := ValidateQuery(request.Query, request.OperationName, valid); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, errAuthenticationRequired) {
				status = http.StatusUnauthorized
			}
			return requestContext.JSON(status, graphQLErrorResult(err))
		}

		secure := server.SecureCookie || requestContext.Request().TLS != nil

		origin := server.Origin
		if origin == "" {
			scheme := "http"
			if secure {
				scheme = "https"
			}

			origin = scheme + "://" + requestContext.Request().Host
		}

		session := &requestState{
			authenticated: valid,
			user:          user,
			sessionHash:   auth.TokenHash(requestToken),
			origin:        origin,
			client:        clientAddress(requestContext.Request()),
		}
		session.login = func(username, password string) (bool, error) {
			token, err := server.Auth.LoginAccount(username, password, session.client)
			if err != nil {
				return false, err
			}

			server.setSessionCookie(requestContext, token)
			session.user, session.authenticated = server.Auth.User(token)
			session.sessionHash = auth.TokenHash(token)

			return session.authenticated, nil
		}

		session.logout = func() error {
			err := server.Store.DeleteUserSession(session.sessionHash)
			server.setSessionCookie(requestContext, "")
			session.authenticated = false
			session.user = domain.User{}
			session.sessionHash = ""

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
		if (valid || session.authenticated) && isMutationOperation(request.Query, request.OperationName) {
			// Cover key/configuration resolvers that do not otherwise emit engine
			// events. Never include query/variables: they can contain credentials.
			// Partial mutation errors may still follow committed earlier fields.
			server.Engine.Emit("resource_changed", "", nil)
		}

		return requestContext.JSON(200, result)
	})

	e.GET("/events", func(requestContext echo.Context) error { return server.events(requestContext, schema) })
	e.POST("/attachments/:agent/:id", server.attachment, server.projectURL("agent", true, true))
	e.GET("/attachments/:agent/:id", server.attachment, server.projectURL("agent", true, false))
	e.POST("/short-chat-knowledge", server.uploadShortChatDocument)
	e.GET("/short-chat-knowledge/:id", server.downloadShortChatDocument)
	e.GET("/transcript-export/:agent", server.transcriptExport)
	e.GET("/workspace-file/:project", server.workspaceFile, server.projectURL("project", false, true))
	e.GET("/office/*", server.officeAsset)
	e.GET("/terminal/:project", server.terminal)
	e.GET("/side/:agent", server.side)
	e.HEAD("/side/:agent", server.sideStatus)
	e.POST("/terminal/:project", server.terminal)
	e.POST("/mcp", server.mcp)
	e.Any("/ide/:project/:session/*", server.ide, server.projectURL("project", false, true))
	e.Any("/preview/:project/:session/:port/*", server.preview, server.projectURL("project", false, true))
	e.Any("/preview/:project/:session/:port", server.preview, server.projectURL("project", false, true))

	e.GET("/*", func(requestContext echo.Context) error {
		requestPath := requestContext.Request().URL.Path
		viewer := strings.HasPrefix(requestPath, "/pdfjs/")
		if viewer {
			requestContext.Response().Header().Set("Content-Security-Policy", "connect-src 'self' blob: data:; frame-ancestors 'self'")
		}
		root, err := filepath.Abs(server.Frontend)
		if err != nil {
			return err
		}

		params := filepath.Join(root, filepath.Clean("/"+requestPath))
		if params != root && !strings.HasPrefix(params, root+string(filepath.Separator)) {
			return echo.ErrNotFound
		}

		pwaEntry := requestPath == "/manifest.webmanifest" || requestPath == "/pwa-sw.js"
		if stat, err := os.Stat(params); err == nil && !stat.IsDir() {
			headers := requestContext.Response().Header()
			if pwaEntry {
				headers.Set("Cache-Control", "no-cache")
			}

			switch requestPath {
			case "/manifest.webmanifest":
				headers.Set("Content-Type", "application/manifest+json")
			case "/pwa-sw.js":
				headers.Set("Content-Type", "text/javascript; charset=utf-8")
			}

			return requestContext.File(params)
		}

		if viewer || pwaEntry || strings.HasPrefix(requestPath, "/assets/") || strings.HasPrefix(requestPath, "/icons/") {
			return echo.ErrNotFound
		}

		if server.OIDC != nil && !server.Auth.Valid(server.token(requestContext.Request())) {
			return server.startOIDC(requestContext)
		}

		requestContext.Response().Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:; img-src 'self' data: blob:; frame-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'",
		)

		return requestContext.File(filepath.Join(root, "index.html"))
	})

	return e, nil
}

func (server *Server) events(requestContext echo.Context, schema graphql.Schema) error {
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
	defer func() {
		if !server.Auth.Valid(token) {
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(sessionExpired, errAuthenticationRequired.Error()),
				time.Now().Add(10*time.Second))
		}
	}()

	ctx, cancel := context.WithCancel(requestContext.Request().Context())
	defer cancel()
	client := clientAddress(requestContext.Request())
	conn.SetReadLimit(socketQueryReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})
	commands := make(chan socketQueryRequest, socketQueryLimit)
	pongs := make(chan string, 4)
	conn.SetPingHandler(func(data string) error {
		select {
		case pongs <- data:
			return nil
		default:
			return errors.New("event control queue full")
		}
	})
	conn.SetCloseHandler(func(code int, text string) error { return &websocket.CloseError{Code: code, Text: text} })
	go func() {
		defer cancel()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil || kind != websocket.TextMessage {
				return
			}
			var command socketQueryRequest
			if err := json.Unmarshal(data, &command); err != nil {
				return
			}
			select {
			case commands <- command:
			case <-ctx.Done():
				return
			default:
				return // Bounded input queue: a flooding client is closed.
			}
		}
	}()
	events, unsubscribe := server.Engine.Subscribe()
	defer unsubscribe()

	after, _ := strconv.ParseInt(requestContext.QueryParam("after"), 10, 64)
	if requestContext.QueryParam("after") == "latest" {
		// A fresh page reads current state through GraphQL, not the entire
		// durable event history. Subscribe first so concurrent changes remain
		// queued, and send a watermark to close the query/subscribe race.
		after, err = server.Store.LatestEventID()
		if err != nil {
			return err
		}
		if ctx.Err() != nil || !server.Auth.Valid(token) {
			return nil
		}
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteJSON(domain.Event{ID: after, Type: "sync"}); err != nil {
			return nil
		}
	}
	send := func(event domain.Event) error {
		user, authenticated := server.Auth.User(token)
		if ctx.Err() != nil || !authenticated {
			return errors.New("event session expired")
		}
		if event.ID != 0 && event.ID <= after {
			return nil
		}
		if event.ID != 0 {
			after = event.ID
		}
		if event.AgentID != "" && server.agentAccess(user, event.AgentID, false) != nil {
			return nil
		}
		if event.AgentID == "" {
			switch event.Type {
			case "resource_changed", "resources", "editor", "environment_reset":
				// These are invalidations, not authorization to read a resource.
				event.Data = ""
			default:
				if !user.IsAdmin() {
					return nil
				}
			}
		}

		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

		if err := conn.WriteJSON(event); err != nil {
			return err
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
	queries := newSocketQueries()
	results := make(chan socketQueryResult, socketQueryConcurrency)
	executions := map[uint64]*socketQueryExecution{}
	works := map[uint64]socketQueryWork{}
	timeout := time.NewTimer(time.Hour)
	defer timeout.Stop()
	defer func() {
		for _, execution := range executions {
			execution.stop()
		}
	}()
	writeSnapshot := func(response socketQueryResponse) error {
		if ctx.Err() != nil || !server.Auth.Valid(token) {
			return errors.New("event session expired")
		}
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(response)
	}

	for {
		for work := queries.schedule(); work != nil; work = queries.schedule() {
			if !server.Auth.Valid(token) {
				return nil
			}
			execution := startSocketQueryExecution(ctx)
			executions[work.generation] = execution
			works[work.generation] = *work
			task := socketQueryTask{ctx: execution.ctx, work: *work, results: results,
				execute: func(ctx context.Context, request socketQueryRequest) any {
					return server.executeSocketQuery(ctx, schema, token, client, request)
				}}
			if !eventSocketQueryPool().submit(task) {
				execution.stop()
				delete(executions, work.generation)
				delete(works, work.generation)
				queries.fail(*work)
				if err := writeSnapshot(socketQueryError(work.request.RequestID, "snapshot executor saturated; resubscribe to retry")); err != nil {
					return nil
				}
			}
		}
		if !timeout.Stop() {
			select {
			case <-timeout.C:
			default:
			}
		}
		wait := time.Hour
		for _, execution := range executions {
			deadline, _ := execution.ctx.Deadline()
			wait = min(wait, max(time.Nanosecond, time.Until(deadline)))
		}
		timeout.Reset(wait)
		select {
		case <-ctx.Done():
			return nil
		case <-timeout.C:
			for generation, execution := range executions {
				if execution.ctx.Err() == nil {
					continue
				}
				work := works[generation]
				execution.stop()
				delete(executions, generation)
				delete(works, generation)
				queries.fail(work)
				if err := writeSnapshot(socketQueryError(work.request.RequestID, "snapshot timed out; resubscribe to retry")); err != nil {
					return nil
				}
			}
		case command := <-commands:
			if !server.Auth.Valid(token) || len(command.RequestID) == 0 || len(command.RequestID) > 128 {
				return nil
			}
			if command.Type == "unsubscribe" {
				if subscription := queries.subscriptions[command.RequestID]; subscription != nil {
					generation := subscription.generation
					executions[generation].stop()
					delete(executions, generation)
					delete(works, generation)
				}
			}
			if failure := queries.command(command); failure != nil {
				if err := writeSnapshot(*failure); err != nil {
					return nil
				}
			}
		case result := <-results:
			execution := executions[result.work.generation]
			if execution == nil {
				continue
			}
			expired := execution.ctx.Err() != nil
			execution.stop()
			delete(executions, result.work.generation)
			delete(works, result.work.generation)
			if expired {
				queries.fail(result.work)
				if err := writeSnapshot(socketQueryError(result.work.request.RequestID, "snapshot timed out; resubscribe to retry")); err != nil {
					return nil
				}
				continue
			}
			if response := queries.receive(result); response != nil {
				if err := writeSnapshot(*response); err != nil {
					return nil
				}
			}
		case data := <-pongs:
			if err := conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(10*time.Second)); err != nil {
				return nil
			}
		case event, ok := <-events:
			if !ok {
				return nil
			}

			if err := send(event); err != nil {
				return nil
			}
			if event.ID != 0 {
				queries.invalidate()
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
	user, ok := server.Auth.User(server.token(requestContext.Request()))
	if !ok {
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
		if rpcErr = server.agentAccess(user, id, true); rpcErr != nil {
			break
		}
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
