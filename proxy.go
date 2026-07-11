package ddmcproxy

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	appName = "ddmcproxy"
)

// Proxy is a multi-org MCP proxy in front of the Datadog MCP server.
//
// It exposes the upstream Datadog tools over stdio, injecting a required "org"
// argument into each tool. On a tool call the org is used to pick the matching
// API/APP keys, and the call is forwarded to the Datadog MCP server.
type Proxy struct {
	config  *Config
	version string

	mu       sync.Mutex
	sessions map[string]*mcp.ClientSession
}

// NewProxy creates a Proxy from the given config.
func NewProxy(config *Config, version string) *Proxy {
	return &Proxy{
		config:   config,
		version:  version,
		sessions: map[string]*mcp.ClientSession{},
	}
}

// Run connects to the upstream server, mirrors its tools and serves the proxy
// over stdio until the client disconnects or ctx is cancelled.
func (proxy *Proxy) Run(ctx context.Context) error {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    appName,
		Version: proxy.version,
	}, nil)

	// Discover the upstream tools using the first configured org. Every org is
	// assumed to expose the same set of Datadog tools.
	primary := proxy.config.Orgs[0].Name
	session, err := proxy.session(ctx, primary)

	if err != nil {
		return fmt.Errorf("failed to connect to the upstream server as org '%s': %w", primary, err)
	}

	tools, err := listTools(ctx, session)

	if err != nil {
		return fmt.Errorf("failed to list upstream tools: %w", err)
	}

	orgNames := proxy.config.OrgNames()

	for _, tool := range tools {
		wrapped, handler, err := proxy.wrapTool(tool, orgNames)

		if err != nil {
			return fmt.Errorf("failed to wrap tool '%s': %w", tool.Name, err)
		}

		server.AddTool(wrapped, handler)
	}

	log.Printf("[%s] serving %d Datadog tools for %d orgs: %v", appName, len(tools), len(orgNames), orgNames)

	return server.Run(ctx, &mcp.StdioTransport{})
}

// session returns a connected upstream session for the org, creating and
// caching it on first use.
func (proxy *Proxy) session(ctx context.Context, org string) (*mcp.ClientSession, error) {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()

	if session, ok := proxy.sessions[org]; ok {
		return session, nil
	}

	orgConfig := proxy.config.Org(org)

	if orgConfig == nil {
		return nil, fmt.Errorf("unknown org: %s", org)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    appName,
		Version: proxy.version,
	}, nil)

	transport := &mcp.StreamableClientTransport{
		Endpoint: orgConfig.Endpoint,
		HTTPClient: &http.Client{
			Transport: &headerTransport{
				base: http.DefaultTransport,
				headers: map[string]string{
					"DD_API_KEY":         orgConfig.APIKey,
					"DD_APPLICATION_KEY": orgConfig.AppKey,
				},
			},
		},
	}

	session, err := client.Connect(ctx, transport, nil)

	if err != nil {
		return nil, err
	}

	proxy.sessions[org] = session

	return session, nil
}

// listTools collects every tool from the upstream server, following pagination.
func listTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool

	params := &mcp.ListToolsParams{}

	for {
		result, err := session.ListTools(ctx, params)

		if err != nil {
			return nil, err
		}

		tools = append(tools, result.Tools...)

		if result.NextCursor == "" {
			break
		}

		params.Cursor = result.NextCursor
	}

	return tools, nil
}

// headerTransport injects fixed headers (the Datadog credentials) into every
// outgoing request.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (transport *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())

	for key, value := range transport.headers {
		req.Header.Set(key, value)
	}

	return transport.base.RoundTrip(req)
}
