package webapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/graphql-go/graphql"
	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/agent"
)

func inputAttachmentArgs() graphql.FieldConfigArgument {
	args := graphql.FieldConfigArgument{}
	for _, name := range []string{"agentId", "id", "text", "mode"} {
		args[name] = &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}
	}
	args["attachmentIds"] = &graphql.ArgumentConfig{Type: graphql.NewList(graphql.NewNonNull(graphql.String))}
	return args
}

func (server *Server) attachment(requestContext echo.Context) error {
	r := requestContext.Request()
	requestContext.Response().Header().Set("Cache-Control", "private, no-store")
	requestContext.Response().Header().Set("X-Content-Type-Options", "nosniff")
	if !server.sameOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return echo.NewHTTPError(http.StatusForbidden, "origin rejected")
	}
	if !server.Auth.Valid(server.token(r)) {
		return echo.NewHTTPError(http.StatusUnauthorized, "authentication required")
	}
	if _, err := server.Engine.UserAttachmentTarget(requestContext.Param("agent")); err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "attachment target rejected")
	}
	if r.Method == http.MethodGet {
		metadata, data, err := server.Engine.AttachmentImage(requestContext.Param("agent"), requestContext.Param("id"))
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "image not found")
		}
		return requestContext.Blob(http.StatusOK, metadata.MimeType, data)
	}
	if r.ContentLength > agent.MaxAttachmentBytes {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "attachment exceeds 25 MiB")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	// Deadlines interrupt a stalled raw body read, not merely the SSH phase.
	controller := http.NewResponseController(requestContext.Response().Writer)
	_ = controller.SetReadDeadline(time.Now().Add(10 * time.Minute))
	defer controller.SetReadDeadline(time.Time{})
	body := http.MaxBytesReader(requestContext.Response(), r.Body, agent.MaxAttachmentBytes)
	defer body.Close()
	stopRead := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stopRead()
	metadata, err := server.Engine.UploadAttachment(ctx, requestContext.Param("agent"), requestContext.Param("id"), r.URL.Query().Get("name"), r.Header.Get("Content-Type"), body)
	if err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "attachment exceeds 25 MiB")
		}
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return requestContext.JSON(http.StatusOK, metadata)
}
