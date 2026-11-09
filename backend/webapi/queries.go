package webapi

import (
	"github.com/lehlud/zentrale/datastore"
	"reflect"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func (builder *schemaBuilder) queries() graphql.Fields {
	server := builder.server
	queries := graphql.Fields{}
	queries["goal"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.Goal]()),
		Args: graphql.FieldConfigArgument{"agentId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			goal, err := server.Engine.CurrentGoal(params.Args["agentId"].(string))
			if goal == nil || err != nil {
				return nil, err
			}
			return mapOf(*goal), nil
		}),
	}
	queries["agentInputs"] = &graphql.Field{
		Type: graphql.NewList(builder.output(reflect.TypeFor[domain.AgentInput]())),
		Args: graphql.FieldConfigArgument{"agentId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			inputs, err := server.Store.AgentInputs(params.Args["agentId"].(string))
			return mapList(inputs), err
		}),
	}
	queries["agentRunState"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.AgentRunState]()),
		Args: graphql.FieldConfigArgument{"agentId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			return mapOf(server.Engine.RunState(params.Args["agentId"].(string))), nil
		}),
	}
	queries["commitIdentity"] = &graphql.Field{
		Type: builder.output(resourceTypes["commitIdentity"]),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			identity, err := server.Engine.CommitIdentity()
			return mapOf(identity), err
		}),
	}
	queries["editorSession"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.EditorSession]()),
		Args: graphql.FieldConfigArgument{
			"projectId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"agentId":   &graphql.ArgumentConfig{Type: graphql.String, DefaultValue: ""},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			session, err := server.Engine.CurrentEditor(params.Args["projectId"].(string), params.Args["agentId"].(string))
			return mapOf(session), err
		}),
	}
	queries["sandboxStatuses"] = &graphql.Field{
		Type: graphql.NewList(builder.output(reflect.TypeFor[domain.SandboxStatus]())),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			statuses, err := server.Engine.SandboxStatuses()
			rows := make([]map[string]any, 0, len(statuses))
			for _, status := range statuses {
				rows = append(rows, mapOf(status))
			}
			return rows, err
		}),
	}
	statusType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Status",
		Fields: graphql.Fields{
			"needsSetup":      &graphql.Field{Type: graphql.NewNonNull(graphql.Boolean)},
			"authenticated":   &graphql.Field{Type: graphql.NewNonNull(graphql.Boolean)},
			"githubConnected": &graphql.Field{Type: graphql.NewNonNull(graphql.Boolean)},
		},
	})
	queries["status"] = &graphql.Field{Type: statusType, Resolve: func(params graphql.ResolveParams) (any, error) {
		need, err := server.Auth.NeedsSetup()
		connected := false
		if state(params.Context).authenticated {
			token, _ := server.Store.Setting("github_token")
			connected = token != ""
			connections, _ := server.Engine.GitForge.Connections()
			for _, connection := range connections {
				if connection.Kind == "github" && connection.Connected {
					connected = true
				}
			}
		}
		return map[string]any{
			"needsSetup":      need,
			"authenticated":   state(params.Context).authenticated,
			"githubConnected": connected,
		}, err
	}}
	collections := map[string]string{
		"machines":    "machine",
		"pools":       "pool",
		"projects":    "project",
		"sources":     "source",
		"providers":   "provider",
		"agents":      "agent",
		"messages":    "message",
		"notes":       "note",
		"questions":   "question",
		"approvals":   "approval",
		"usage":       "usage",
		"jobs":        "job",
		"goalReviews": "goal_review",
	}
	for field, kind := range collections {
		parentName := ""
		switch kind {
		case "source", "agent":
			parentName = "projectId"
		case "message", "note", "question", "approval", "usage", "job", "goal_review":
			parentName = "agentId"
		}
		args := graphql.FieldConfigArgument{
			"limit":  &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 100},
			"offset": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 0},
		}
		if parentName != "" {
			args[parentName] = &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}
		}
		queries[field] = &graphql.Field{Type: graphql.NewList(builder.output(resourceTypes[kind])), Args: args, Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			var parent *string
			if parentName != "" {
				value := params.Args[parentName].(string)
				parent = &value
			}
			rows, err := server.Store.ListFields(kind, parent, params.Args["limit"].(int), params.Args["offset"].(int), requestedResourceFields(params.Info, kind == "provider"))
			if err == nil && kind == "message" {
				err = server.attachFileChanges(rows, params.Info)
			}
			if kind == "provider" {
				for _, row := range rows {
					credential, _ := row["credential"].(string)
					oauth, _ := row["oauth"].(map[string]any)
					row["connected"] = row["catalogStatus"] == "ready" || credential != "" || oauth["accessToken"] != "" && oauth["accessToken"] != nil
					row["accountLabel"] = oauth["accountLabel"]
					delete(row, "oauth")
					delete(row, "credential")
				}
			}
			return rows, err
		})}
	}
	for _, kind := range []string{"project", "agent"} {
		queries[kind] = &graphql.Field{Type: builder.output(resourceTypes[kind]), Args: graphql.FieldConfigArgument{"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}}, Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			var row map[string]any
			err := server.Store.Load(kind, params.Args["id"].(string), &row)
			return row, err
		})}
	}
	sshType := builder.sshKeyType()
	queries["sshKeys"] = &graphql.Field{Type: graphql.NewList(sshType), Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
		keys, err := server.Store.ListPrivSSHKeys()
		if err != nil {
			return nil, err
		}
		usages, err := server.Store.SSHKeyUsages()
		if err != nil {
			return nil, err
		}
		result := []any{}
		for _, key := range keys {
			pub, err := remote.PublicKey(key.X509Data)
			if err != nil {
				return nil, err
			}
			raw, err := remote.DecodePublicKey(pub)
			if err != nil {
				return nil, err
			}
			result = append(result, map[string]any{"id": key.ID, "publicKey": pub, "fingerprint": remote.KeyFingerprint(raw), "purpose": key.Purpose, "purposeId": key.PurposeID, "usages": mapList(usages[key.ID]), "canDelete": len(usages[key.ID]) == 0, "canRevoke": repositoryKeyCanRevoke(usages[key.ID])})
		}
		return result, nil
	})}
	queries["trustedKeys"] = &graphql.Field{Type: graphql.NewList(sshType), Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
		keys, err := server.Store.TrustedKeys()
		if err != nil {
			return nil, err
		}
		result := []any{}
		for _, raw := range keys {
			result = append(result, map[string]any{"publicKey": hostPublic(raw), "fingerprint": remote.KeyFingerprint(raw)})
		}
		return result, nil
	})}

	for name, field := range builder.providerQueries() {
		queries[name] = field
	}
	for name, field := range builder.gitQueries() {
		queries[name] = field
	}
	return queries
}

func sshKeyType() *graphql.Object {
	return graphql.NewObject(graphql.ObjectConfig{
		Name: "SSHKey",
		Fields: graphql.Fields{
			"id":          &graphql.Field{Type: graphql.Int},
			"purpose":     &graphql.Field{Type: graphql.String},
			"purposeId":   &graphql.Field{Type: graphql.String},
			"usages":      &graphql.Field{Type: graphql.NewList(newSchemaBuilder(nil).output(reflect.TypeFor[datastore.SSHKeyUsage]()))},
			"canDelete":   &graphql.Field{Type: graphql.Boolean},
			"canRevoke":   &graphql.Field{Type: graphql.Boolean},
			"publicKey":   &graphql.Field{Type: graphql.String},
			"fingerprint": &graphql.Field{Type: graphql.String},
		},
	})
}

func repositoryKeyCanRevoke(usages []datastore.SSHKeyUsage) bool {
	if len(usages) == 0 {
		return false
	}
	for _, use := range usages {
		if use.Kind != "repository" || use.ProjectID != "" {
			return false
		}
	}
	return true
}
