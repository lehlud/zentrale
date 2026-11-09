// schema exports introspection directly; production query limits remain enabled.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/webapi"
)

func main() {
	schema, err := (&webapi.Server{}).Schema()
	if err != nil {
		panic(err)
	}
	result := graphql.Do(graphql.Params{
		Schema:        schema,
		RequestString: `query { __schema { types { kind name fields { name type { kind name ofType { kind name ofType { kind name ofType {kind name} } } } } inputFields { name type { kind name ofType {kind name ofType {kind name ofType {kind name}}} } } } } }`,
	})
	if len(result.Errors) > 0 {
		panic(fmt.Sprint(result.Errors))
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result.Data); err != nil {
		panic(err)
	}
}
