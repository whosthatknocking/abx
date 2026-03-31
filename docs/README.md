# ABX

`abx` is a local-first service for connecting messaging apps to configurable AI agents. The architecture is intended to stay agnostic to the underlying messaging transport and agent provider, even though the current v1 implementation is built around Signal via `signal-cli` on macOS.

## Objectives

- Connect trusted messaging conversations to agent-backed assistance through a simple local runtime.
- Keep messaging, agent, persistence, and execution layers modular so the system can evolve beyond a single messaging app or model API.
- Support both conversational workflows and carefully controlled local task execution.
- Favor auditability, explicit trust checks, and deny-by-default execution over opaque automation.
- Stay easy to run and reason about: one binary, local config, minimal moving parts.

## Status

- The project currently provides a working local scaffold for config loading, session persistence, approval handling, command policy enforcement, and agent-backed conversational responses.
- The first messaging implementation is `signal-cli` JSON-RPC, and it still needs a production-grade event loop and send/receive implementation.

## Features

- Messaging-to-agent bridge architecture with Signal as the first transport
- Trusted-sender-only messaging interaction in v1
- Group-chat activation via transport metadata in v1 (`signal-cli` Signal mention metadata)
- Conversational agent responses from local context only
- Built-in control commands: `/version`, `/config`, `/reset`
- Deny-by-default shell execution with explicit allow rules
- Request-bound approval tokens for command execution
- Local conversation/session persistence with soft-reset support

## Quick Start

1. Install `signal-cli`.
2. Register the bot Signal account with `signal-cli`.
3. Copy `config.toml.example` to `~/.config/abx/config.toml`.
4. Update the Signal account, trusted numbers, OpenAI API key, and command policy rules.
5. If you want local MCP-style integrations for a local agent endpoint, enable the desired `[[mcp.servers]]` entries.
6. Start `signal-cli` in JSON-RPC mode.
7. Run `make build`.
8. Start `./abx`.

## Running `signal-cli` in JSON-RPC Mode

The default `abx` config expects `signal-cli` to expose JSON-RPC over a local UNIX socket.

1. Create the Signal data directory if needed:

```bash
mkdir -p ~/.local/share/signal-cli
```

2. Start `signal-cli` in daemon mode with a UNIX socket:

```bash
signal-cli daemon --socket ~/.local/share/signal-cli/json-rpc.sock
```

3. Make sure your `~/.config/abx/config.toml` matches that socket path:

```toml
[messaging.signal_cli]
rpc_mode = "json-rpc"
rpc_socket = "~/.local/share/signal-cli/json-rpc.sock"
```

If you prefer TCP on loopback instead of a UNIX socket, bind only to `127.0.0.1` and update the config accordingly:

```bash
signal-cli daemon --tcp 127.0.0.1:7583
```

```toml
[messaging.signal_cli]
rpc_mode = "json-rpc"
rpc_host = "127.0.0.1"
rpc_port = 7583
```

## Project Docs

- [Project Specification](./PROJECT_SPEC.md)
- [User Guide](./USER_GUIDE.md)
- [Development](./DEVELOPMENT.md)

## Configuration Notes

- Configuration is file-based only in v1.
- `agent.primary.model` is required for OpenAI.
- `[[mcp.servers]]` controls which MCP server names are enabled for local LM Studio-style integrations.
- For local endpoints with enabled MCP servers, `abx` uses LM Studio's native `/api/v1/chat` route instead of OpenAI-compatible `/v1/chat/completions`.
- In LM Studio, using servers from `mcp.json` through the API requires the `Allow calling servers from mcp.json` setting to be enabled, and LM Studio requires authentication to be enabled before that setting can be turned on.
- `[debug].enabled = true` appends agent identity details to normal chat responses for troubleshooting.
- Shell commands are blocked unless they match an enabled allow rule.
- `signal-cli` is expected to run locally in JSON-RPC mode over a UNIX socket by default.

## Notes

- v1 is file-configured only.
- Conversational answers come from model context only; live external lookups are disabled in v1.
- Shell execution is deny-by-default and must match an explicit allow rule.
