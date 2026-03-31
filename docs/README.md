# ABX

`abx` is a local macOS service that bridges Signal conversations to a configured agent, with explicit approval required before any local shell command executes.

## Status

- The project currently provides a working local scaffold for config loading, session persistence, approval handling, command policy enforcement, and OpenAI-backed conversational responses.
- The `signal-cli` JSON-RPC integration is still a runtime skeleton and needs a production-grade event loop and send/receive implementation.

## Features

- Trusted-number-only Signal interaction
- Group-chat activation via Signal mention metadata
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

- [User Guide](./USER_GUIDE.md)
- [Development](./DEVELOPMENT.md)

## Configuration Notes

- Configuration is file-based only in v1.
- `agent.primary.model` is required for OpenAI.
- `[[mcp.servers]]` controls which MCP server names are forwarded to local OpenAI-compatible endpoints as request integrations.
- `[debug].enabled = true` appends agent identity details to normal chat responses for troubleshooting.
- Shell commands are blocked unless they match an enabled allow rule.
- `signal-cli` is expected to run locally in JSON-RPC mode over a UNIX socket by default.

## Notes

- v1 is file-configured only.
- Conversational answers come from model context only; live external lookups are disabled in v1.
- Shell execution is deny-by-default and must match an explicit allow rule.
