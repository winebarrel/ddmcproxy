package ddmcproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

	if err != nil {
		t.Fatal(err)
	}

	if apiKey, appKey, _ := keysUp.headers(); apiKey != "api-secret" || appKey != "app-secret" {
		t.Errorf("key headers = %q/%q, want api-secret/app-secret", apiKey, appKey)
	}

	// The same org returns the cached session without reconnecting.
	keys2, err := proxy.session(ctx, "keys")

	if err != nil {
		t.Fatal(err)
	}

	if keys1 != keys2 {
		t.Error("session was not cached")
	}

	// A token org attaches an Authorization: Bearer header.
	if _, err := proxy.session(ctx, "tok"); err != nil {
		t.Fatal(err)
	}

	if _, _, auth := tokenUp.headers(); auth != "Bearer ddsat_secret" {
		t.Errorf("Authorization = %q, want Bearer ddsat_secret", auth)
	}

	// An unknown org is an error.
	if _, err := proxy.session(ctx, "nope"); err == nil {
		t.Error("expected error for unknown org")
	}

	// dropSession evicts the session so the next call reconnects.
	proxy.dropSession("keys")

	keys3, err := proxy.session(ctx, "keys")

	if err != nil {
		t.Fatal(err)
	}

	if keys3 == keys1 {
		t.Error("dropSession did not force a reconnect")
	}
}

func TestCloseSessions(t *testing.T) {
	up := newCapturingUpstream(t)

	proxy := NewProxy(&Config{Orgs: []*OrgConfig{
		{Name: "org1", APIKey: "a", AppKey: "b", Endpoint: up.URL},
	}}, "test")

	if _, err := proxy.session(context.Background(), "org1"); err != nil {
		t.Fatal(err)
	}

	proxy.closeSessions()

	proxy.mu.Lock()
	n := len(proxy.sessions)
	proxy.mu.Unlock()

	if n != 0 {
		t.Errorf("closeSessions left %d cached sessions, want 0", n)
	}
}
