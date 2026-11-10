package webapi

import (
	"errors"
	"reflect"

	"github.com/graphql-go/graphql"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func (builder *schemaBuilder) mutations() graphql.Fields {
	server := builder.server
	mutations := graphql.Fields{}

	mutations["login"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: graphql.FieldConfigArgument{
			"password": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(params graphql.ResolveParams) (any, error) {
			return state(params.Context).login(params.Args["password"].(string))
		},
	}

	mutations["setup"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: graphql.FieldConfigArgument{
			"password": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"commitIdentity": &graphql.ArgumentConfig{
				Type: graphql.NewNonNull(builder.input(resourceTypes["commitIdentity"])),
			},
		},
		Resolve: func(params graphql.ResolveParams) (any, error) {
			identity, err := convert[domain.CommitIdentity](params.Args["commitIdentity"])
			if err != nil {
				return false, err
			}

			password := params.Args["password"].(string)
			if err := server.Auth.SetupWithIdentity(password, identity); err != nil {
				return false, err
			}

			return state(params.Context).login(password)
		},
	}

	mutations["logout"] = &graphql.Field{
		Type: graphql.Boolean,
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			return true, state(params.Context).logout()
		}),
	}

	saveArgs := func(t reflect.Type) graphql.FieldConfigArgument {
		return graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{Type: graphql.NewNonNull(builder.input(t))},
		}
	}

	mutations["saveMachine"] = &graphql.Field{
		Type: builder.output(resourceTypes["machine"]),
		Args: saveArgs(resourceTypes["machine"]),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			v, err := convert[domain.Machine](params.Args["input"])
			if err != nil {
				return nil, err
			}

			saved, err := server.Engine.SaveMachine(v)

			return mapOf(saved), err
		}),
	}

	mutations["savePool"] = &graphql.Field{
		Type: builder.output(resourceTypes["pool"]),
		Args: graphql.FieldConfigArgument{
			"yaml": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			v, err := server.Engine.SavePool(params.Args["yaml"].(string))

			return mapOf(v), err
		}),
	}

	mutations["saveProject"] = &graphql.Field{
		Type: builder.output(resourceTypes["project"]),
		Args: saveArgs(resourceTypes["project"]),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			v, err := convert[domain.Project](params.Args["input"])
			if err != nil {
				return nil, err
			}

			saved, err := server.Engine.SaveProjectContext(params.Context, v)

			return mapOf(saved), err
		}),
	}

	mutations["saveSource"] = &graphql.Field{
		Type: builder.output(resourceTypes["source"]),
		Args: saveArgs(resourceTypes["source"]),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			v, err := convert[domain.Source](params.Args["input"])
			if err != nil {
				return nil, err
			}

			saved, err := server.Engine.SaveSource(v)

			return mapOf(saved), err
		}),
	}

	mutations["saveProvider"] = &graphql.Field{
		Type: builder.output(resourceTypes["provider"]),
		Args: saveArgs(resourceTypes["provider"]),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			v, err := convert[domain.Provider](params.Args["input"])
			if err != nil {
				return nil, err
			}

			saved, err := server.Engine.SaveProvider(v)

			return mapOf(saved), err
		}),
	}

	simple := func(name string, args graphql.FieldConfigArgument, resolve graphql.FieldResolveFn) {
		mutations[name] = &graphql.Field{Type: graphql.Boolean, Args: args, Resolve: authenticated(resolve)}
	}

	strArg := func(names ...string) graphql.FieldConfigArgument {
		args := graphql.FieldConfigArgument{}

		for _, name := range names {
			args[name] = &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}
		}

		return args
	}

	simple("deleteResource", strArg("kind", "id"), func(params graphql.ResolveParams) (any, error) {
		return true, server.Engine.Delete(params.Args["kind"].(string), params.Args["id"].(string))
	})

	simple("prompt", strArg("agentId", "text"), func(params graphql.ResolveParams) (any, error) {
		return true, server.Engine.PromptContext(params.Context, params.Args["agentId"].(string), params.Args["text"].(string))
	})

	mutations["submitInput"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.AgentInput]()),
		Args: strArg("agentId", "id", "text", "mode"),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			input, err := server.Engine.SubmitInput(
				params.Args["agentId"].(string),
				params.Args["id"].(string),
				params.Args["text"].(string),
				params.Args["mode"].(string),
			)

			return mapOf(input), err
		}),
	}

	simple("cancelInput", strArg("agentId", "id"), func(params graphql.ResolveParams) (any, error) {
		return true, server.Engine.CancelInput(params.Args["agentId"].(string), params.Args["id"].(string))
	})

	simple("resumeInputs", strArg("agentId"), func(params graphql.ResolveParams) (any, error) {
		return true, server.Engine.ResumeInputs(params.Args["agentId"].(string))
	})

	simple("cancelGoal", strArg("agentId", "goalId"), func(params graphql.ResolveParams) (any, error) {
		return true, server.Engine.CancelGoal(params.Args["agentId"].(string), params.Args["goalId"].(string))
	})

	simple("stopJob", strArg("id"), func(params graphql.ResolveParams) (any, error) {
		return true, server.Engine.StopJob(params.Args["id"].(string))
	})

	simple("stopAgent", strArg("agentId"), func(params graphql.ResolveParams) (any, error) {
		return true, server.Engine.Stop(params.Args["agentId"].(string))
	})

	simple("renameAgent", strArg("agentId", "name"), func(params graphql.ResolveParams) (any, error) {
		return true, server.Engine.RenameAgent(params.Args["agentId"].(string), params.Args["name"].(string))
	})

	simple("answerQuestion", strArg("id", "answer", "status"), func(params graphql.ResolveParams) (any, error) {
		return true, server.Engine.AnswerQuestion(
			params.Args["id"].(string),
			params.Args["answer"].(string),
			params.Args["status"].(string),
			"",
		)
	})

	simple("trustHostKey", strArg("publicKey", "trust"), func(params graphql.ResolveParams) (any, error) {
		raw, err := remote.DecodePublicKey(params.Args["publicKey"].(string))
		if err != nil {
			return nil, err
		}

		if params.Args["trust"] == "trust" {
			return true, server.Store.TrustSSHKey(raw)
		}

		if params.Args["trust"] != "distrust" {
			return nil, errors.New("trust must be trust or distrust")
		}

		return true, server.Store.DistrustSSHKey(raw)
	})

	mutations["generateSSHKey"] = &graphql.Field{
		Type: builder.sshKeyType(),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			key, err := server.Store.GenerateSSHKey()
			if err != nil {
				return nil, err
			}

			pub, err := remote.PublicKey(key.X509Data)

			return map[string]any{"id": key.ID, "publicKey": pub}, err
		}),
	}

	simple("deleteSSHKey", graphql.FieldConfigArgument{
		"id": &graphql.ArgumentConfig{
			Type: graphql.NewNonNull(graphql.Int),
		},
	}, func(params graphql.ResolveParams) (any, error) {
		id := params.Args["id"].(int)

		return true, server.Store.DropPrivSSHKey(id)
	})

	simple("setGitHubToken", strArg("token"), func(params graphql.ResolveParams) (any, error) {
		return true, server.Store.SetSetting("github_token", params.Args["token"].(string))
	})

	simple("setCommitIdentity", saveArgs(resourceTypes["commitIdentity"]), func(params graphql.ResolveParams) (any, error) {
		identity, err := convert[domain.CommitIdentity](params.Args["input"])
		if err != nil {
			return false, err
		}

		return true, server.Engine.SaveCommitIdentity(identity)
	})

	mutations["testMachine"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.MachineConnectionCheck]()), Args: strArg("id"),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			return mapOf(server.Engine.CheckMachineConnection(params.Context, params.Args["id"].(string))), nil
		}),
	}

	mutations["testPool"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.PoolConnectionCheck]()),
		Args: strArg("id"),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			result, err := server.Engine.CheckPoolConnection(params.Context, params.Args["id"].(string))
			if err != nil {
				return nil, err
			}

			return mapOf(result), nil
		}),
	}

	mutations["prepareAgent"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.MachineConnectionCheck]()), Args: strArg("agentId"),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			return mapOf(server.Engine.CheckAgentConnection(params.Context, params.Args["agentId"].(string))), nil
		}),
	}

	mutations["startEditor"] = &graphql.Field{
		Type: builder.output(reflect.TypeFor[domain.EditorStartResult]()),
		Args: graphql.FieldConfigArgument{
			"projectId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"agentId":   &graphql.ArgumentConfig{Type: graphql.String, DefaultValue: ""},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			session, err := server.Engine.StartEditor(params.Context, params.Args["projectId"].(string), params.Args["agentId"].(string))
			if err == nil {
				err = server.Workbench.Ensure(params.Context)
			}

			result := domain.EditorStartResult{Session: session}

			if err != nil {
				result.Error = err.Error()
				result.HostKey = remote.HostKeyChallenge(err)
			} else {
				server.Engine.Emit("editor", "", session)
			}

			return mapOf(result), nil
		}),
	}

	simple("stopEditor", strArg("projectId", "sessionId"), func(params graphql.ResolveParams) (any, error) {
		err := server.Engine.Editors.Stop(params.Args["projectId"].(string), params.Args["sessionId"].(string))
		if err == nil {
			server.Engine.Emit("editor", "", nil)
		}

		return true, err
	})

	for name, field := range builder.providerMutations() {
		mutations[name] = field
	}

	for name, field := range builder.gitMutations() {
		mutations[name] = field
	}

	return mutations
}
