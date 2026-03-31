# Development

`abx` is intended to be messaging-platform-agnostic at the architecture level. The current codebase is still v1 and therefore Signal-first, but the internal shape should continue to support future messaging adapters without redefining the application around one transport.

## Requirements

- Go 1.23+
- `signal-cli` installed locally on macOS
- `sqlite3` available locally when using the SQLite repository backend

## Running `signal-cli` for Local Development

Default UNIX socket mode:

```bash
mkdir -p ~/.local/share/signal-cli
signal-cli daemon --socket ~/.local/share/signal-cli/json-rpc.sock
```

Loopback TCP mode:

```bash
signal-cli daemon --tcp 127.0.0.1:7583
```

If you use loopback TCP, update `config.toml` to set `rpc_host` and `rpc_port` instead of `rpc_socket`.

## Layout

- `cmd/abx`: entrypoint
- `internal/config`: file-based TOML loading
- `internal/agent`: agent providers
- `internal/messenger`: messaging adapters, with Signal as the current implementation
- `internal/repository`: persistence backends
- `internal/executor`: command policy and execution
- `internal/handler`: runtime orchestration

## Current State

- TOML parsing is implemented locally with stdlib-only code
- The Signal adapter now includes JSON-RPC socket/TCP transport scaffolding, but still needs production hardening against the exact `signal-cli` event shapes used in your environment
- OpenAI chat-completions integration is implemented with the standard library HTTP client
- Local chat routing now has two paths:
  - normal local or remote chat uses OpenAI-compatible `/v1/chat/completions`
  - local chat with enabled `[[mcp.servers]]` uses LM Studio native `/api/v1/chat`
- On the LM Studio MCP path, `abx` sends a transcript-style `input`, `integrations`, and a short system instruction telling the model to use available integrations for browser/live-data tasks
- LM Studio requires `Require Authentication` to be enabled before `Allow calling servers from mcp.json` can be turned on for API-driven MCP usage
- SQLite persistence uses the local `sqlite3` CLI
- Command execution is deny-by-default and policy-validated at startup
- Runtime logs now include higher-level interaction tracing for accepted messages, agent request start/end, command proposal creation, approvals, and command execution outcomes

## Quality Checks

- `make fmt`
- `make test`
- `GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go vet ./...`

## Commands

- `make build`
- `make test`
- `make fmt`

## Known Gaps

- The `signal-cli` transport layer still needs production validation against real daemon traffic and broader event parsing coverage
- The current TOML parser intentionally supports the project’s config shape, not the full TOML language
- LM Studio MCP behavior still depends on the selected model and LM Studio-side MCP permissions; `abx` can route and instruct correctly, but it cannot force tool use if the local model refuses or lacks capability
