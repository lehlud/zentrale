package webapi

import (
	"database/sql"
	"errors"
	"reflect"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providerlogin"
)

func (builder *schemaBuilder) providerQueries() graphql.Fields {
	server := builder.server
	queries := graphql.Fields{}
	queries["usageSummary"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.UsageSummary]()),
		Args: graphql.FieldConfigArgument{"agentId": {Type: graphql.NewNonNull(graphql.String)}},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			summary, err := server.Store.UsageSummary(params.Args["agentId"].(string))
			return mapOf(summary), err
		}),
	}

	queries["recentMessages"] = &graphql.Field{
		Type: graphql.NewList(builder.output(reflect.TypeFor[domain.Message]())),
		Args: graphql.FieldConfigArgument{
			"agentId":  {Type: graphql.NewNonNull(graphql.String)},
			"beforeId": {Type: graphql.String, DefaultValue: ""},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			rows, err := server.Store.TranscriptBefore(params.Args["agentId"].(string), 100, params.Args["beforeId"].(string))
			if err == nil {
				err = server.attachFileChanges(rows, params.Info)
			}

			return rows, err
		}),
	}

	queries["model"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.LanguageModel]()),
		Args: graphql.FieldConfigArgument{
			"providerId": {Type: graphql.NewNonNull(graphql.String)},
			"modelId":    {Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			var model map[string]any
			err := server.Store.Load("model", params.Args["providerId"].(string)+":"+params.Args["modelId"].(string), &model)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil
			}
			if err == nil {
				var provider domain.Provider
				if err = server.Store.Load("provider", params.Args["providerId"].(string), &provider); err == nil {
					model["reasoningLevels"] = domain.ReasoningLevels(provider.Kind, params.Args["modelId"].(string))
				}
			}

			return model, err
		}),
	}

	queries["modelDefaults"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.ModelDefaults]()),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			defaults, err := server.Engine.ModelDefaults()
			return mapOf(defaults), err
		}),
	}

	queries["models"] = &graphql.Field{
		Type: graphql.NewList(graphql.NewNonNull(builder.output(reflect.TypeFor[domain.LanguageModel]()))),
		Args: graphql.FieldConfigArgument{
			"providerId": {Type: graphql.NewNonNull(graphql.String)},
			"search":     {Type: graphql.String, DefaultValue: ""},
			"limit":      {Type: graphql.Int, DefaultValue: 100},
			"offset":     {Type: graphql.Int, DefaultValue: 0},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			models, err := server.Store.SearchModels(
				params.Args["providerId"].(string), params.Args["search"].(string),
				params.Args["limit"].(int), params.Args["offset"].(int),
			)
			if err != nil {
				return nil, err
			}
			var provider domain.Provider
			if err := server.Store.Load("provider", params.Args["providerId"].(string), &provider); err != nil {
				return nil, err
			}
			for _, model := range models {
				model["reasoningLevels"] = domain.ReasoningLevels(provider.Kind, model["id"].(string))
			}
			return models, nil
		}),
	}

	methodType := graphql.NewObject(graphql.ObjectConfig{
		Name: "ProviderKind",
		Fields: graphql.Fields{
			"id":          {Type: graphql.NewNonNull(graphql.String)},
			"name":        {Type: graphql.NewNonNull(graphql.String)},
			"authMethods": {Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(graphql.String)))},
		},
	})

	queries["providerKinds"] = &graphql.Field{
		Type: graphql.NewList(methodType),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			kinds := []any{}
			for _, entry := range []struct{ id, name string }{
				{"openai", "OpenAI"},
				{"anthropic", "Anthropic"},
				{"openrouter", "OpenRouter"},

				{
					"opencode",
					"OpenCode",
				},
				{
					"mistral",
					"Mistral",
				},
				{
					"litellm",
					"LiteLLM",
				},
				{
					"gemini",
					"Google / Gemini",
				},
			} {
				kinds = append(
					kinds,
					map[string]any{
						"id":          entry.id,
						"name":        entry.name,
						"authMethods": providerlogin.SupportedMethods(entry.id),
					},
				)
			}

			return kinds, nil
		}),
	}

	return queries
}

func (builder *schemaBuilder) providerMutations() graphql.Fields {
	server := builder.server
	mutations := graphql.Fields{}
	mutations["saveModelMetadata"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: graphql.FieldConfigArgument{
			"providerId": {Type: graphql.NewNonNull(graphql.String)},
			"modelId":    {Type: graphql.NewNonNull(graphql.String)},
			"input":      {Type: graphql.NewNonNull(builder.input(reflect.TypeFor[domain.ModelMetadata]()))},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			metadata, err := convert[domain.ModelMetadata](params.Args["input"])
			if err != nil {
				return false, err
			}

			return true, server.Engine.SaveModelMetadata(params.Args["providerId"].(string), params.Args["modelId"].(string), metadata)
		}),
	}

	choiceInput := builder.input(reflect.TypeFor[domain.ModelChoice]())
	requiredString := func(name string) graphql.FieldConfigArgument {
		return graphql.FieldConfigArgument{name: {Type: graphql.NewNonNull(graphql.String)}}
	}

	mutations["setModelDefaults"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: graphql.FieldConfigArgument{
			"input": {
				Type: graphql.NewNonNull(builder.input(reflect.TypeFor[domain.ModelDefaults]())),
			},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			defaults, err := convert[domain.ModelDefaults](params.Args["input"])
			if err != nil {
				return false, err
			}

			return true, server.Engine.SaveModelDefaults(defaults)
		}),
	}

	mutations["refreshModels"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: requiredString("providerId"),

		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			return true, server.Engine.RefreshProviderModels(params.Context, params.Args["providerId"].(string))
		}),
	}

	mutations["beginProviderLogin"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.ProviderLogin]()),
		Args: requiredString("providerId"),

		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			login, err := server.Engine.BeginProviderLogin(params.Context, params.Args["providerId"].(string))
			return mapOf(login), err
		}),
	}

	mutations["pollProviderLogin"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.ProviderLogin]()),
		Args: graphql.FieldConfigArgument{
			"id":   {Type: graphql.NewNonNull(graphql.String)},
			"code": {Type: graphql.String, DefaultValue: ""},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			login, err := server.Engine.PollProviderLogin(params.Context, params.Args["id"].(string), params.Args["code"].(string))
			return mapOf(login), err
		}),
	}

	mutations["cancelProviderLogin"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: requiredString("id"),

		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			return true, server.Engine.CancelProviderLogin(params.Args["id"].(string))
		}),
	}

	mutations["disconnectProvider"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: requiredString("providerId"),

		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			return true, server.Engine.DisconnectProvider(params.Args["providerId"].(string))
		}),
	}

	mutations["createAgent"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.Agent]()),
		Args: graphql.FieldConfigArgument{
			"projectId": {Type: graphql.NewNonNull(graphql.String)},

			"name": {Type: graphql.NewNonNull(graphql.String)},

			"main":     {Type: choiceInput},
			"approver": {Type: choiceInput},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			mainChoice, err := convert[domain.ModelChoice](params.Args["main"])
			if err != nil {
				return nil, err
			}

			approverChoice, err := convert[domain.ModelChoice](params.Args["approver"])
			if err != nil {
				return nil, err
			}

			created, err := server.Engine.CreateWithModels(
				params.Args["projectId"].(string), params.Args["name"].(string), mainChoice, approverChoice, "",
			)

			return mapOf(created), err
		}),
	}

	mutations["testProvider"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: graphql.FieldConfigArgument{
			"providerId": {Type: graphql.NewNonNull(graphql.String)},
			"modelId":    {Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			return server.Engine.TestProvider(params.Context, params.Args["providerId"].(string), params.Args["modelId"].(string))
		}),
	}

	return mutations
}
