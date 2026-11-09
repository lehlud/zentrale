package webapi

import (
	"slices"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
)

// Diffs are UI records, not part of the provider transcript. Load them once per
// requested message batch, rather than issuing a query for each nested field.
func (server *Server) attachFileChanges(messages []map[string]any, info graphql.ResolveInfo) error {
	if nestedResourceFields(info, "activities") != nil {
		ids := []string{}
		for _, message := range messages {
			if id, ok := message["id"].(string); ok {
				ids = append(ids, id)
			}
		}

		rows, err := server.Store.ResourcesByID("tool_activity", ids, []string{"id", "activities"})
		if err != nil {
			return err
		}

		byID := map[string]any{}
		for _, row := range rows {
			id, _ := row["id"].(string)
			byID[id] = row["activities"]
		}

		for _, message := range messages {
			id, _ := message["id"].(string)
			message["activities"] = byID[id]
		}
	}

	fields := nestedResourceFields(info, "change")
	allFields := nestedResourceFields(info, "changes")
	if fields == nil && allFields == nil {
		return nil
	}

	for _, field := range allFields {
		if !slices.Contains(fields, field) {
			fields = append(fields, field)
		}
	}

	ids := make([]string, 0, len(messages))

	for _, message := range messages {
		if id, ok := message["id"].(string); ok {
			ids = append(ids, id)
		}
	}

	if !slices.Contains(fields, "id") {
		fields = append(fields, "id")
	}

	if !slices.Contains(fields, "messageId") {
		fields = append(fields, "messageId")
	}

	changes, err := server.Store.MessageFileChanges(ids, fields)
	if err != nil {
		return err
	}

	byID := map[string][]map[string]any{}
	for _, change := range changes {
		id, _ := change["messageId"].(string)
		if id == "" {
			id, _ = change["id"].(string)
		}

		byID[id] = append(byID[id], change)
	}

	for _, message := range messages {
		id, _ := message["id"].(string)
		if changes := byID[id]; len(changes) > 0 {
			message["change"] = changes[0]
			message["changes"] = changes
		}
	}

	return nil
}

func nestedResourceFields(info graphql.ResolveInfo, name string) []string {
	nested := info
	nested.FieldASTs = nil
	visited := map[string]bool{}
	var visit func(*ast.SelectionSet)
	visit = func(selection *ast.SelectionSet) {
		if selection == nil {
			return
		}

		for _, entry := range selection.Selections {
			switch field := entry.(type) {
			case *ast.Field:
				if field.Name.Value == name {
					nested.FieldASTs = append(nested.FieldASTs, field)
				}
			case *ast.InlineFragment:
				visit(field.SelectionSet)
			case *ast.FragmentSpread:
				if !visited[field.Name.Value] {
					visited[field.Name.Value] = true

					if fragment, ok := info.Fragments[field.Name.Value].(*ast.FragmentDefinition); ok {
						visit(fragment.SelectionSet)
					}
				}
			}
		}
	}

	for _, field := range info.FieldASTs {
		visit(field.SelectionSet)
	}

	if len(nested.FieldASTs) == 0 {
		return nil
	}

	return requestedResourceFields(nested, false)
}
