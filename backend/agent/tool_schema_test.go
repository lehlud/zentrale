package agent

import (
	"encoding/json"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestToolSchemasSerializeRequiredAsArrays(t *testing.T) {
	for _, contextWindow := range []int{4096, 200000} {
		for _, tool := range toolsForModel(domain.LanguageModel{ContextWindow: contextWindow}) {
			t.Run(tool.Name, func(t *testing.T) {
				encoded, err := json.Marshal(tool.Schema)
				if err != nil {
					t.Fatal(err)
				}

				var schema any
				if err := json.Unmarshal(encoded, &schema); err != nil {
					t.Fatal(err)
				}

				var validate func(any)
				validate = func(value any) {
					switch value := value.(type) {
					case map[string]any:
						if value["type"] == "object" {
							if required, exists := value["required"]; exists {
								fields, valid := required.([]any)
								if !valid {
									t.Fatalf("required must serialize as an array, got %v in %s", required, encoded)
								}

								properties, _ := value["properties"].(map[string]any)

								for _, field := range fields {
									name, valid := field.(string)
									if !valid || properties[name] == nil {
										t.Fatalf("invalid required field: %v", field)
									}
								}
							}
						}

						for _, child := range value {
							validate(child)
						}
					case []any:
						for _, child := range value {
							validate(child)
						}
					}
				}

				validate(schema)
			})
		}
	}
}
