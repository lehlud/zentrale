package webapi

import (
	"errors"
	"fmt"

	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/source"
)

// ValidateQuery expands fragments before counting depth and total fields. Cycles,
// huge alias fans and deep introspection requests are rejected before execution.
func ValidateQuery(query, operationName string, authenticated bool) error {
	if len(query) > 64*1024 {
		return errors.New("GraphQL document exceeds 64 KiB")
	}
	doc, err := parser.Parse(parser.ParseParams{Source: source.NewSource(&source.Source{Body: []byte(query)})})
	if err != nil {
		return err
	}
	fragments := map[string]*ast.FragmentDefinition{}
	ops := []*ast.OperationDefinition{}
	for _, definition := range doc.Definitions {
		switch d := definition.(type) {
		case *ast.FragmentDefinition:
			fragments[d.Name.Value] = d
		case *ast.OperationDefinition:
			ops = append(ops, d)
		}
	}
	if len(ops) > 1 && operationName == "" {
		return errors.New("operationName is required")
	}
	var op *ast.OperationDefinition
	for _, candidate := range ops {
		if operationName == "" || (candidate.Name != nil && candidate.Name.Value == operationName) {
			op = candidate
			break
		}
	}
	if op == nil {
		return errors.New("operation not found")
	}
	if op.Operation == "subscription" {
		return errors.New("use /events for streaming")
	}
	count, roots := 0, 0
	var visit func(*ast.SelectionSet, int, map[string]bool) error
	visit = func(set *ast.SelectionSet, depth int, stack map[string]bool) error {
		if set == nil {
			return nil
		}
		for _, selection := range set.Selections {
			switch node := selection.(type) {
			case *ast.Field:
				count++
				if depth > 4 {
					return errors.New("GraphQL maximum depth is 4")
				}
				if count > 500 {
					return errors.New("GraphQL field budget exceeded")
				}
				if depth == 1 {
					roots++
					if roots > 20 {
						return errors.New("GraphQL root field budget exceeded")
					}
					if !authenticated && node.Name.Value != "status" && node.Name.Value != "login" && node.Name.Value != "setup" {
						return errors.New("authentication required")
					}
				}
				if err := visit(node.SelectionSet, depth+1, stack); err != nil {
					return err
				}
			case *ast.InlineFragment:
				if err := visit(node.SelectionSet, depth, stack); err != nil {
					return err
				}
			case *ast.FragmentSpread:
				name := node.Name.Value
				if stack[name] {
					return errors.New("cyclic GraphQL fragment")
				}
				fragment := fragments[name]
				if fragment == nil {
					return fmt.Errorf("unknown fragment %s", name)
				}
				stack[name] = true
				if err := visit(fragment.SelectionSet, depth, stack); err != nil {
					return err
				}
				delete(stack, name)
			}
		}
		return nil
	}
	return visit(op.SelectionSet, 1, map[string]bool{})
}
