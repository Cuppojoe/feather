package openapi

import (
	"reflect"
	"testing"
)

func TestGenerateExamplePrimitivesAndPrecedence(t *testing.T) {
	schemas := map[string]*Schema{}

	if got := GenerateExample(&Schema{Type: "string"}, schemas, true); got != "" {
		t.Fatalf("string default: got %#v", got)
	}
	if got := GenerateExample(&Schema{Type: "integer"}, schemas, true); got != 0 {
		t.Fatalf("integer default: got %#v", got)
	}
	if got := GenerateExample(&Schema{Type: "boolean"}, schemas, true); got != false {
		t.Fatalf("boolean default: got %#v", got)
	}
	// Explicit example wins over type-derived value.
	if got := GenerateExample(&Schema{Type: "string", Example: "hi"}, schemas, true); got != "hi" {
		t.Fatalf("example precedence: got %#v", got)
	}
	// Default wins when no example.
	if got := GenerateExample(&Schema{Type: "integer", Default: 7}, schemas, true); got != 7 {
		t.Fatalf("default precedence: got %#v", got)
	}
	// Enum first when no example/default.
	if got := GenerateExample(&Schema{Type: "string", Enum: []any{"a", "b"}}, schemas, true); got != "a" {
		t.Fatalf("enum precedence: got %#v", got)
	}
}

func TestGenerateExampleStringFormats(t *testing.T) {
	cases := map[string]string{
		"date-time": "2024-01-01T00:00:00Z",
		"date":      "2024-01-01",
		"uuid":      "00000000-0000-0000-0000-000000000000",
		"email":     "user@example.com",
	}
	for format, want := range cases {
		if got := GenerateExample(&Schema{Type: "string", Format: format}, nil, true); got != want {
			t.Errorf("format %s: got %#v want %q", format, got, want)
		}
	}
}

func TestGenerateExampleObjectSkipsReadOnly(t *testing.T) {
	s := &Schema{
		Type: "object",
		Properties: map[string]*Schema{
			"name": {Type: "string"},
			"id":   {Type: "string", ReadOnly: true},
		},
	}
	// forRequest=true skips read-only.
	got := GenerateExample(s, nil, true).(map[string]any)
	if _, ok := got["id"]; ok {
		t.Fatalf("read-only field should be skipped for request: %#v", got)
	}
	if _, ok := got["name"]; !ok {
		t.Fatalf("name should be present: %#v", got)
	}
	// forRequest=false keeps it.
	got2 := GenerateExample(s, nil, false).(map[string]any)
	if _, ok := got2["id"]; !ok {
		t.Fatalf("read-only field should be present for response: %#v", got2)
	}
}

func TestGenerateExampleArray(t *testing.T) {
	s := &Schema{Type: "array", Items: &Schema{Type: "string"}}
	got := GenerateExample(s, nil, true)
	want := []any{""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("array: got %#v want %#v", got, want)
	}

	empty := GenerateExample(&Schema{Type: "array"}, nil, true)
	if !reflect.DeepEqual(empty, []any{}) {
		t.Fatalf("array no items: got %#v", empty)
	}
}

func TestGenerateExampleRefResolution(t *testing.T) {
	schemas := map[string]*Schema{
		"Item": {
			Type: "object",
			Properties: map[string]*Schema{
				"name": {Type: "string"},
			},
		},
	}
	s := &Schema{Ref: "#/components/schemas/Item"}
	got, ok := GenerateExample(s, schemas, true).(map[string]any)
	if !ok {
		t.Fatalf("ref should resolve to object: %#v", got)
	}
	if _, ok := got["name"]; !ok {
		t.Fatalf("resolved object missing field: %#v", got)
	}
}

func TestGenerateExampleRefCycle(t *testing.T) {
	// Node references itself via children: [Node]; must terminate.
	schemas := map[string]*Schema{
		"Node": {
			Type: "object",
			Properties: map[string]*Schema{
				"value":    {Type: "string"},
				"children": {Type: "array", Items: &Schema{Ref: "#/components/schemas/Node"}},
			},
		},
	}
	got := GenerateExample(&Schema{Ref: "#/components/schemas/Node"}, schemas, true).(map[string]any)
	children := got["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("expected one child element, got %#v", children)
	}
	// The cycle is broken: the nested Node resolves to nil.
	if children[0] != nil {
		t.Fatalf("cycle should break to nil, got %#v", children[0])
	}
}

func TestGenerateExampleAllOfMerge(t *testing.T) {
	schemas := map[string]*Schema{
		"A": {Type: "object", Properties: map[string]*Schema{"a": {Type: "string"}}},
		"B": {Type: "object", Properties: map[string]*Schema{"b": {Type: "integer"}}},
	}
	s := &Schema{AllOf: []*Schema{
		{Ref: "#/components/schemas/A"},
		{Ref: "#/components/schemas/B"},
	}}
	got := GenerateExample(s, schemas, true).(map[string]any)
	if _, ok := got["a"]; !ok {
		t.Fatalf("allOf missing key a: %#v", got)
	}
	if _, ok := got["b"]; !ok {
		t.Fatalf("allOf missing key b: %#v", got)
	}
}

func TestGenerateExampleOneOfAnyOfFirst(t *testing.T) {
	one := &Schema{OneOf: []*Schema{{Type: "string", Example: "first"}, {Type: "integer"}}}
	if got := GenerateExample(one, nil, true); got != "first" {
		t.Fatalf("oneOf first: got %#v", got)
	}
	any_ := &Schema{AnyOf: []*Schema{{Type: "boolean", Default: true}, {Type: "string"}}}
	if got := GenerateExample(any_, nil, true); got != true {
		t.Fatalf("anyOf first: got %#v", got)
	}
}

func TestGenerateExampleJSON(t *testing.T) {
	s := &Schema{Type: "object", Properties: map[string]*Schema{"name": {Type: "string"}}}
	out := GenerateExampleJSON(s, nil)
	if out == "" {
		t.Fatal("expected non-empty JSON")
	}
	if GenerateExampleJSON(nil, nil) != "" {
		t.Fatal("nil schema should give empty string")
	}
}

func TestExampleForParam(t *testing.T) {
	if got := ExampleForParam(Parameter{Name: "x", Example: "v"}, nil); got != "v" {
		t.Fatalf("param example: got %q", got)
	}
	if got := ExampleForParam(Parameter{Name: "x", Schema: &Schema{Default: 5}}, nil); got != "5" {
		t.Fatalf("param schema default: got %q", got)
	}
	if got := ExampleForParam(Parameter{Name: "x", Schema: &Schema{Enum: []any{"e"}}}, nil); got != "e" {
		t.Fatalf("param enum: got %q", got)
	}
	if got := ExampleForParam(Parameter{Name: "x"}, nil); got != "" {
		t.Fatalf("param no info: got %q", got)
	}
}
