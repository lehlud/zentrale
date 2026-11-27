package webapi

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"reflect"
	"time"

	"github.com/graphql-go/graphql"
	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/datastore"
)

func (builder *schemaBuilder) shortChatKnowledgeFields(queries, mutations graphql.Fields) {
	server := builder.server
	queries["shortChatSystemPrompt"] = &graphql.Field{
		Type: graphql.String,
		Resolve: adminOnly(func(_ graphql.ResolveParams) (any, error) {
			return server.Engine.ShortChatSystemPrompt()
		}),
	}
	mutations["saveShortChatSystemPrompt"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: gitStringArgs("prompt"),
		Resolve: adminOnly(func(params graphql.ResolveParams) (any, error) {
			err := server.Engine.SaveShortChatSystemPrompt(params.Args["prompt"].(string))
			return err == nil, err
		}),
	}
	queries["shortChatDocuments"] = &graphql.Field{
		Type: graphql.NewList(builder.output(reflect.TypeFor[datastore.ShortChatDocument]())),
		Args: graphql.FieldConfigArgument{
			"limit":  {Type: graphql.Int, DefaultValue: 25},
			"offset": {Type: graphql.Int, DefaultValue: 0},
		},
		Resolve: adminOnly(func(params graphql.ResolveParams) (any, error) {
			docs, err := server.Store.ShortChatDocuments(params.Context, params.Args["limit"].(int), params.Args["offset"].(int))
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(docs))
			for _, doc := range docs {
				out = append(out, mapOf(doc))
			}
			return out, nil
		}),
	}
	mutations["deleteShortChatDocument"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: gitStringArgs("id"),
		Resolve: adminOnly(func(params graphql.ResolveParams) (any, error) {
			if err := server.Store.DeleteShortChatDocument(params.Context, params.Args["id"].(string)); err != nil {
				return false, err
			}
			server.Engine.Emit("short_chat_knowledge", "", nil)
			return true, nil
		}),
	}
}

func (server *Server) uploadShortChatDocument(c echo.Context) error {
	r := c.Request()
	if !server.sameOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return echo.ErrForbidden
	}
	user, valid := server.Auth.User(server.token(r))
	if !valid {
		return echo.ErrUnauthorized
	}
	if !user.IsAdmin() {
		return echo.ErrForbidden
	}
	if r.ContentLength > agent.MaxAttachmentBytes {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	body := http.MaxBytesReader(c.Response(), r.Body, agent.MaxAttachmentBytes)
	defer body.Close()
	stopRead := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stopRead()
	doc, err := server.Engine.AddShortChatDocument(ctx, r.URL.Query().Get("name"), body)
	if err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge)
		}
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	c.Response().Header().Set("Cache-Control", "private, no-store")
	return c.JSON(http.StatusCreated, doc)
}

func (server *Server) downloadShortChatDocument(c echo.Context) error {
	r := c.Request()
	if !server.sameOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return echo.ErrForbidden
	}
	if !server.Auth.Valid(server.token(r)) {
		return echo.ErrUnauthorized
	}
	name, data, err := server.Store.ShortChatDocument(r.Context(), c.Param("id"))
	if err != nil {
		return echo.ErrNotFound
	}
	headers := c.Response().Header()
	headers.Set("Cache-Control", "private, no-store")
	headers.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	headers.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	return c.Blob(http.StatusOK, "application/octet-stream", data)
}
