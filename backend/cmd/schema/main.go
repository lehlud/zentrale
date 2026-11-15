// schema exports introspection directly; production query limits remain enabled.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

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

	// graphql-go collects types from a map. Stable ordering keeps generated
	// schemas reviewable and prevents unrelated changes on every regeneration.
	introspection := result.Data.(map[string]any)["__schema"].(map[string]any)
	types := introspection["types"].([]any)
	sortByName(types)
	for _, value := range types {
		entry := value.(map[string]any)
		for _, field := range []string{"fields", "inputFields"} {
			entries, _ := entry[field].([]any) // Scalar types have no fields.
			sortByName(entries)
		}
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(result.Data); err != nil {
		panic(err)
	}
}

func sortByName(entries []any) {
	sort.Slice(entries, func(i, j int) bool {
		left := entries[i].(map[string]any)["name"].(string)
		right := entries[j].(map[string]any)["name"].(string)
		return left < right
	})
}
