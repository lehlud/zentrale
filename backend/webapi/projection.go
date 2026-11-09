package webapi

import (
	"sort"
	"strings"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
)

// Select top-level resource fields in SQL. A message list requesting only IDs
// must not load every model reasoning payload from DuckDB into Go memory.
func requestedResourceFields(info graphql.ResolveInfo, provider bool) []string {
	selected := map[string]bool{}
	visited := map[string]bool{}
	var visit func(*ast.SelectionSet)
	visit = func(selection *ast.SelectionSet) {
		if selection == nil {
			return
		}
		for _, item := range selection.Selections {
			switch node := item.(type) {
			case *ast.Field:
				if !strings.HasPrefix(node.Name.Value, "__") {
					selected[node.Name.Value] = true
				}
			case *ast.InlineFragment:
				visit(node.SelectionSet)
			case *ast.FragmentSpread:
				if visited[node.Name.Value] {
					continue
				}
				visited[node.Name.Value] = true
				if fragment, valid := info.Fragments[node.Name.Value].(*ast.FragmentDefinition); valid {
					visit(fragment.SelectionSet)
				}
			}
		}
	}
	for _, field := range info.FieldASTs {
		visit(field.SelectionSet)
	}
	if selected["change"] || selected["changes"] || selected["activities"] {
		selected["id"] = true
	}
	if provider {
		// These private fields are consumed by the resolver and removed before
		// output. Neither is present in the public GraphQL type.
		if selected["connected"] {
			selected["credential"] = true
			selected["oauth"] = true
			selected["catalogStatus"] = true
		}
		if selected["accountLabel"] {
			selected["oauth"] = true
		}
	}
	fields := make([]string, 0, len(selected))
	for field := range selected {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}
