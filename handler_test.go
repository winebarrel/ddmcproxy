package ddmcproxy

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListOrgsTool(t *testing.T) {
	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "foo", Token: "secret", Endpoint: "https://example.com/foo"},
		{Name: "bar", APIKey: "a", AppKey: "b", Endpoint: "https://example.com/bar"},
	}}, "test")

	tool, handler := proxy.listOrgsTool()

	assert.Equal(t, "list_orgs", tool.Name)

	res, err := handler(context.Background(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}})
	require.NoError(t, err)

	body := res.Content[0].(*mcp.TextContent).Text

	// Org names and endpoints are listed; credentials never are.
	for _, want := range []string{"foo", "bar", "https://example.com/foo"} {
		assert.Contains(t, body, want)
	}

	for _, secret := range []string{"secret", `"a"`, `"b"`} {
		assert.NotContains(t, body, secret)
	}
}

func TestInjectOrgArgNonObject(t *testing.T) {
	// A schema that marshals to a JSON array cannot be unmarshaled back into a
	// map, so injectOrgArg must return an error rather than panic.
	_, err := injectOrgArg([]any{"not", "an", "object"}, []string{"org1"})
	assert.Error(t, err)
}

func TestInjectOrgArg(t *testing.T) {
	upstream := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []any{"query"},
	}

	schema, err := injectOrgArg(upstream, []string{"org1", "org2"})
	require.NoError(t, err)

	props := schema["properties"].(map[string]any)
	assert.Contains(t, props, "query", "original property 'query' was dropped")

	org, ok := props[orgArg].(map[string]any)
	require.True(t, ok, "org property was not added")

	assert.Equal(t, "string", org["type"])
	assert.Equal(t, []any{"org1", "org2"}, org["enum"])
	assert.Equal(t, []any{orgArg, "query"}, schema["required"])

	// The upstream schema must not be mutated.
	upstreamProps := upstream["properties"].(map[string]any)
	assert.NotContains(t, upstreamProps, orgArg, "upstream schema was mutated")
}

func TestInjectOrgArgEmptySchema(t *testing.T) {
	schema, err := injectOrgArg(nil, []string{"org1"})
	require.NoError(t, err)

	assert.Equal(t, "object", schema["type"])
	assert.Equal(t, []any{orgArg}, schema["required"])
}

func TestInjectOrgArgRawMessage(t *testing.T) {
	// Simulate an InputSchema that arrives as json.RawMessage.
	raw := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)

	schema, err := injectOrgArg(raw, []string{"org1"})
	require.NoError(t, err)

	props := schema["properties"].(map[string]any)
	assert.Contains(t, props, orgArg, "org property was not added")
	assert.Contains(t, props, "q", "original property 'q' was dropped")
}
