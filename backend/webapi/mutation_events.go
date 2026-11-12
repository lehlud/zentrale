package webapi

import (
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/source"
)

// Select the actual operation, not a string match or an unselected mutation.
func isMutationOperation(query, operationName string) bool {
	document, err := parser.Parse(parser.ParseParams{Source: source.NewSource(&source.Source{Body: []byte(query)})})
	if err != nil {
		return false
	}
	for _, definition := range document.Definitions {
		operation, ok := definition.(*ast.OperationDefinition)
		if !ok {
			continue
		}
		if operationName != "" && (operation.Name == nil || operation.Name.Value != operationName) {
			continue
		}
		return operation.Operation == "mutation"
	}
	return false
}
