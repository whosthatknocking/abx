# AGENTS.md

This file gives project-specific guidance to AI agents working in this repository.

## Project Context

- Project: `abx`
- Purpose: bridge trusted messaging conversations to configurable AI agents and tightly controlled local shell execution
- Language: Go 1.23+
- Runtime model: single long-lived local process
- Current platform scope: macOS-first
- Current messaging implementation: `signal-cli` over JSON-RPC
- Current agent contract: OpenAI-compatible chat APIs, with a native LM Studio `/api/v1/chat` path when local MCP integrations are enabled
- Persistence:
  - SQLite by default via the local `sqlite3` CLI
  - in-memory repository for tests

## Source of Truth

When behavior, naming, safety rules, or scope is unclear, use these files in this order:

1. `docs/PROJECT_SPEC.md`
2. `docs/README.md`
3. `docs/USER_GUIDE.md`
4. `docs/DEVELOPMENT.md`
5. `README.md`

Keep docs aligned with implementation. If you change config shape, slash commands, agent behavior, approval flow, or persistence behavior, update the relevant docs in the same task.

## Architecture Map

- `cmd/abx/main.go`
  - process entrypoint
  - config loading
  - runtime wiring for messenger, repository, executor, and agent provider
  - startup health check and signal handling
- `internal/config/`
  - local TOML parser and config decoding
  - config normalization and defaulting
  - config schema is intentionally narrow and project-specific
- `internal/handler/service.go`
  - main runtime orchestration
  - trust checks
  - slash command handling
  - conversation routing
  - approval state handling
  - agent prompt assembly
  - session-scoped settings such as persona, format, fallback mode, and thinking mode
- `internal/agent/openai/provider.go`
  - OpenAI-compatible request/response handling
  - LM Studio native `/api/v1/chat` routing when local MCP integrations are enabled
  - model checks and provider-specific request shaping
- `internal/agent/fallback.go`
  - primary/fallback routing and swap behavior
- `internal/messenger/signalcli/adapter.go`
  - Signal JSON-RPC transport integration
  - send routing and subscription/reconnect behavior
- `internal/executor/`
  - command policy evaluation
  - bash execution with timeout and working directory
- `internal/repository/interface.go`
  - repository contract
- `internal/repository/sqlite/`
  - SQLite-backed persistence and schema evolution
- `internal/repository/inmemory/`
  - lightweight repository used by tests
- `internal/audit/`
  - audit logging and retention behavior
- `pkg/types/types.go`
  - shared runtime types

## Non-Negotiable Design Rules

- Keep the trust model explicit.
  - Only trusted senders may interact.
  - Group chats must require an explicit bot mention.
- Keep shell execution deny-by-default.
  - Do not bypass policy checks.
  - Do not introduce hidden execution paths.
- Preserve the approval flow.
  - Commands must require an explicit request-bound approval token such as `YES <token>`.
- Keep built-in local controls local.
  - Read-only or session-local slash commands should not require an agent round-trip.
  - If a command should respond immediately, make sure it can bypass blocked in-flight agent work when appropriate.
- Do not expose secrets in `/config`, logs, or user-visible status text.
- Keep conversation behavior local-first.
  - Normal chat responses should come from configured models plus locally stored context.
- Preserve transport parity where practical.
  - Behavior should not silently diverge between direct and group chat beyond the explicit trust/mention rules.
- Prefer minimal, targeted changes over broad refactors.

## Agent and MCP Conventions

- Route OpenAI-compatible chat through `internal/agent/openai/provider.go`.
- When local MCP integrations are enabled for a local endpoint, `abx` intentionally uses LM Studio's native `/api/v1/chat` path instead of `/v1/chat/completions`.
- Be careful with request-shape differences:
  - OpenAI-compatible chat-completions accepts OpenAI-style message payloads.
  - LM Studio native chat expects transcript-style `input` plus `integrations`.
  - Do not assume request-body extensions accepted by one route are valid on the other.
- Provider-specific controls should stay generic at the config surface whenever possible.
  - Example: thinking controls use generic config keys and are translated by the provider implementation.
- If you add or change provider behavior, update tests in `internal/agent/openai/provider_test.go`.

## Handler and Slash Command Conventions

- Built-in commands live in `internal/handler/service.go`.
- Keep slash command semantics predictable and explicit.
- Session-scoped settings should persist through the repository abstraction, not ad hoc in memory.
- Status-style local controls such as `/agents status` and `/config` must reflect the implemented runtime/session behavior and remain safe for user-visible output.
- If a command mutates only agent/session behavior and should respond immediately, ensure the immediate local-control path covers it.
- Session-scoped controls currently include persona, format, thinking mode, and fallback mode; `/reset` returns those to fresh-session defaults by rotating the active session.
- If you add or change slash commands, update:
  - handler tests
  - `docs/README.md`
  - `docs/USER_GUIDE.md`
  - `config.toml.example` when config is involved

## Config Conventions

- The config loader is intentionally project-specific, not a full TOML implementation.
- Prefer flat config keys when the surface is small and stable.
- Keep backward compatibility for config shape changes when practical, especially if a recent shape was already documented.
- Normalize and default values in `internal/config/config.go`.
- Decode shape changes in `internal/config/decode.go`.
- Add or update tests in `internal/config/config_test.go` for config changes.

## Persistence Conventions

- Keep the repository interface authoritative.
- If you add session state or new persisted behavior:
  - update `internal/repository/interface.go`
  - update both repository implementations
  - add migration/column-ensure logic for SQLite
  - add or update tests for both backends where appropriate
- Do not implement behavior in only one repository backend unless the task explicitly says to.

## Error Handling and Stability

- Return bounded, user-meaningful errors rather than leaking raw internals where avoidable.
- Keep logs informative, but do not log secrets.
- Preserve compatibility with local-first failure modes such as:
  - missing config
  - unavailable `signal-cli`
  - unavailable `sqlite3`
  - local model connectivity failure
  - LM Studio MCP permission or schema errors
- If an operation cannot truly be hot-reloaded, do not pretend it can. Narrow the feature scope instead.

## Testing Expectations

Run the smallest relevant test set first, then broaden when needed.

- Main suite: `go test ./...`
- Format: `make fmt`
- Full tests: `make test`
- Optional extra check: `GOCACHE="${XDG_CACHE_HOME:-$HOME/.cache}/abx/go-build" GOMODCACHE="${XDG_CACHE_HOME:-$HOME/.cache}/abx/gomod" go vet ./...`

Testing guidance:

- Add or update tests for any behavior change in:
  - config parsing or normalization
  - slash command behavior
  - approval flow
  - repository persistence
  - provider request shaping
  - LM Studio routing behavior
- Prefer focused tests first.
- If you do not run the full suite, say so explicitly.

## Documentation Expectations

Update docs when any of these change:

- config keys or config semantics
- slash commands or command behavior
- approval or trust behavior
- provider behavior
- LM Studio/MCP routing behavior
- startup or run instructions
- persistence behavior visible to users

Common files to update:

- `config.toml.example`
- `docs/README.md`
- `docs/USER_GUIDE.md`
- `docs/DEVELOPMENT.md`
- `docs/PROJECT_SPEC.md` when the contract itself changes

## Practical Workflow

1. Read the relevant code and contract docs first.
2. Make the smallest coherent change.
3. Update tests with the code change.
4. Update docs if user-visible behavior changed.
5. Run targeted tests, then broaden if warranted.

## Commit Guidance

- Use imperative commit subjects.
- Keep commits small and single-purpose when practical.
- Include tests in the same commit as the behavior change when possible.
- Avoid mixing unrelated refactors with behavior changes unless they are tightly coupled.

## Good Changes

- tightening slash-command behavior with regression tests
- keeping local control commands responsive during blocked agent work
- improving provider request shaping for LM Studio vs OpenAI-compatible routes
- adding session-scoped settings through the repository abstraction
- updating docs and config examples with behavior changes
- narrowing reload scope when full reload is not truly supported

## Bad Changes

- bypassing trust checks or mention requirements
- bypassing command approval or policy validation
- exposing secrets in `/config` or logs
- changing session behavior in only one repository backend
- pretending full hot reload works when only part of runtime state can be safely replaced
- sending OpenAI-style extensions to LM Studio native chat without verifying schema compatibility
- changing config shape without updating docs and examples
