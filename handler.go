package ddmcproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// orgArg is the name of the argument injected into every proxied tool to select
// the target Datadog organization.
const orgArg = "org"

// wrapTool returns a copy of the upstream tool with the "org" argument injected,
// together with a handler that forwards the call to the org's Datadog MCP server.
func (proxy *Proxy) wrapTool(tool *mcp.Tool, orgNames []string) (*mcp.Tool, mcp.ToolHandler, error) {
	schema, err := injectOrgArg(tool.InputSchema, orgNames)

	if err != nil {
		return nil, nil, err
	}

	wrapped := *tool
	wrapped.InputSchema = schema
	// OutputSchema is passed through unchanged from the upstream tool.

	toolName := tool.Name

	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := map[string]any{}

		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return errorResult("failed to parse arguments: %s", err), nil
			}
		}

		org, ok := args[orgArg].(string)

		if !ok || org == "" {
			return errorResult("missing required argument '%s'; must be one of: %s", orgArg, strings.Join(orgNames, ", ")), nil
		}

		delete(args, orgArg)

		session, err := proxy.session(ctx, org)

		if err != nil {
			return errorResult("%s (available orgs: %s)", err, strings.Join(orgNames, ", ")), nil
		}

		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		})

		if err != nil {
			// The upstream session may be broken; drop it so the next call reconnects.
			proxy.dropSession(org)
			return errorResult("failed to call '%s' for org '%s': %s", toolName, org, err), nil
		}

		return result, nil
	}

	return &wrapped, handler, nil
}

// injectOrgArg returns a copy of the given JSON schema with a required "org"
// string property (enumerated over orgNames) added.
func injectOrgArg(inputSchema any, orgNames []string) (map[string]any, error) {
	schema := map[string]any{}

	// InputSchema arrives as a map[string]any from the upstream client, but
	// round-trip through JSON to get an independent, mutable copy regardless of
	// the concrete type.
	if inputSchema != nil {
		buf, err := json.Marshal(inputSchema)

		if err != nil {
			return nil, err
		}

		if err := json.Unmarshal(buf, &schema); err != nil {
			return nil, err
		}
	}

	if schema["type"] == nil {
		schema["type"] = "object"
	}

	properties, ok := schema["properties"].(map[string]any)

	if !ok {
		properties = map[string]any{}
		schema["properties"] = properties
	}

	enum := make([]any, len(orgNames))

	for i, name := range orgNames {
		enum[i] = name
	}

	properties[orgArg] = map[string]any{
		"type":        "string",
		"enum":        enum,
		"description": "The Datadog organization to run this tool against. One of: " + strings.Join(orgNames, ", ") + ".",
	}

	schema["required"] = prependRequired(schema["required"], orgArg)

	return schema, nil
}

// prependRequired adds name to the front of a JSON schema "required" list,
// avoiding duplicates.
func prependRequired(existing any, name string) []any {
	required := []any{name}

	if list, ok := existing.([]any); ok {
		for _, item := range list {
			if item != name {
				required = append(required, item)
			}
		}
	}

	return required
}

// dropSession removes a cached upstream session so the next call reconnects.
func (proxy *Proxy) dropSession(org string) {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	delete(proxy.sessions, org)
}

func errorResult(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf(format, args...)},
		},
	}
}
