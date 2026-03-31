# User Guide

## Chat Types

- In direct chats, only trusted numbers may interact with `abx`.
- In group chats, only trusted numbers may interact and the bot must be explicitly mentioned by Signal mention metadata.

## Before Using `abx`

- Make sure your bot Signal account is already registered with `signal-cli`.
- Start `signal-cli` in JSON-RPC daemon mode before starting `abx`.
- By default, `abx` expects a local UNIX socket at `~/.local/share/signal-cli/json-rpc.sock`.
- If you use a local OpenAI-compatible agent such as LM Studio, you can enable or disable forwarded MCP server names with `[[mcp.servers]]` in `config.toml`.
- For LM Studio MCP access through the API, LM Studio must have `Allow calling servers from mcp.json` enabled, and that setting requires authentication to be enabled in LM Studio first.

## Message Types

- Normal questions are sent to the configured agent.
- `/version`, `/config`, and `/reset` are built-in control commands.
- `/run` shows command usage help.
- `/run <command>` proposes a shell command and requires an approval token before execution.

## Conversational Requests

- Trusted users can ask normal questions such as explanations, summaries, or brainstorming prompts.
- In v1, responses come only from the configured model plus locally stored conversation context.
- Live external lookups such as current weather or news are not available in v1.
- If you use a local LM Studio-compatible endpoint with enabled `[[mcp.servers]]`, `abx` will route those requests through LM Studio's MCP-aware chat path for supported tasks such as browser access.
- If `[debug].enabled = true`, normal chat replies also include which configured agent responded.

## Approval Flow

1. Send `/run` if you want to see the usage format
2. Send `/run pwd`
3. `abx` replies with a command proposal and token such as `YES abc123`
4. Any trusted participant in that chat may approve with the exact token
5. If the command is allowed by policy, it executes in the configured workspace

## Example

1. Send `/run pwd`
2. `abx` replies with a command proposal and token such as `YES abc123`
3. Any trusted participant in that chat may approve with the exact token
4. If the command is allowed by policy, it executes in the configured workspace

## Control Commands

- `/version`: show the running application version
- `/config`: show a safe summary of the configured agent provider/model
- `/reset`: start a fresh active session for the current chat while preserving historical state

## Resetting Context

- Send `/reset` to soft-reset the active session
- Previous sessions remain available for audit and diagnostics
