package ddmcproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingUpstream is a real MCP server, served over the streamable HTTP
// transport, that records the auth headers of the requests it receives.
type capturingUpstream struct {
	*httptest.Server

	mu     sync.Mutex
	apiKey string
	appKey string
	auth   string
}

func (up *capturingUpstream) headers() (apiKey, appKey, auth string) {
	up.mu.Lock()
	defer up.mu.Unlock()
	return up.apiKey, up.appKey, up.auth
}

func newCapturingUpstream(t *testing.T) *capturingUpstream {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "upstream", Version: "0"}, nil)

	// A single "echo" tool that returns the raw arguments it received, so tests
	// can assert the proxy forwarded the call (minus the org argument).
	server.AddTool(
		&mcp.Tool{
			Name:        "echo",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"msg": map[string]any{"type": "string"}}},
		},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: string(req.Params.Arguments)}},
			}, nil
		},
	)

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	up := &capturingUpstream{}
	up.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up.mu.Lock()
		// Only record non-empty values so background keepalive requests carrying
		// the same headers do not clobber what we captured.
		if v := r.Header.Get("DD_API_KEY"); v != "" {
			up.apiKey = v
		}
		if v := r.Header.Get("DD_APPLICATION_KEY"); v != "" {
			up.appKey = v
		}
		if v := r.Header.Get("Authorization"); v != "" {
			up.auth = v
		}
		up.mu.Unlock()

		handler.ServeHTTP(w, r)
	}))

	t.Cleanup(up.Close)

	return up
}

func TestSession(t *testing.T) {
	keysUp := newCapturingUpstream(t)
	tokenUp := newCapturingUpstream(t)

	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "keys", APIKey: "api-secret", AppKey: "app-secret", Endpoint: keysUp.URL},
		{Name: "tok", Token: "ddsat_secret", Endpoint: tokenUp.URL},
	}}, "test")
	defer proxy.closeSessions()

	ctx := context.Background()

	// Connecting attaches the legacy key headers.
	keys1, err := proxy.session(ctx, "keys")
	require.NoError(t, err)

	apiKey, appKey, _ := keysUp.headers()
	assert.Equal(t, "api-secret", apiKey)
	assert.Equal(t, "app-secret", appKey)

	// The same org returns the cached session without reconnecting.
	keys2, err := proxy.session(ctx, "keys")
	require.NoError(t, err)
	assert.Same(t, keys1, keys2, "session was not cached")

	// A token org attaches an Authorization: Bearer header.
	_, err = proxy.session(ctx, "tok")
	require.NoError(t, err)

	_, _, auth := tokenUp.headers()
	assert.Equal(t, "Bearer ddsat_secret", auth)

	// An unknown org is an error.
	_, err = proxy.session(ctx, "nope")
	assert.Error(t, err)

	// dropSession evicts the session so the next call reconnects.
	proxy.dropSession("keys")

	keys3, err := proxy.session(ctx, "keys")
	require.NoError(t, err)
	assert.NotSame(t, keys1, keys3, "dropSession did not force a reconnect")
}

func TestSessionConnectError(t *testing.T) {
	// Port 0 is not dialable, so connecting fails.
	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "org1", Token: "x", Endpoint: "http://127.0.0.1:0"},
	}}, "test")

	_, err := proxy.session(context.Background(), "org1")
	assert.Error(t, err)
}

func TestListTools(t *testing.T) {
	up := newCapturingUpstream(t)

	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "org1", Token: "x", Endpoint: up.URL},
	}}, "test")
	defer proxy.closeSessions()

	session, err := proxy.session(context.Background(), "org1")
	require.NoError(t, err)

	tools, err := listTools(context.Background(), session)
	require.NoError(t, err)

	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}

	assert.Contains(t, names, "echo")
}

func TestListToolsError(t *testing.T) {
	up := newCapturingUpstream(t)

	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "org1", Token: "x", Endpoint: up.URL},
	}}, "test")
	defer proxy.closeSessions()

	session, err := proxy.session(context.Background(), "org1")
	require.NoError(t, err)

	// A canceled context makes the upstream ListTools call fail.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = listTools(ctx, session)
	assert.Error(t, err)
}

func TestListToolsPagination(t *testing.T) {
	// PageSize 1 with three tools forces listTools to follow the cursor.
	server := mcp.NewServer(&mcp.Implementation{Name: "upstream", Version: "0"}, &mcp.ServerOptions{PageSize: 1})

	for _, name := range []string{"a", "b", "c"} {
		server.AddTool(
			&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			},
		)
	}

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "org1", Token: "x", Endpoint: ts.URL},
	}}, "test")
	defer proxy.closeSessions()

	session, err := proxy.session(context.Background(), "org1")
	require.NoError(t, err)

	tools, err := listTools(context.Background(), session)
	require.NoError(t, err)

	assert.Len(t, tools, 3, "listTools should follow the pagination cursor")
}

func TestSessionConcurrent(t *testing.T) {
	up := newCapturingUpstream(t)

	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "org1", Token: "x", Endpoint: up.URL},
	}}, "test")
	defer proxy.closeSessions()

	const n = 8

	var wg sync.WaitGroup

	sessions := make([]*mcp.ClientSession, n)
	errs := make([]error, n)
	start := make(chan struct{})

	for i := range n {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()
			<-start
			sessions[i], errs[i] = proxy.session(context.Background(), "org1")
		}(i)
	}

	close(start)
	wg.Wait()

	// Every concurrent caller must end up with the same cached session, even
	// though several may have dialed at once.
	for i := range n {
		require.NoError(t, errs[i])
		assert.Same(t, sessions[0], sessions[i], "caching is not race-safe")
	}
}

func TestWrapToolSchemaError(t *testing.T) {
	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "org1", Token: "x", Endpoint: "http://127.0.0.1:0"},
	}}, "test")

	// A non-object InputSchema cannot have the org argument injected.
	_, _, err := proxy.wrapTool(&mcp.Tool{Name: "bad", InputSchema: []any{"nope"}}, []string{"org1"})
	assert.Error(t, err)
}

func TestWrapTool(t *testing.T) {
	up := newCapturingUpstream(t)

	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "org1", Token: "x", Endpoint: up.URL},
	}}, "test")
	defer proxy.closeSessions()

	orgNames := []string{"org1"}

	wrapped, handler, err := proxy.wrapTool(
		&mcp.Tool{Name: "echo", InputSchema: map[string]any{"type": "object"}},
		orgNames,
	)
	require.NoError(t, err)

	// The wrapped schema gains the injected org argument.
	props := wrapped.InputSchema.(map[string]any)["properties"].(map[string]any)
	assert.Contains(t, props, orgArg, "org argument was not injected into the wrapped schema")

	call := func(args string) *mcp.CallToolResult {
		t.Helper()
		res, err := handler(context.Background(), &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(args)},
		})
		require.NoError(t, err)

		return res
	}

	// Happy path: the call is forwarded with the org argument stripped.
	res := call(`{"org":"org1","msg":"hi"}`)
	require.False(t, res.IsError, "unexpected error result: %+v", res.Content)

	forwarded := res.Content[0].(*mcp.TextContent).Text
	assert.NotContains(t, forwarded, orgArg, "org argument was not stripped before forwarding")
	assert.Contains(t, forwarded, "hi", "argument was not forwarded")

	// Error paths.
	assert.True(t, call(`{"msg":"hi"}`).IsError, "expected an error when org is missing")
	assert.True(t, call(``).IsError, "expected an error when arguments are empty (no org)")
	assert.True(t, call(`{"org":"nope"}`).IsError, "expected an error for an unknown org")
	assert.True(t, call(`{bad json`).IsError, "expected an error for malformed arguments")

	// A call to a tool the upstream does not expose fails and is reported.
	_, missingHandler, err := proxy.wrapTool(
		&mcp.Tool{Name: "missing", InputSchema: map[string]any{"type": "object"}},
		orgNames,
	)
	require.NoError(t, err)

	res, err = missingHandler(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{"org":"org1"}`)},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError, "expected an error when the upstream tool call fails")
}

func TestBuildServer(t *testing.T) {
	up := newCapturingUpstream(t)

	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "org1", Token: "x", Endpoint: up.URL},
	}}, "test")
	defer proxy.closeSessions()

	server, err := proxy.buildServer(context.Background())
	require.NoError(t, err)

	// Connect a client to the built server over an in-memory transport and
	// confirm it mirrors the upstream tools plus the proxy-native list_orgs.
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	require.NoError(t, err)
	defer func() { assert.NoError(t, serverSession.Close()) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)

	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	require.NoError(t, err)
	defer func() { assert.NoError(t, clientSession.Close()) }()

	tools, err := listTools(context.Background(), clientSession)
	require.NoError(t, err)

	byName := map[string]*mcp.Tool{}

	for _, tool := range tools {
		byName[tool.Name] = tool
	}

	assert.Contains(t, byName, "list_orgs", "list_orgs tool was not registered")

	echo, ok := byName["echo"]
	require.True(t, ok, "upstream echo tool was not mirrored")

	// The mirrored tool carries the injected org argument.
	schema, err := json.Marshal(echo.InputSchema)
	require.NoError(t, err)
	assert.Contains(t, string(schema), `"`+orgArg+`"`, "mirrored echo tool is missing the org argument")
}

func TestRunErrors(t *testing.T) {
	ctx := context.Background()

	assert.Error(t, NewProxy(nil, "test").Run(ctx), "expected an error for a nil config")
	assert.Error(t, NewProxy(&Config{}, "test").Run(ctx), "expected an error when no orgs are configured")

	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "org1", Token: "x", Endpoint: "http://127.0.0.1:0"},
	}}, "test")

	assert.Error(t, proxy.Run(ctx), "expected an error when the upstream connection fails")
}

func TestCloseSessions(t *testing.T) {
	up := newCapturingUpstream(t)

	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "org1", APIKey: "a", AppKey: "b", Endpoint: up.URL},
	}}, "test")

	_, err := proxy.session(context.Background(), "org1")
	require.NoError(t, err)

	proxy.closeSessions()

	proxy.mu.Lock()
	n := len(proxy.sessions)
	proxy.mu.Unlock()

	assert.Zero(t, n, "closeSessions left cached sessions")
}
