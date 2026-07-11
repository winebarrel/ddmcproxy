package ddmcproxy

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")

	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("TEST_APP_KEY", "app-from-env")

	path := writeConfig(t, `
orgs:
  - name: org1
    api_key: api1
    app_key: app1
  - name: org2
    api_key: api2
    app_key: ${TEST_APP_KEY}
    endpoint: https://mcp.datadoghq.eu/api/unstable/mcp-server/mcp
`)

	config, err := LoadConfig(path)

	if err != nil {
		t.Fatal(err)
	}

	if got := config.OrgNames(); got[0] != "org1" || got[1] != "org2" {
		t.Errorf("OrgNames = %v", got)
	}

	// Default endpoint is filled in.
	if config.Org("org1").Endpoint != DefaultEndpoint {
		t.Errorf("org1 endpoint = %s, want default", config.Org("org1").Endpoint)
	}

	// Per-org endpoint override is kept.
	if config.Org("org2").Endpoint != "https://mcp.datadoghq.eu/api/unstable/mcp-server/mcp" {
		t.Errorf("org2 endpoint = %s", config.Org("org2").Endpoint)
	}

	// Env expansion.
	if config.Org("org2").AppKey != "app-from-env" {
		t.Errorf("org2 app_key = %s, want app-from-env", config.Org("org2").AppKey)
	}

	if config.Org("nope") != nil {
		t.Error("Org(nope) should be nil")
	}
}

func TestLoadConfigToken(t *testing.T) {
	// A token authenticates as a bearer token, whether it is a PAT or a SAT.
	for name, token := range map[string]string{"pat": "ddpat_secret", "sat": "ddsat_secret"} {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, "orgs:\n  - name: org1\n    token: "+token+"\n")

			config, err := LoadConfig(path)

			if err != nil {
				t.Fatal(err)
			}

			org := config.Org("org1")

			if !org.UseToken() {
				t.Error("org1 should use a token")
			}

			if got := authHeaders(org); got["Authorization"] != "Bearer "+token {
				t.Errorf("authHeaders = %v", got)
			}
		})
	}
}

func TestLoadConfigErrors(t *testing.T) {
	cases := map[string]string{
		"no orgs":        `endpoint: https://example.com`,
		"missing name":   "orgs:\n  - api_key: a\n    app_key: b\n",
		"missing apikey": "orgs:\n  - name: org1\n    app_key: b\n",
		"missing appkey": "orgs:\n  - name: org1\n    api_key: a\n",
		"no auth":        "orgs:\n  - name: org1\n",
		"token and keys": "orgs:\n  - name: org1\n    token: ddpat_x\n    api_key: a\n    app_key: b\n",
		"duplicate":      "orgs:\n  - name: org1\n    api_key: a\n    app_key: b\n  - name: org1\n    api_key: a\n    app_key: b\n",
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeConfig(t, content)); err == nil {
				t.Errorf("expected error for %q", name)
			}
		})
	}
}
