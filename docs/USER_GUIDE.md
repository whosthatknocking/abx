# User Guide

`abx` is designed as a messaging-to-agent bridge. In the current v1 build, the messaging side is implemented through Signal using `signal-cli`, so the examples in this guide are Signal-specific even though the broader project direction is transport-agnostic.

## Early Stage Warning

- `abx` is still an early-stage project.
- Configuration, runtime behavior, and integration interfaces may change as the project evolves.
- If you build tooling or workflows around the current API or message behavior, expect some churn until the project stabilizes.

## Chat Types

- In direct chats, only trusted numbers may interact with `abx`.
- In group chats, only trusted numbers may interact and the bot must be explicitly mentioned by Signal mention metadata.
- In group chats, built-in slash commands such as `/help`, `/version`, `/config`, `/agents ...`, `/run ...`, and `/reset` work the same way as in direct chat once the bot is explicitly mentioned.
- Optionally, `abx` can notify selected trusted numbers when an untrusted number messages the bot. The untrusted sender still receives no response.

## Before Using `abx`

- Make sure your bot Signal account is already registered with `signal-cli`.
- Start `signal-cli` in JSON-RPC daemon mode before starting `abx`.
- By default, `abx` expects a local UNIX socket at `~/.local/share/signal-cli/json-rpc.sock`.
- If you use a local OpenAI-compatible agent such as LM Studio, you can enable or disable forwarded MCP server names with `[[mcp.servers]]` in `config.toml`.
- You can control how long `abx` waits for each agent with `agent.primary.request_timeout_seconds` and `agent.fallback.request_timeout_seconds`. This is especially useful when a slower local model should get more time before fallback is tried.
- For LM Studio MCP access through the API, LM Studio must have `Allow calling servers from mcp.json` enabled, and that setting requires authentication to be enabled in LM Studio first.

## Message Types

- `/help` shows a quick summary of the available message types and built-in commands.
- Normal questions are sent to the configured agent.
- `/version`, `/config`, `/agents list`, `/agents status`, `/agents reload`, `/agents persona`, `/agents format`, `/agents thinking`, `/agents fallback`, `/agents switch`, and `/reset` are built-in control commands.
- `/run` shows command usage help.
- `/run <command-or-intent>` can either:
  - propose an exact shell command directly when the input already looks executable under the current policy
  - ask the agent to recommend one runnable command plus a short reason, then propose that command for approval

## Conversational Requests

- Trusted users can ask normal questions such as explanations, summaries, or brainstorming prompts.
- In v1, responses come only from the configured model plus locally stored conversation context.
- For longer chats, `abx` automatically keeps a local summary of older conversation turns and combines it with a recent message window when building agent context.
- Live external lookups such as current weather or news are not available in v1.
- If you use a local LM Studio-compatible endpoint with enabled `[[mcp.servers]]`, `abx` will route those requests through LM Studio's MCP-aware chat path for supported tasks such as browser access.
- If `[debug].enabled = true`, normal chat replies also include which configured agent responded.

## Approval Flow

1. Send `/run` if you want to see the usage format
2. Send `/run pwd`
3. `abx` replies with the command plus a single approval line such as `YES abc123`
4. Any trusted participant in that chat may approve with the exact token
5. Any other trusted reply cancels the pending proposal for that chat
6. If the command is allowed by policy, it executes in the configured workspace

## Example

1. Send `/run pwd`
2. `abx` replies with the command plus a single approval line such as `YES abc123`
3. Any trusted participant in that chat may approve with the exact token
4. If the command is allowed by policy, it executes in the configured workspace

## Agent-Assisted `/run`

- You can also use `/run` with a plain-English intent such as `/run show the current user`.
- In that mode, `abx` asks the agent to recommend one minimal shell command and a short explanation.
- If the recommended command passes the current command policy, `abx` turns it into the normal approval flow.
- If the recommended command is blocked by policy, `abx` explains that instead of creating a runnable approval request.

## Control Commands

- `/help`: show a quick summary of supported message types and commands
- `/version`: show the running application version and build metadata when available
- `/config`: show a safe normalized runtime summary of messaging mode, agent contract/model, per-agent request timeouts, optional fallback, thinking-control state, MCP visibility, storage, command policy, debug state, and version
- `/config` also reports whether untrusted-message alerts are enabled, whether previews are forwarded, and the configured rate-limit window without exposing phone numbers

## Untrusted Message Alerts

- If `security.notify_on_untrusted_message = true`, `abx` can forward a short alert to the configured `security.untrusted_message_notify_numbers` when an unknown sender contacts the bot.
- Alert recipients must already be listed in `security.trusted_numbers`.
- The unknown sender is still ignored. The alert does not create a normal conversation session or grant access.
- By default, alerts do not include the unknown sender's message body. Enable `security.untrusted_message_include_preview = true` only if you want a short preview forwarded.
- Repeated messages from the same unknown sender are rate-limited by `security.untrusted_message_rate_limit_seconds`.
- `/agents list`: show the configured primary and optional fallback agents
- `/agents status`: check whether the configured agents are reachable, and show the current session fallback and thinking status when configured
- `/agents reload`: reload agent-related config from disk so updated models and agent settings take effect without restarting the process
- `/agents persona`: show the current session persona, or set it with `/agents persona <instruction>`, or clear it with `/agents persona reset`
- `/agents format`: show the current session format, or set it with `/agents format <instruction>`, or clear it with `/agents format reset`
- `/agents thinking`: show the current session thinking mode, enable it with `/agents thinking enable`, disable it with `/agents thinking disable`, or reset to the agent default with `/agents thinking reset`; changing the mode updates the current session and sends a non-persisted confirmation so the control reply does not contaminate the next model prompt
- `/agents fallback`: show whether fallback is enabled for the current session, disable it with `/agents fallback disable`, or re-enable it with `/agents fallback enable`
- `/agents switch`: switch the active primary and fallback agent order for the current running process
- `/reset`: start a fresh active session for the current chat while preserving historical state
- In group chat, prefix these commands by mentioning the bot in the same message; after mention normalization they follow the same local control flow as in direct chat.

## Images

- If a trusted sender sends a Signal message with image attachments, `abx` can forward those images to a vision-capable model together with the message text.
- Image-only messages are supported.
- This currently depends on `signal-cli` exposing a stored local filename for the image attachment in the inbound JSON-RPC event.
- `abx` does not send generated images or media attachments back to Signal in v1; outbound replies remain text-only.

## Resetting Context

- Send `/reset` to soft-reset the active session
- A reset also starts a fresh summary/history window for future agent requests in that chat
- A reset also clears any session-scoped persona because the next session starts fresh
- A reset also returns the session format to the default plain-text instruction
- A reset also returns the session thinking mode to the configured agent default
- A reset also returns session fallback behavior to the default enabled state
- Previous sessions remain available for audit and diagnostics
