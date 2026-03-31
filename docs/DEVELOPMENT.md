# Development

## Requirements

- Go 1.23+
- `signal-cli` installed locally on macOS
- `sqlite3` available locally when using the SQLite repository backend

## Layout

- `cmd/abx`: entrypoint
- `internal/config`: file-based TOML loading
- `internal/agent`: agent providers
- `internal/messenger`: Signal adapter
- `internal/repository`: persistence backends
- `internal/executor`: command policy and execution
- `internal/handler`: runtime orchestration

## Current State

- TOML parsing is implemented locally with stdlib-only code
- The Signal adapter now includes JSON-RPC socket/TCP transport scaffolding, but still needs production hardening against the exact `signal-cli` event shapes used in your environment
- OpenAI chat-completions integration is implemented with the standard library HTTP client
- SQLite persistence uses the local `sqlite3` CLI
- Command execution is deny-by-default and policy-validated at startup

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
