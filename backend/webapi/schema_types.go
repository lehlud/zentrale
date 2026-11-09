package webapi

import (
	"reflect"
	"strings"
	"time"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/domain"
)

var resourceTypes = map[string]reflect.Type{
	"machine":        reflect.TypeFor[domain.Machine](),
	"pool":           reflect.TypeFor[domain.Pool](),
	"project":        reflect.TypeFor[domain.Project](),
	"source":         reflect.TypeFor[domain.Source](),
	"provider":       reflect.TypeFor[domain.Provider](),
	"languageModel":  reflect.TypeFor[domain.LanguageModel](),
	"modelChoice":    reflect.TypeFor[domain.ModelChoice](),
	"modelDefaults":  reflect.TypeFor[domain.ModelDefaults](),
	"commitIdentity": reflect.TypeFor[domain.CommitIdentity](),
	"providerLogin":  reflect.TypeFor[domain.ProviderLogin](),
	"agent":          reflect.TypeFor[domain.Agent](),
	"agent_input":    reflect.TypeFor[domain.AgentInput](),
	"goal_review":    reflect.TypeFor[domain.GoalReview](),
	"message":        reflect.TypeFor[domain.Message](),
	"note":           reflect.TypeFor[domain.Note](),
	"question":       reflect.TypeFor[domain.Question](),
	"approval":       reflect.TypeFor[domain.Approval](),
	"usage":          reflect.TypeFor[domain.Usage](),
	"job":            reflect.TypeFor[domain.Job](),
}

// The API and generated TypeScript types share the domain field definitions.
// Credential fields are deliberately input-only and never enter the output schema.
type schemaBuilder struct {
	server  *Server
	objects map[reflect.Type]*graphql.Object
	inputs  map[reflect.Type]*graphql.InputObject
	ssh     *graphql.Object
}

func newSchemaBuilder(server *Server) *schemaBuilder {
	return &schemaBuilder{
		server:  server,
		objects: make(map[reflect.Type]*graphql.Object),
		inputs:  make(map[reflect.Type]*graphql.InputObject),
	}
}

func scalarType(goType reflect.Type) graphql.Type {
	switch goType.Kind() {
	case reflect.String:
		return graphql.String
	case reflect.Bool:
		return graphql.Boolean
	case reflect.Int, reflect.Int64:
		return graphql.Int
	case reflect.Float64:
		return graphql.Float
	}

	if goType == reflect.TypeFor[time.Time]() {
		return graphql.String
	}

	return nil
}

func (builder *schemaBuilder) output(goType reflect.Type) graphql.Output {
	if scalar := scalarType(goType); scalar != nil {
		return scalar.(graphql.Output)
	}

	if goType.Kind() == reflect.Slice {
		return graphql.NewList(graphql.NewNonNull(builder.output(goType.Elem())))
	}

	if cached := builder.objects[goType]; cached != nil {
		return cached
	}

	fields := graphql.Fields{}
	object := graphql.NewObject(graphql.ObjectConfig{Name: goType.Name(), Fields: fields})
	builder.objects[goType] = object

	for index := 0; index < goType.NumField(); index++ {
		field := goType.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" || name == "credential" || name == "oauth" {
			continue
		}

		fields[name] = &graphql.Field{
			Type:    graphql.NewNonNull(builder.output(field.Type)),
			Resolve: resolveDomainField(name, field.Type),
		}
	}

	if goType == reflect.TypeFor[domain.Message]() {
		fields["activities"] = &graphql.Field{
			Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(builder.output(reflect.TypeFor[domain.ToolActivity]())))),
			Resolve: func(params graphql.ResolveParams) (any, error) {
				if message, ok := params.Source.(map[string]any); ok && message["activities"] != nil {
					return message["activities"], nil
				}

				return []any{}, nil
			},
		}

		fields["changes"] = &graphql.Field{
			Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(builder.output(reflect.TypeFor[domain.FileChange]())))),
			Resolve: func(params graphql.ResolveParams) (any, error) {
				if message, ok := params.Source.(map[string]any); ok && message["changes"] != nil {
					return message["changes"], nil
				}

				return []any{}, nil
			},
		}

		fields["change"] = &graphql.Field{
			Type: builder.output(reflect.TypeFor[domain.FileChange]()),
			Resolve: func(params graphql.ResolveParams) (any, error) {
				if message, ok := params.Source.(map[string]any); ok {
					return message["change"], nil
				}

				return nil, nil
			},
		}
	}

	return object
}

func resolveDomainField(name string, goType reflect.Type) graphql.FieldResolveFn {
	return func(params graphql.ResolveParams) (any, error) {
		if resource, ok := params.Source.(map[string]any); ok && resource[name] != nil {
			return resource[name], nil
		}

		// Nil slices and omitted optional strings have stable non-null API defaults.
		switch goType.Kind() {
		case reflect.Slice:
			return []any{}, nil
		case reflect.Bool:
			return false, nil
		case reflect.Int, reflect.Int64, reflect.Float64:
			return 0, nil
		default:
			return "", nil
		}
	}
}

func (builder *schemaBuilder) input(goType reflect.Type) graphql.Input {
	if scalar := scalarType(goType); scalar != nil {
		return scalar.(graphql.Input)
	}

	if goType.Kind() == reflect.Slice {
		return graphql.NewList(graphql.NewNonNull(builder.input(goType.Elem())))
	}

	if cached := builder.inputs[goType]; cached != nil {
		return cached
	}

	fields := graphql.InputObjectConfigFieldMap{}
	object := graphql.NewInputObject(graphql.InputObjectConfig{Name: goType.Name() + "Input", Fields: fields})
	builder.inputs[goType] = object

	for index := 0; index < goType.NumField(); index++ {
		field := goType.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" &&
			name != "-" &&
			name != "oauth" &&
			name != "catalogStatus" &&
			name != "catalogError" &&
			name != "modelCount" &&
			name != "connected" &&
			name != "accountLabel" {
			fields[name] = &graphql.InputObjectFieldConfig{Type: builder.input(field.Type)}
		}
	}

	return object
}

func (builder *schemaBuilder) sshKeyType() *graphql.Object {
	if builder.ssh == nil {
		builder.ssh = sshKeyType()
	}

	return builder.ssh
}
