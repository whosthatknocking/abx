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
- The first messaging implementation is `signal-cli` JSON-RPC, with reconnect handling, direct/group send routing, JSON-RPC response handling, and compatibility fallback for daemons that do not implement `subscribe`.
- The Signal transport still needs broader real-world validation against additional `signal-cli` event shapes and deployment environments.
- Signal image handling depends on the inbound `signal-cli` event including a local stored attachment filename for image media.

## Features

- Messaging-to-agent bridge architecture with Signal as the first transport
- Trusted-sender-only messaging interaction in v1
- Optional alerts to trusted recipients when an untrusted number messages the bot
- Group-chat activation via transport metadata in v1 (`signal-cli` Signal mention metadata)
- Built-in slash commands and `/run` behave the same in direct and group chat once the bot is explicitly mentioned in the group
- Conversational agent responses from local context only
- Inbound Signal image attachments can be forwarded to vision-capable models
- Automatic conversation summaries for older context in longer chats
- Built-in control commands: `/help`, `/version`, `/config`, `/agents list`, `/agents status`, `/agents reload`, `/agents persona`, `/agents format`, `/agents thinking`, `/agents fallback`, `/agents switch`, `/reset`
- Session-scoped agent controls for persona, format, thinking, and fallback behavior
- Unified `/run <command-or-intent>` flow for direct commands or agent-recommended commands
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
- The checked-in `VERSION` file is the source of truth for release versioning.
- `agent.primary.model` is required for OpenAI.
- `security.notify_on_untrusted_message` can send a notification to selected trusted numbers when an untrusted sender contacts the bot.
- `security.untrusted_message_notify_numbers` must be a subset of `security.trusted_numbers`.
- `security.untrusted_message_include_preview` defaults to `false` so unknown message content is not forwarded unless you explicitly opt in.
- `security.untrusted_message_rate_limit_seconds` controls how often repeated alerts from the same unknown sender can be forwarded; the default is 900 seconds.
- `agent.primary.request_timeout_seconds` and `agent.fallback.request_timeout_seconds` control how long `abx` waits before treating an agent request as failed and moving to fallback.
- Agents can optionally declare thinking-control settings directly on each agent block with keys such as `thinking_default`, `thinking_parameter_path`, `thinking_enable_parameter_value`, `thinking_disable_parameter_value`, `thinking_enable_suffix`, `thinking_disable_suffix`, `thinking_enable_system_prompt`, and `thinking_disable_system_prompt`.
- Changing session thinking mode with `/agents thinking enable|disable|reset` updates the current session and sends a non-persisted confirmation so the control reply does not pollute the next prompt.
- `/agents status` includes the current session fallback state and, when thinking control is configured, the current session thinking state.
- `[[mcp.servers]]` controls which MCP server names are enabled for local LM Studio-style integrations.
- For local endpoints with enabled MCP servers, `abx` uses LM Studio's native `/api/v1/chat` route instead of OpenAI-compatible `/v1/chat/completions`.
- For LM Studio native `/api/v1/chat`, prefer the model's native reasoning request field when `/api/v1/models` reports `capabilities.reasoning.allowed_options`; for Gemma 4 this can be `thinking_parameter_path = "reasoning"` with values `on` and `off`.
- When LM Studio honors native reasoning controls, responses may still omit a separate `reasoning` output section; validate behavior using the model metadata plus response stats such as `reasoning_output_tokens`.
- In LM Studio, using servers from `mcp.json` through the API requires the `Allow calling servers from mcp.json` setting to be enabled, and LM Studio requires authentication to be enabled before that setting can be turned on.
- `[debug].enabled = true` appends agent identity details to normal chat responses for troubleshooting.
- Shell commands are blocked unless they match an enabled allow rule.
- `signal-cli` is expected to run locally in JSON-RPC mode over a UNIX socket by default.
- `/version` includes build metadata when it is available in the binary.
- `/config` reports normalized, non-secret runtime settings including messaging mode, agent contract/model, MCP visibility, storage, command policy, untrusted-message alert state, thinking-control state, debug state, and version.
- Pushing a Git tag in the form `vX.Y.Z` that matches `VERSION` triggers the GitHub release workflow and uploads release artifacts.

## Notes

- v1 is file-configured only.
- Conversational answers come from model context only; live external lookups are disabled in v1.
- Signal image messages are currently inbound-only; `abx` does not send generated images or other media back through Signal in v1.
- Longer conversations use a local summary of older turns plus a recent message window to keep useful context without sending the full transcript every time.
- Shell execution is deny-by-default and must match an explicit allow rule.
