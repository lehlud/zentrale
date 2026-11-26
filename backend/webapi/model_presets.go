package webapi

import (
	"errors"
	"reflect"
	"slices"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/domain"
)

func (server *Server) selectableModelPresets(scope string, user domain.User) ([]domain.ModelChoice, error) {
	policy, err := server.Engine.ModelPresetPolicy(scope)
	if err != nil || policy.Configured {
		return policy.Presets, err
	}
	if user.IsAdmin() {
		return nil, nil // Keep the legacy catalog accessible to admins during setup.
	}

	presets := []domain.ModelChoice{}
	for _, choice := range []domain.ModelChoice{policy.Defaults.Main, policy.Defaults.Approver} {
		if choice.ModelID != "" && !slices.Contains(presets, choice) {
			presets = append(presets, choice)
		}
	}
	return presets, nil
}

func (server *Server) requireSelectableModel(scope string, user domain.User, choices ...domain.ModelChoice) error {
	presets, err := server.selectableModelPresets(scope, user)
	if err != nil || presets == nil {
		return err
	}
	for _, choice := range choices {
		if choice != (domain.ModelChoice{}) && !slices.Contains(presets, choice) {
			return errors.New("select a released model and thinking level")
		}
	}
	return nil
}

func (builder *schemaBuilder) modelPresetFields(queries, mutations graphql.Fields) {
	server := builder.server
	policyType := reflect.TypeFor[agent.ModelPresetPolicy]()
	defaultsType := reflect.TypeFor[domain.ModelDefaults]()
	choiceType := reflect.TypeFor[domain.ModelChoice]()
	queries["shortChatModelOptions"] = &graphql.Field{
		Type: builder.output(policyType),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			user := state(params.Context).user
			presets, err := server.selectableModelPresets(agent.ChatModels, user)
			if err != nil {
				return nil, err
			}

			defaults, err := server.Engine.EffectiveModelDefaults(agent.ChatModels, user.ID)
			if presets == nil && defaults.Main != (domain.ModelChoice{}) {
				presets = []domain.ModelChoice{defaults.Main}
			}
			return mapOf(agent.ModelPresetPolicy{Presets: presets, Defaults: defaults}), err
		}),
	}
	queries["modelPresetPolicy"] = &graphql.Field{
		Type: builder.output(policyType), Args: gitStringArgs("scope"),
		Resolve: adminOnly(func(params graphql.ResolveParams) (any, error) {
			policy, err := server.Engine.ModelPresetPolicy(params.Args["scope"].(string))
			return mapOf(policy), err
		}),
	}
	queries["personalModelDefaults"] = &graphql.Field{
		Type: builder.output(defaultsType), Args: gitStringArgs("scope"),
		Resolve: adminOnly(func(params graphql.ResolveParams) (any, error) {
			defaults, err := server.Engine.PersonalModelDefaults(params.Args["scope"].(string), state(params.Context).user.ID)
			return mapOf(defaults), err
		}),
	}
	queries["conversationModelPresets"] = &graphql.Field{
		Type: graphql.NewList(builder.output(choiceType)), Args: gitStringArgs("agentId"),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			id := params.Args["agentId"].(string)
			if err := server.agentAccess(state(params.Context).user, id, false); err != nil {
				return nil, err
			}
			var record domain.Agent
			if err := server.Store.Load("agent", id, &record); err != nil {
				return nil, err
			}
			scope := agent.ProjectModels
			if record.ChatOwnerID != "" {
				scope = agent.ChatModels
			}
			presets, err := server.selectableModelPresets(scope, state(params.Context).user)
			if err != nil || presets == nil {
				return nil, err
			}
			return mapList(presets), nil
		}),
	}

	mutations["setModelPresetPolicy"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: graphql.FieldConfigArgument{
			"scope":    {Type: graphql.NewNonNull(graphql.String)},
			"presets":  {Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(builder.input(choiceType))))},
			"defaults": {Type: graphql.NewNonNull(builder.input(defaultsType))},
		},
		Resolve: adminOnly(func(params graphql.ResolveParams) (any, error) {
			defaults, err := convert[domain.ModelDefaults](params.Args["defaults"])
			if err != nil {
				return false, err
			}
			presets, err := convert[[]domain.ModelChoice](params.Args["presets"])
			if err != nil {
				return false, err
			}
			err = server.Engine.SaveModelPresetPolicy(params.Args["scope"].(string), agent.ModelPresetPolicy{Presets: presets, Defaults: defaults})
			return err == nil, err
		}),
	}
	mutations["setPersonalModelDefaults"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: graphql.FieldConfigArgument{
			"scope":    {Type: graphql.NewNonNull(graphql.String)},
			"defaults": {Type: graphql.NewNonNull(builder.input(defaultsType))},
		},
		Resolve: adminOnly(func(params graphql.ResolveParams) (any, error) {
			defaults, err := convert[domain.ModelDefaults](params.Args["defaults"])
			if err != nil {
				return false, err
			}
			err = server.Engine.SavePersonalModelDefaults(params.Args["scope"].(string), state(params.Context).user.ID, defaults)
			return err == nil, err
		}),
	}
}
