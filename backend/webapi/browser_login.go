package webapi

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/oauthflow"
)

// A browser redirect is not authentication. The same-origin completion POST
// must carry exactly the Zentrale session which initiated the PKCE transaction.
// Keeping SameSite=Strict also avoids granting the external callback request
// access to authenticated application data.
func browserLoginContext(ctx context.Context, raw string) (context.Context, error) {
	if raw == "" {
		return ctx, nil // Existing device and pasted-code flows have no callback.
	}

	current := state(ctx)
	address, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || address.User != nil ||
		address.RawQuery != "" || address.ForceQuery || address.Fragment != "" ||
		address.RawPath != "" || address.Path != path.Clean(address.Path) ||
		!strings.HasSuffix(address.Path, "/oauth/callback") ||
		address.Scheme+"://"+address.Host != current.origin ||
		current.sessionHash == "" || !current.authenticated {
		return ctx, errors.New("browser login requires a same-origin callback and an active application session")
	}

	return oauthflow.WithCallback(ctx, oauthflow.Callback{
		RedirectURI: address.String(),
		Owner:       current.sessionHash,
	}), nil
}

func browserGitLoginArgs() graphql.FieldConfigArgument {
	args := gitStringArgs("connectionId")
	args["redirectUri"] = &graphql.ArgumentConfig{Type: graphql.String, DefaultValue: ""}

	return args
}

func (builder *schemaBuilder) completeBrowserLogin() *graphql.Field {
	return &graphql.Field{
		Type: graphql.Boolean,
		Args: gitStringArgs("state", "code"),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			stateValue := params.Args["state"].(string)
			if len(stateValue) > 1024 {
				return false, errors.New("invalid browser login state")
			}

			parts := strings.Split(stateValue, ":")
			if len(parts) == 3 && parts[0] == "huggingface" {
				connection, err := builder.server.Engine.HuggingFace.CompleteOAuth(
					params.Context, parts[1], stateValue, params.Args["code"].(string), state(params.Context).sessionHash,
				)
				if err == nil {
					builder.server.Engine.Emit("huggingface", "", connection)
				}

				return err == nil && connection.Connected && connection.AuthMode == "oauth", err
			}

			if len(parts) == 3 && parts[0] == "provider" {
				login, err := builder.server.Engine.CompleteGoogleLogin(
					params.Context, parts[1], stateValue, params.Args["code"].(string), state(params.Context).sessionHash,
				)

				return err == nil && login.Status == "connected", err
			}

			if len(parts) != 4 || parts[0] != "git" {
				return false, errors.New("unknown browser login; start again")
			}

			login, err := builder.server.Engine.GitForge.CompleteBrowser(
				params.Context,
				parts[1],
				parts[2],
				stateValue,
				params.Args["code"].(string),
				state(params.Context).sessionHash,
			)
			if login.ID != "" {
				builder.server.Engine.Emit("git_login", "", map[string]string{
					"id": login.ID, "connectionId": login.ConnectionID,
				})
			}

			return err == nil && (login.Status == "connected" || login.Status == "pending"), err
		}),
	}
}
