package webapi

import (
	"context"
	"errors"
	"mime"
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
		if r.URL.Query().Get("download") == "1" {
			return server.downloadDeliveredAttachment(requestContext)
		}

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
	// Interrupt a stalled raw body read even if the client stops sending data.
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

func (server *Server) downloadDeliveredAttachment(requestContext echo.Context) error {
	metadata, data, err := server.Engine.DeliveredAttachment(requestContext.Param("agent"), requestContext.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "delivered file not found")
	}

	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": metadata.Name})
	if disposition == "" {
		disposition = "attachment"
	}
	headers := requestContext.Response().Header()
	headers.Set("Content-Disposition", disposition)
	headers.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	return requestContext.Blob(http.StatusOK, "application/octet-stream", data)
}
