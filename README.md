# ddmcproxy

[![CI](https://github.com/winebarrel/ddmcproxy/actions/workflows/ci.yml/badge.svg)](https://github.com/winebarrel/ddmcproxy/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/winebarrel/ddmcproxy/branch/main/graph/badge.svg)](https://codecov.io/gh/winebarrel/ddmcproxy)

A multi-org proxy for the [Datadog MCP Server](https://docs.datadoghq.com/bits_ai/mcp_server/setup/).

The Datadog MCP server does not support multiple organizations in a single
connection. `ddmcproxy` sits in front of it, mirrors all of its tools, and adds
an `org` argument to each tool. When a tool is called, the proxy picks the
matching org's credentials and forwards the request to the Datadog MCP server.

```
Claude Code ──stdio──▶ ddmcproxy ──HTTP(token or DD_API_KEY/DD_APPLICATION_KEY)──▶ Datadog MCP
                          │
                          ├─ org=foo ─▶ foo's credentials
                          └─ org=bar ─▶ bar's credentials
```

## Install

```
go install github.com/winebarrel/ddmcproxy/cmd/ddmcproxy@latest
```

## Configuration

Create a YAML config file. Values are passed through `os.ExpandEnv`, so secrets
can be referenced as `${ENV_VAR}` instead of being written in plain text.

```yaml
# ddmcproxy.yml

# Optional. Default: https://mcp.datadoghq.com/api/unstable/mcp-server/mcp
# endpoint: https://mcp.datadoghq.com/api/unstable/mcp-server/mcp

orgs:
  # Each org authenticates with EITHER a token (a PAT or SAT) OR the legacy
  # api_key + app_key pair -- not both.
  - name: foo
    token: ${FOO_DD_TOKEN}
  - name: bar
    api_key: ${BAR_DD_API_KEY}
    app_key: ${BAR_DD_APP_KEY}
    # Optional per-org endpoint override (e.g. for a different Datadog site).
    # endpoint: https://mcp.datadoghq.eu/api/unstable/mcp-server/mcp
```

Authentication per org is one of:

- `token`: a Datadog [Personal Access Token][pat] (`ddpat_`) or
  [Service Access Token][sat] (`ddsat_`), sent as an `Authorization: Bearer`
  header. Preferred.
- `api_key` + `app_key`: the legacy API key and Application key pair.

[pat]: https://docs.datadoghq.com/account_management/personal-access-tokens/
[sat]: https://docs.datadoghq.com/account_management/service-access-tokens/

## Usage

```
Usage: ddmcproxy --config=STRING [flags]

Flags:
  -h, --help             Show help.
  -c, --config=STRING    Config file path ($DDMCPROXY_CONFIG).
      --version
```

### Claude Code

Register it as an MCP server:

```json
{
  "mcpServers": {
    "datadog": {
      "command": "ddmcproxy",
      "args": ["--config", "/path/to/ddmcproxy.yml"]
    }
  }
}
```

Then call a tool with the target org, e.g.:

> Using the **foo** org, search Datadog logs for errors in the last hour.

Every proxied tool gains a required `org` argument whose allowed values are the
org names from your config.

## How it works

- On startup the proxy connects to the Datadog MCP server as the **first**
  configured org and lists the available tools. All orgs are assumed to expose
  the same set of tools.
- Each upstream tool is re-registered with a required `org` string argument
  (enumerated over the configured org names).
- On a tool call, the proxy strips the `org` argument, looks up that org's
  credentials, connects (lazily, then cached) to the Datadog MCP server -- with
  an `Authorization: Bearer` token, or the `DD_API_KEY` / `DD_APPLICATION_KEY`
  headers -- and forwards the call.