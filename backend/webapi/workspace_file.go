package webapi

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/agent"
)

func (server *Server) workspaceFile(c echo.Context) error {
	r := c.Request()
	response := c.Response()
	response.Header().Set("Cache-Control", "private, no-store")
	if !server.sameOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return echo.ErrForbidden
	}
	if !server.Auth.Valid(server.token(r)) {
		return echo.ErrUnauthorized
	}
	query := r.URL.Query()
	// Leave room for the size header in the daemon's 64 MiB output budget.
	limit := int64((64 << 20) - 4096)
	switch query.Get("preview") {
	case "text":
		limit = 2 << 20
	case "pdf":
		limit = 32 << 20
	case "office", "image":
		limit = 25 << 20
	}
	err := server.Engine.ReadWorkspaceFile(r.Context(), c.Param("project"), query.Get("agent"), query.Get("path"), limit, func(name string, size int64, source io.Reader) error {
		response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		response.Header().Set("Content-Type", "application/octet-stream")
		response.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		response.WriteHeader(http.StatusOK)
		_, err := io.CopyN(response, source, size)
		return err
	})
	switch {
	case err == nil || r.Context().Err() != nil:
		return nil
	case errors.Is(err, agent.ErrWorkspaceFileNotFound):
		return echo.ErrNotFound
	case errors.Is(err, agent.ErrWorkspaceFileTooLarge):
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge)
	default:
		slog.Warn("workspace file transfer failed", "project", c.Param("project"), "error", err)
		if response.Committed {
			return nil
		}
		return echo.NewHTTPError(http.StatusBadGateway, "workspace file unavailable")
	}
}
