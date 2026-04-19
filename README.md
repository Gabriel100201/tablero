# Tablero

[![release](https://img.shields.io/github/v/release/Gabriel100201/tablero?logo=github)](https://github.com/Gabriel100201/tablero/releases/latest)
[![ci](https://github.com/Gabriel100201/tablero/actions/workflows/ci.yml/badge.svg)](https://github.com/Gabriel100201/tablero/actions/workflows/ci.yml)
[![license](https://img.shields.io/github/license/Gabriel100201/tablero)](./LICENSE)

Unified task aggregator for AI coding agents. One MCP server that connects **Linear** and **Taiga** so you can list, search, create, update, and read tasks — and Linear documents — across all your workspaces without leaving the terminal.

```
Agent (Claude Code / Cursor / Cline / OpenCode / any MCP client)
    | MCP stdio
Tablero (single Go binary)
    |
    +---> Linear GraphQL API (any number of workspaces)
    +---> Taiga REST API (any number of instances, self-hosted or cloud)
```

## Features

- **Tasks across workspaces** — list, get, create, update, search issues from multiple Linear workspaces and Taiga instances at once.
- **Linear documents CRUD** — read and write Linear docs (markdown) from your agent.
- **Team vs Project aware** — Linear's two-level hierarchy (teams + projects) is exposed correctly; filter tasks or docs by either.
- **Interactive CLI** — `tablero config add linear` / `add taiga` walks you through setup, validates the connection, and writes the config for you.
- **Graceful degradation** — if one provider is unreachable (VPN down, etc.) the others still work; failures surface as warnings, not hard errors.
- **Single binary** — one Go executable, zero runtime dependencies.

## Install

### Option A — Download a pre-built binary (no Go needed)

Grab the archive for your OS and architecture from the [**latest release**](https://github.com/Gabriel100201/tablero/releases/latest):

- `tablero_<version>_linux_amd64.tar.gz`
- `tablero_<version>_linux_arm64.tar.gz`
- `tablero_<version>_darwin_amd64.tar.gz` — macOS Intel
- `tablero_<version>_darwin_arm64.tar.gz` — macOS Apple Silicon
- `tablero_<version>_windows_amd64.zip`

Extract the `tablero` (or `tablero.exe`) binary and put it somewhere on your `PATH`.

```bash
# example for Linux amd64
curl -sL https://github.com/Gabriel100201/tablero/releases/latest/download/tablero_<version>_linux_amd64.tar.gz | tar -xz
sudo mv tablero /usr/local/bin/
```

Each release also ships `checksums.txt` (SHA-256) so you can verify the download.

### Option B — `go install`

```bash
go install github.com/Gabriel100201/tablero/cmd/tablero@latest
```

The binary lands in `$(go env GOPATH)/bin` (typically `~/go/bin` on Unix, `%USERPROFILE%\go\bin` on Windows). Make sure that directory is on your `PATH`.

### Option C — Build from source

```bash
git clone https://github.com/Gabriel100201/tablero.git
cd tablero
go install ./cmd/tablero/
```

### Verify

```bash
tablero version
tablero help
```

## Quick start — interactive setup

The fastest path is the built-in CLI:

```bash
# Add a Linear workspace (prompts for a friendly name + API key; validates the key)
tablero config add linear

# Add a Taiga instance (prompts for URL + username + password; validates via auth)
tablero config add taiga

# Add as many as you want — e.g. multiple Linear workspaces for work and personal
tablero config add linear

# Inspect what's configured (secrets are masked)
tablero config list

# Verify every provider is reachable
tablero config test

# Remove one
tablero config remove <name>

# Print the config file path
tablero config path
```

The config is saved to `~/.tablero/config.yaml` (or `$TABLERO_CONFIG` if set) with mode `0600` — owner read/write only — because it contains API keys and passwords.

## Manual configuration (optional)

If you prefer editing YAML by hand, copy [`config.example.yaml`](./config.example.yaml) to `~/.tablero/config.yaml` and fill it in:

```yaml
providers:
  - name: work           # any unique label — you'll use it to reference this provider
    type: linear
    api_key: "lin_api_..."

  - name: personal
    type: linear
    api_key: "lin_api_..."

  - name: team-taiga
    type: taiga
    url: "https://taiga.example.com"
    username: "myuser"
    password: "mypassword"
```

| Field | Required for | Description |
|-------|-------------|-------------|
| `name` | all | Unique identifier — an arbitrary label you choose |
| `type` | all | `linear` or `taiga` |
| `api_key` | linear | Personal API key (Linear → Settings → API → Personal API keys) |
| `url` | taiga | Base URL of the Taiga instance |
| `username` | taiga | Taiga username |
| `password` | taiga | Taiga password |

Override the config path with the `TABLERO_CONFIG` environment variable.

## Register with your AI agent

### Claude Code

```bash
claude mcp add --transport stdio tablero -- tablero mcp
```

> **Windows gotcha:** `claude mcp add` with absolute paths can strip backslashes. Either use just `tablero` (if it's on your PATH), or after running the command verify the path in `~/.claude.json` has proper backslashes (`C:\\Users\\...`) and fix it manually if needed.

### OpenCode

Add to `~/.config/opencode/config.json`:

```json
{
  "mcpServers": {
    "tablero": {
      "command": "tablero",
      "args": ["mcp"]
    }
  }
}
```

### Any MCP client

Tablero uses **stdio** transport. Configure with:

- Command: `tablero` (or full path to the binary)
- Args: `["mcp"]`

## MCP Tools

### Tasks

| Tool | Read-only | Description |
|------|-----------|-------------|
| `tasks_list` | ✓ | List open tasks across all providers. By default returns **all** open tasks; set `assigned=true` to see only your own. |
| `tasks_get` | ✓ | Full detail of a task by identifier (description, comments, labels) |
| `tasks_create` | — | Create a task in a specific provider/project |
| `tasks_update` | — | Update status, title, priority, description |
| `tasks_search` | ✓ | Keyword search across titles and descriptions |
| `tasks_projects` | ✓ | List all teams and projects (Linear exposes both; Taiga only projects) |
| `tasks_states` | ✓ | List valid workflow states for a project (use before `tasks_update`) |

### Documents (Linear only)

| Tool | Read-only | Description |
|------|-----------|-------------|
| `docs_list` | ✓ | List Linear docs; filter by provider, project, or title substring. Project filter also accepts a team name (returns union across its projects). |
| `docs_get` | ✓ | Fetch a doc by slug or UUID — returns the full markdown body |
| `docs_create` | — | Create a doc inside a Linear project |
| `docs_update` | — | Update title, content, icon, or color |
| `docs_delete` | — (destructive) | Permanently delete a doc |
| `docs_search` | ✓ | Native Linear document search |

### Identifier format

- **Linear tasks:** issue key, e.g. `ABC-42`
- **Linear docs:** slugId (from URL) or UUID
- **Taiga user stories:** `<providerName>:us:<id>`, e.g. `work:us:234`
- **Taiga tasks:** `<providerName>:task:<id>`, e.g. `work:task:56`

### Project model (Linear)

Linear has two levels:

- **Team** — e.g. name `"Acme"` with key `ACME`, groups all issues with prefix `ACME-xx`
- **Project** — e.g. `"Website Redesign"`, a logical grouping of issues inside a team

`tasks_projects` lists both, and the `Kind` column tells them apart. The `project` filter on `tasks_list` and `docs_list` matches either level — pass a team name/key to see everything in a team, or a project name to narrow down.

## Graceful degradation

If a provider is unreachable (e.g. Taiga behind a VPN that's disconnected), Tablero returns results from the healthy providers with a warning. It does not fail the whole call.

## Security

The config file contains API keys and passwords. Tablero writes it with permissions `0600` so only the owner can read it. On shared machines, make sure your home directory has appropriate protection.

Never commit your config to a public repository. This repo's `.gitignore` excludes `config.yaml` by default.

## Environment variables

| Variable | Description | Default |
|----------|-------------|---------|
| `TABLERO_CONFIG` | Config file path | `~/.tablero/config.yaml` |

## Requirements

- **Go 1.25+** to build from source (the CLI depends on `golang.org/x/term` for hidden password prompts)
- No runtime dependencies

## License

[MIT](./LICENSE)
