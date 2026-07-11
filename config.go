package ddmcproxy

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// DefaultEndpoint is the Datadog MCP server endpoint used when a config or an
// org does not override it. See https://docs.datadoghq.com/bits_ai/mcp_server/setup/
const DefaultEndpoint = "https://mcp.datadoghq.com/api/unstable/mcp-server/mcp"

// OrgConfig holds the credentials for a single Datadog organization.
//
// Authentication is either a token -- a Personal (ddpat_) or Service (ddsat_)
// Access Token, passed as a bearer token -- or the legacy API key + Application
// key pair. Exactly one of the two must be configured.
type OrgConfig struct {
	Name     string `yaml:"name"`
	Token    string `yaml:"token,omitempty"`
	APIKey   string `yaml:"api_key,omitempty"`
	AppKey   string `yaml:"app_key,omitempty"`
	Endpoint string `yaml:"endpoint,omitempty"`
}

// Config is the ddmcproxy configuration file.
type Config struct {
	// Endpoint is the default Datadog MCP endpoint for every org.
	Endpoint string       `yaml:"endpoint,omitempty"`
	Orgs     []*OrgConfig `yaml:"orgs"`
}

// LoadConfig reads and validates the config file at path.
//
// The file content is passed through os.ExpandEnv so that secrets can be
// referenced as ${DD_API_KEY} instead of being written in plain text.
func LoadConfig(path string) (*Config, error) {
	buf, err := os.ReadFile(path)

	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config

	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(buf))), &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := config.validate(); err != nil {
		return nil, err
	}

	return &config, nil
}

func (config *Config) validate() error {
	if config.Endpoint == "" {
		config.Endpoint = DefaultEndpoint
	}

	if len(config.Orgs) == 0 {
		return fmt.Errorf("no orgs are configured")
	}

	seen := map[string]bool{}

	for i, org := range config.Orgs {
		if org.Name == "" {
			return fmt.Errorf("orgs[%d]: 'name' is required", i)
		}

		if seen[org.Name] {
			return fmt.Errorf("orgs[%d]: duplicated org name: %s", i, org.Name)
		}

		seen[org.Name] = true

		if err := org.validateAuth(); err != nil {
			return err
		}

		if org.Endpoint == "" {
			org.Endpoint = config.Endpoint
		}
	}

	return nil
}

// UseToken reports whether the org authenticates with a bearer token (a PAT or
// SAT) rather than the legacy API key + Application key pair.
func (org *OrgConfig) UseToken() bool {
	return org.Token != ""
}

// validateAuth ensures exactly one authentication method is configured: either a
// token, or both an API key and an Application key.
func (org *OrgConfig) validateAuth() error {
	if org.Token != "" {
		if org.APIKey != "" || org.AppKey != "" {
			return fmt.Errorf("org '%s': specify either 'token' or 'api_key'/'app_key', not both", org.Name)
		}

		return nil
	}

	if org.APIKey == "" || org.AppKey == "" {
		return fmt.Errorf("org '%s': either 'token' or both 'api_key' and 'app_key' are required", org.Name)
	}

	return nil
}

// OrgNames returns the configured org names in file order.
func (config *Config) OrgNames() []string {
	names := make([]string, len(config.Orgs))

	for i, org := range config.Orgs {
		names[i] = org.Name
	}

	return names
}

// Org returns the config for the named org, or nil if it is not configured.
func (config *Config) Org(name string) *OrgConfig {
	for _, org := range config.Orgs {
		if org.Name == name {
			return org
		}
	}

	return nil
}
