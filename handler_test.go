package ddmcproxy

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestInjectOrgArg(t *testing.T) {
	upstream := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []any{"query"},
	}

	schema, err := injectOrgArg(upstream, []string{"org1", "org2"})

	if err != nil {
		t.Fatal(err)
	}

	props := schema["properties"].(map[string]any)

	if _, ok := props["query"]; !ok {
		t.Error("original property 'query' was dropped")
	}

	org, ok := props[orgArg].(map[string]any)

	if !ok {
		t.Fatal("org property was not added")
	}

	if org["type"] != "string" {
		t.Errorf("org type = %v, want string", org["type"])
	}

	if !reflect.DeepEqual(org["enum"], []any{"org1", "org2"}) {
		t.Errorf("org enum = %v, want [org1 org2]", org["enum"])
	}

	if !reflect.DeepEqual(schema["required"], []any{orgArg, "query"}) {
		t.Errorf("required = %v, want [org query]", schema["required"])
	}

	// The upstream schema must not be mutated.
	upstreamProps := upstream["properties"].(map[string]any)

	if _, ok := upstreamProps[orgArg]; ok {
		t.Error("upstream schema was mutated")
	}
}

func TestInjectOrgArgEmptySchema(t *testing.T) {
	schema, err := injectOrgArg(nil, []string{"org1"})

	if err != nil {
		t.Fatal(err)
	}

	if schema["type"] != "object" {
		t.Errorf("type = %v, want object", schema["type"])
	}

	if !reflect.DeepEqual(schema["required"], []any{orgArg}) {
		t.Errorf("required = %v, want [org]", schema["required"])
	}
}

func TestInjectOrgArgRawMessage(t *testing.T) {
	// Simulate an InputSchema that arrives as json.RawMessage.
	raw := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)

	schema, err := injectOrgArg(raw, []string{"org1"})

	if err != nil {
		t.Fatal(err)
	}

	props := schema["properties"].(map[string]any)

	if _, ok := props[orgArg]; !ok {
		t.Error("org property was not added")
	}

	if _, ok := props["q"]; !ok {
		t.Error("original property 'q' was dropped")
	}
}
