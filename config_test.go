package ddmcproxy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

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
	require.NoError(t, err)

	assert.Equal(t, []string{"org1", "org2"}, config.OrgNames())

	// Default endpoint is filled in.
	assert.Equal(t, DefaultEndpoint, config.Org("org1").Endpoint)

	// Per-org endpoint override is kept.
	assert.Equal(t, "https://mcp.datadoghq.eu/api/unstable/mcp-server/mcp", config.Org("org2").Endpoint)

	// Env expansion.
	assert.Equal(t, "app-from-env", config.Org("org2").AppKey)

	assert.Nil(t, config.Org("nope"))
}

func TestLoadConfigToken(t *testing.T) {
	// A token authenticates as a bearer token, whether it is a PAT or a SAT.
	for name, token := range map[string]string{"pat": "ddpat_secret", "sat": "ddsat_secret"} {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, "orgs:\n  - name: org1\n    token: "+token+"\n")

			config, err := LoadConfig(path)
			require.NoError(t, err)

			org := config.Org("org1")

			assert.True(t, org.UseToken())
			assert.Equal(t, "Bearer "+token, authHeaders(org)["Authorization"])
		})
	}
}

func TestLoadConfigFileErrors(t *testing.T) {
	// Missing file.
	_, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.yml"))
	assert.Error(t, err)

	// Malformed YAML.
	_, err = LoadConfig(writeConfig(t, "orgs: [1, 2"))
	assert.Error(t, err)
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
			_, err := LoadConfig(writeConfig(t, content))
			assert.Error(t, err)
		})
	}
}
