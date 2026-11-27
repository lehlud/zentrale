package webapi

import (
	"reflect"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/domain"
)

func (builder *schemaBuilder) conversationBranchFields(queries, mutations graphql.Fields) {
	engine := builder.server.Engine
	agentType := builder.output(reflect.TypeFor[domain.Agent]())
	queries["conversationBranches"] = &graphql.Field{
		Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(agentType))),
		Args: gitStringArgs("agentId"),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			branches, err := engine.ConversationBranches(params.Context, state(params.Context).user.ID, params.Args["agentId"].(string))
			if err != nil {
				return nil, err
			}

			result := make([]map[string]any, 0, len(branches))
			for _, branch := range branches {
				result = append(result, mapOf(branch))
			}
			return result, nil
		}),
	}

	args := gitStringArgs("agentId", "messageId")
	args["text"] = &graphql.ArgumentConfig{Type: graphql.String}
	mutations["branchConversation"] = &graphql.Field{
		Type: agentType,
		Args: args,
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			var text *string
			if value, ok := params.Args["text"].(string); ok {
				text = &value
			}

			branch, err := engine.BranchConversation(params.Context, state(params.Context).user.ID,
				params.Args["agentId"].(string), params.Args["messageId"].(string), text)
			if err != nil {
				return nil, err
			}
			return mapOf(branch), nil
		}),
	}
}
