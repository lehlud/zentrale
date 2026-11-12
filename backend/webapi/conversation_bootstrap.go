package webapi

import (
	"errors"
	"reflect"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/domain"
)

// conversationBootstrapField combines project selection and compact transcript
// loading into one authenticated request. This single-user installation uses
// the same session authorization as the existing project/agent query roots
// (schema.go, queries.go). Explicit agent selections must additionally belong
// to the requested project, before any nested transcript resolver is invoked.
func (builder *schemaBuilder) conversationBootstrapField(queries graphql.Fields) *graphql.Field {
	server := builder.server
	fields := graphql.Fields{
		"project":     {Type: graphql.NewNonNull(builder.output(reflect.TypeFor[domain.Project]()))},
		"agents":      {Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(builder.output(reflect.TypeFor[domain.Agent]()))))},
		"agent":       {Type: builder.output(reflect.TypeFor[domain.Agent]())},
		"machineName": {Type: graphql.NewNonNull(graphql.String)},
		"sandboxMode": {Type: graphql.NewNonNull(graphql.String)},
	}
	for name, original := range map[string]string{
		"messages": "recentMessages", "goal": "goal", "agentInputs": "agentInputs",
		"agentRunState": "agentRunState", "modelDefaults": "modelDefaults",
	} {
		root := queries[original]
		fields[name] = &graphql.Field{Type: root.Type, Resolve: func(params graphql.ResolveParams) (any, error) {
			id := params.Source.(map[string]any)["agentId"].(string)
			if id == "" && original != "modelDefaults" {
				if original == "recentMessages" || original == "agentInputs" {
					return []any{}, nil
				}
				return nil, nil
			}
			// Keep the child ResolveInfo: recentMessages must see the requested
			// attachment/activity/diff fields, including aliases and fragments.
			params.Args = map[string]any{"agentId": id, "beforeId": ""}
			return root.Resolve(params)
		}}
	}
	fields["questions"] = &graphql.Field{
		Type: queries["questions"].Type,
		Resolve: func(params graphql.ResolveParams) (any, error) {
			id := params.Source.(map[string]any)["agentId"].(string)
			result := []map[string]any{}
			if id == "" {
				return result, nil
			}
			// Do not silently lose open questions beyond the first history page.
			for offset := 0; ; offset += 100 {
				rows, err := server.Store.ListStatusFields("question", id, "open", 100, offset,
					[]string{"id", "agentId", "title", "options", "status", "answer"})
				if err != nil {
					return nil, err
				}
				result = append(result, rows...)
				if len(rows) < 100 {
					return result, nil
				}
			}
		},
	}
	fields["jobs"] = &graphql.Field{
		Type: queries["jobs"].Type,
		Resolve: func(params graphql.ResolveParams) (any, error) {
			id := params.Source.(map[string]any)["agentId"].(string)
			if id == "" {
				return []map[string]any{}, nil
			}
			return server.Store.ListStatusFields("job", id, "running", 100, 0,
				[]string{"id", "command", "status", "error", "startedAt", "machineId", "agentId", "timeoutMs"})
		},
	}
	return &graphql.Field{
		Type: graphql.NewObject(graphql.ObjectConfig{Name: "ConversationBootstrap", Fields: fields}),
		Args: graphql.FieldConfigArgument{
			"projectId": {Type: graphql.NewNonNull(graphql.String)},
			"agentId":   {Type: graphql.String, DefaultValue: ""},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			projectID := params.Args["projectId"].(string)
			var project domain.Project
			if err := server.Store.Load("project", projectID, &project); err != nil {
				return nil, err
			}
			agentFields := []string{
				"id", "name", "role", "status", "parentId", "approverId", "projectId",
				"providerId", "modelId", "machineId", "error", "contextStart",
			}
			rows, err := server.Store.ListFields("agent", &projectID, 500, 0, agentFields)
			if err != nil {
				return nil, err
			}
			id, _ := params.Args["agentId"].(string)
			if id == "" {
				for _, row := range rows {
					if row["role"] == "main" {
						id, _ = row["id"].(string)
						break
					}
				}
			}
			var selected any
			var machine map[string]any
			if id != "" {
				var agent domain.Agent
				if err := server.Store.LoadFields("agent", id, &agent, agentFields); err != nil {
					return nil, err
				}
				if agent.ProjectID != projectID {
					return nil, errors.New("agent does not belong to project")
				}
				selected = mapOf(agent)
				if agent.MachineID != "" {
					// Metadata only: never contact a host or enumerate providers.
					_ = server.Store.LoadFields("machine", agent.MachineID, &machine, []string{"name", "sandbox"})
				}
			}
			name, _ := machine["name"].(string)
			mode, _ := machine["sandbox"].(string)
			return map[string]any{"project": mapOf(project), "agents": rows, "agent": selected,
				"agentId": id, "machineName": name, "sandboxMode": mode}, nil
		}),
	}
}
