package webapi

import (
	"context"
	"errors"
	"reflect"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/domain"
)

func (builder *schemaBuilder) shortChatFields(queries, mutations graphql.Fields) {
	server := builder.server
	chatType := builder.output(reflect.TypeFor[domain.Agent]())
	queries["shortChatSettings"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[agent.ShortChatSettings]()),
		Resolve: adminOnly(func(_ graphql.ResolveParams) (any, error) {
			settings, err := server.Engine.ShortChatSettings()
			return mapOf(settings), err
		}),
	}

	saveSettings := resourceMutation(builder, func(_ context.Context, settings agent.ShortChatSettings) (agent.ShortChatSettings, error) {
		return server.Engine.SaveShortChatSettings(settings)
	})
	saveSettings.Resolve = adminOnly(saveSettings.Resolve)
	mutations["saveShortChatSettings"] = saveSettings
	queries["shortChat"] = &graphql.Field{
		Type: chatType,
		Args: gitStringArgs("id"),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			id := params.Args["id"].(string)
			var chat domain.Agent
			if err := server.Store.Load("agent", id, &chat); err != nil || !ownsShortChat(state(params.Context).user, chat) {
				return nil, errProjectUnavailable
			}

			return mapOf(chat), nil
		}),
	}
	queries["shortChats"] = &graphql.Field{
		Type: graphql.NewList(chatType),
		Args: graphql.FieldConfigArgument{
			"limit":  {Type: graphql.Int, DefaultValue: 100},
			"offset": {Type: graphql.Int, DefaultValue: 0},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			return server.Store.ShortChats(state(params.Context).user.ID, params.Args["limit"].(int),
				params.Args["offset"].(int), requestedResourceFields(params.Info, false))
		}),
	}
	mutations["createShortChat"] = &graphql.Field{
		Type: chatType,
		Args: graphql.FieldConfigArgument{"name": {Type: graphql.String, DefaultValue: ""}},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			chat, err := server.Engine.CreateShortChat(state(params.Context).user.ID, params.Args["name"].(string))
			return mapOf(chat), err
		}),
	}

	shareConfig := reflect.TypeFor[agent.FileShareConfig]()
	queries["fileShares"] = &graphql.Field{
		Type: graphql.NewList(builder.output(shareConfig)),
		Resolve: adminOnly(func(_ graphql.ResolveParams) (any, error) {
			return server.Engine.FileShares(true)
		}),
	}
	mutations["saveFileShare"] = &graphql.Field{
		Type: builder.output(shareConfig),
		Args: graphql.FieldConfigArgument{
			"input":    {Type: graphql.NewNonNull(builder.input(shareConfig))},
			"password": {Type: graphql.String},
		},
		Resolve: adminOnly(func(params graphql.ResolveParams) (any, error) {
			config, err := convert[agent.FileShareConfig](params.Args["input"])
			if err != nil {
				return nil, err
			}
			var password *string
			if value, ok := params.Args["password"].(string); ok {
				password = &value
			}
			saved, err := server.Engine.SaveFileShare(config, password)
			return mapOf(saved), err
		}),
	}
	mutations["deleteFileShare"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: gitStringArgs("id"),
		Resolve: adminOnly(func(params graphql.ResolveParams) (any, error) {
			err := server.Engine.DeleteFileShare(params.Args["id"].(string))
			return err == nil, err
		}),
	}
}

func (server *Server) deleteAccess(user domain.User, kind, id string) error {
	switch kind {
	case "agent":
		return server.agentAccess(user, id, true)
	case "project":
		return server.projectAccess(user, id, true, true)
	case "source":
		var source domain.Source
		if err := server.Store.Load(kind, id, &source); err != nil {
			return errProjectUnavailable
		}
		return server.projectAccess(user, source.ProjectID, true, true)
	case "machine", "pool", "provider":
		if !user.IsAdmin() {
			return errAdministratorRequired
		}
		return nil
	default:
		return errors.New("unsupported deletion")
	}
}
