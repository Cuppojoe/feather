package openapi

import (
	"encoding/json"
	"fmt"
)

// GenerateExample builds a Go skeleton value for a schema, resolving $refs
// against the provided schema map. When forRequest is true, read-only
// properties are omitted (they belong only in responses).
//
// Precedence at each node: an explicit Example wins, then Default, then the
// first Enum value, then a value synthesized from the schema's type/shape.
func GenerateExample(s *Schema, schemas map[string]*Schema, forRequest bool) any {
	return genExample(s, schemas, forRequest, map[string]bool{})
}

// GenerateExampleJSON returns an indented-JSON skeleton suitable for prefilling
// a request body. It returns "" when there is no schema or marshalling fails.
func GenerateExampleJSON(s *Schema, schemas map[string]*Schema) string {
	if s == nil {
		return ""
	}
	v := GenerateExample(s, schemas, true)
	if v == nil {
		return ""
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// ExampleForParam returns a string prefill value for a parameter, or "" when no
// example/default is known (meaning: leave the field blank rather than inject a
// noisy placeholder).
func ExampleForParam(p Parameter, schemas map[string]*Schema) string {
	if p.Example != nil {
		return scalarString(p.Example)
	}
	if p.Schema != nil {
		if p.Schema.Example != nil {
			return scalarString(p.Schema.Example)
		}
		if p.Schema.Default != nil {
			return scalarString(p.Schema.Default)
		}
		if len(p.Schema.Enum) > 0 {
			return scalarString(p.Schema.Enum[0])
		}
	}
	return ""
}

// genExample is the recursive worker. visited tracks resolved $ref component
// names along the current branch to break cycles.
func genExample(s *Schema, schemas map[string]*Schema, forRequest bool, visited map[string]bool) any {
	if s == nil {
		return nil
	}

	// Resolve $ref against the component map, guarding against cycles.
	if s.Ref != "" {
		name := extractRefName(s.Ref)
		if name == "" || visited[name] {
			return nil
		}
		target := schemas[name]
		if target == nil {
			return nil
		}
		// Clone visited for this branch so sibling refs aren't poisoned.
		next := make(map[string]bool, len(visited)+1)
		for k := range visited {
			next[k] = true
		}
		next[name] = true
		return genExample(target, schemas, forRequest, next)
	}

	if s.Example != nil {
		return s.Example
	}
	if s.Default != nil {
		return s.Default
	}
	if len(s.Enum) > 0 {
		return s.Enum[0]
	}

	// allOf: merge object subschemas into one map.
	if len(s.AllOf) > 0 {
		merged := map[string]any{}
		for _, sub := range s.AllOf {
			if m, ok := genExample(sub, schemas, forRequest, visited).(map[string]any); ok {
				for k, v := range m {
					merged[k] = v
				}
			}
		}
		// Some allOf members may carry their own properties directly.
		if len(s.Properties) > 0 {
			for k, v := range objectExample(s, schemas, forRequest, visited) {
				merged[k] = v
			}
		}
		return merged
	}

	// oneOf / anyOf: take the first option.
	if len(s.OneOf) > 0 {
		return genExample(s.OneOf[0], schemas, forRequest, visited)
	}
	if len(s.AnyOf) > 0 {
		return genExample(s.AnyOf[0], schemas, forRequest, visited)
	}

	switch s.Type {
	case "object":
		return objectExample(s, schemas, forRequest, visited)
	case "array":
		if s.Items == nil {
			return []any{}
		}
		return []any{genExample(s.Items, schemas, forRequest, visited)}
	case "string":
		return stringExample(s.Format)
	case "integer", "number":
		return 0
	case "boolean":
		return false
	default:
		// Untyped schema with properties → treat as object.
		if len(s.Properties) > 0 || s.AdditionalProperties != nil {
			return objectExample(s, schemas, forRequest, visited)
		}
		return nil
	}
}

// objectExample builds a map for an object schema, recursing into properties
// and skipping read-only fields for request bodies.
func objectExample(s *Schema, schemas map[string]*Schema, forRequest bool, visited map[string]bool) map[string]any {
	out := map[string]any{}
	for name, prop := range s.Properties {
		if forRequest && prop != nil && prop.ReadOnly {
			continue
		}
		out[name] = genExample(prop, schemas, forRequest, visited)
	}
	// If there are no concrete properties but a value schema is declared,
	// emit a single sample key so the shape is visible.
	if len(out) == 0 && s.AdditionalProperties != nil {
		out["key"] = genExample(s.AdditionalProperties, schemas, forRequest, visited)
	}
	return out
}

// stringExample returns a representative string for a given OpenAPI format.
func stringExample(format string) string {
	switch format {
	case "date-time":
		return "2024-01-01T00:00:00Z"
	case "date":
		return "2024-01-01"
	case "uuid":
		return "00000000-0000-0000-0000-000000000000"
	case "email":
		return "user@example.com"
	default:
		return ""
	}
}

// scalarString renders a scalar example value as a string for param prefill.
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}
