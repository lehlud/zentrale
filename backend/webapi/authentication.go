package webapi

import (
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/gqlerrors"
)

// sessionExpired is an application-specific WebSocket close code. An invalid
// HTTP handshake still returns 401; established sockets use this explicit code
// so clients do not mistake revoked credentials for a temporary network outage.
const sessionExpired = 4401

type authenticationError struct{}

func (authenticationError) Error() string { return "authentication required" }

func (authenticationError) Extensions() map[string]any {
	return map[string]any{"code": "UNAUTHENTICATED"}
}

var errAuthenticationRequired = authenticationError{}

// Validation errors bypass GraphQL's resolver wrapper, which normally copies
// ExtendedError metadata. Preserve the same wire format on both paths.
func graphQLErrorResult(err error) graphql.Result {
	formatted := gqlerrors.FormatError(err)
	if extended, ok := err.(gqlerrors.ExtendedError); ok {
		formatted.Extensions = extended.Extensions()
	}
	return graphql.Result{Errors: []gqlerrors.FormattedError{formatted}}
}
