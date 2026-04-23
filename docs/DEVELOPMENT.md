# Development

`abx` is intended to be messaging-platform-agnostic at the architecture level. The current codebase is still v1 and therefore Signal-first, but the internal shape should continue to support future messaging adapters without redefining the application around one transport.

## Requirements

- Go 1.23+
- `signal-cli` installed locally on macOS
- `sqlite3` available locally when using the SQLite repository backend

## Running `signal-cli` for Local Development

Default UNIX socket mode:

```bash
mkdir -p "${XDG_DATA_HOME:-$HOME/.local/share}/signal-cli"
signal-cli daemon --socket "${XDG_DATA_HOME:-$HOME/.local/share}/signal-cli/json-rpc.sock"
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
- The Signal adapter supports JSON-RPC over UNIX socket or loopback TCP, direct/group send routing, reconnect handling, and graceful fallback when `signal-cli` does not implement the `subscribe` method
- The remaining Signal work is mostly production validation against the exact `signal-cli` event shapes used in your environment rather than basic transport wiring
- OpenAI chat-completions integration is implemented with the standard library HTTP client
- Local chat routing now has two paths:
  - normal local or remote chat uses OpenAI-compatible `/v1/chat/completions`
  - local chat with enabled `[[mcp.servers]]` uses LM Studio native `/api/v1/chat`
- On the LM Studio MCP path, `abx` sends a transcript-style `input`, `integrations`, and a short system instruction telling the model to use available integrations for browser/live-data tasks
- LM Studio requires `Require Authentication` to be enabled before `Allow calling servers from mcp.json` can be turned on for API-driven MCP usage
- SQLite persistence uses the local `sqlite3` CLI
- Command execution is deny-by-default and policy-validated at startup
- Longer conversations are compacted by storing a local summary of older turns and prepending that summary back into the agent prompt alongside a recent history window
- Session-scoped persona, format, thinking, and fallback overrides are stored through the repository abstraction and reset by rotating to a fresh active session
- `/version` can include build metadata injected at build time through `make build`
- Runtime logs now include higher-level interaction tracing for accepted messages, agent request start/end, command proposal creation, approvals, and command execution outcomes

## Quality Checks

- `make fmt`
- `make test`
- `make release-artifacts`
- `GOCACHE="${XDG_CACHE_HOME:-$HOME/.cache}/abx/go-build" GOMODCACHE="${XDG_CACHE_HOME:-$HOME/.cache}/abx/gomod" go vet ./...`
- GitHub Actions runs formatting, tests, `go vet`, and build checks on every push and pull request via `.github/workflows/ci.yml`
- GitHub Actions also publishes a GitHub release from `.github/workflows/release.yml` when a `vX.Y.Z` tag is pushed and the tag matches the checked-in `VERSION` file

## Commands

- `make build`
- `make test`
- `make fmt`
- `make release-artifacts`

## Versioning

`abx` uses the checked-in `VERSION` file as the default build version source. GitHub releases are published by pushing a matching tag such as `v0.1.0`.

### Release Steps

1. Update `VERSION` to the release number, for example `0.1.0`.
2. Commit the version bump and push the branch.
3. Create an annotated tag with a leading `v`, for example `git tag -a v0.1.0 -m "Release v0.1.0"`.
4. Push the tag with `git push origin v0.1.0`.
5. GitHub Actions will verify the tag matches `VERSION`, run tests, build release artifacts, and publish the GitHub release.

## Known Gaps

- The `signal-cli` transport layer still needs production validation against real daemon traffic and broader event parsing coverage
- The current TOML parser intentionally supports the project’s config shape, not the full TOML language
- LM Studio MCP behavior still depends on the selected model and LM Studio-side MCP permissions; `abx` can route and instruct correctly, but it cannot force tool use if the local model refuses or lacks capability
- Agent HTTP timeouts are configurable per provider via `request_timeout_seconds`; longer local-primary timeouts can reduce premature fallback when LM Studio is slow on large prompts or MCP-heavy responses
