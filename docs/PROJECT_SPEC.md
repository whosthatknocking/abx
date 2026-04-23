# ABX Project Specification

## 1. Project Overview

**abx** is a lightweight, single-binary Go application for connecting messaging applications to configurable agent backends. The long-term goal is to keep the messaging layer and agent layer loosely coupled so the product is not defined by any single transport or model provider. In v1, the first concrete implementation is a macOS-native bridge from `signal-cli` to configurable agents, with mandatory approval for every bash command and a standard local configuration location.

### Application Objectives

- Provide a simple, local-first bridge between trusted messaging conversations and agent-assisted workflows.
- Keep messaging transport, agent provider, persistence, and execution concerns modular so the system can evolve beyond any single vendor or protocol.
- Support both conversational assistance and tightly controlled local task execution from a chat interface.
- Default to explicit trust, auditability, and deny-by-default command execution instead of convenience-first automation.
- Stay operationally lightweight: one binary, minimal dependencies, straightforward local configuration, and predictable runtime behavior.

### Key Constraints for v1:

- Runs on macOS (tested with recent macOS versions + bash)
- Uses locally installed signal-cli (no Docker)
- Configuration file located at `${XDG_CONFIG_HOME:-$HOME/.config}/abx/config.toml`
- Simple os/exec for bash commands (no sandboxing)
- All commands require explicit approval using a request-bound token such as `YES 482731`
- Single binary, minimal dependencies

## 2. Core Requirements

- **Language & Build**: Go 1.23+, single static binary optimized for macOS (GOOS=darwin GOARCH=arm64 or amd64)
- **Platform**: macOS only for v1. Commands executed via macOS bash
- **Messaging**: Messaging integration should be transport-agnostic at the architecture level. In v1, `signal-cli` local daemon with JSON-RPC is the primary implementation.
  - In 1:1 chats, respond only when the sender is in `trusted_numbers`
  - In group chats, ignore all messages unless the sender is in `trusted_numbers` and the bot is explicitly mentioned according to Signal mention metadata
- **Agent**: OpenAI-compatible chat APIs as the primary agent contract, with a native LM Studio `/api/v1/chat` path used for local MCP-enabled endpoints
  - In v1, agent responses must come from the configured model plus locally available conversation context only
  - In v1, external tools for live data retrieval are out of scope
- **Configuration**: TOML file at `${XDG_CONFIG_HOME:-$HOME/.config}/abx/config.toml`
- **Runtime Model**: `abx` runs as a long-lived local process that continuously handles messaging events, approvals, and command execution
- **Persistence**: Abstracted repository (SQLite default, in-memory for testing)
- **Security**:
  - Trusted Signal numbers only
  - Trusted numbers are necessary but not sufficient; phone numbers are a weak trust anchor on their own
  - Mandatory approval before any bash command execution
  - Full audit logging
- **Command Execution**: Simple `exec.CommandContext` using `/bin/bash` on macOS with configurable timeout and working directory

## 3. High-Level Architecture

```
User (trusted messaging identity)
    ↓ E2EE
Messaging Adapter (v1: signal-cli installed locally on macOS)
    ↓ JSON-RPC events and commands
Go Application `abx` (single macOS binary)
    ├── Config Loader (${XDG_CONFIG_HOME:-$HOME/.config}/abx/config.toml)
    ├── Messenger Adapter (v1: SignalCLIAdapter)
    ├── Agent: OpenAIAdapter (primary) + Fallback logic
    ├── Repository (abstracted: SQLite / InMemory)
    ├── Approval State Machine
    └── Command Executor (bash via os/exec)
```

## 4. Configuration

**Default location**: `${XDG_CONFIG_HOME:-$HOME/.config}/abx/config.toml`

### Example config.toml:

```toml
# Messaging Configuration
[messaging]
provider = "signal-cli"

[messaging.signal_cli]
binary_path = "/usr/local/bin/signal-cli"   # or /opt/homebrew/bin/signal-cli on Apple Silicon
account = "+16505551234"                    # Bot's Signal phone number
# Defaults to $XDG_DATA_HOME/signal-cli or ~/.local/share/signal-cli.
# data_dir = "/absolute/path/to/signal-cli-data"
rpc_mode = "json-rpc"                       # v1 primary mode
# Defaults to <data_dir>/json-rpc.sock when rpc_mode = "json-rpc".
# rpc_socket = "/absolute/path/to/signal-cli-data/json-rpc.sock"
# If using TCP instead of a UNIX socket, configure loopback-only settings such as:
# rpc_host = "127.0.0.1"
# rpc_port = 7583

# Agent Configuration
[agent.primary]
provider = "openai"
api_key = "sk-..."
model = "gpt-4o-mini"
request_timeout_seconds = 180

[agent.fallback]
provider = "openai"
base_url = "http://localhost:11434/v1"      # Example: Ollama
model = "llama3.2"
request_timeout_seconds = 60

# Optional MCP integrations for local OpenAI-compatible agents
[mcp]

[[mcp.servers]]
name = "mcp/playwright"
enabled = true

[[mcp.servers]]
name = "mcp/weather"
enabled = false

# Security
[security]
trusted_numbers = [
  "+15551234567",
  "+15557654321"
]
notify_on_untrusted_message = false
untrusted_message_notify_numbers = ["+15551234567"]
untrusted_message_include_preview = false
untrusted_message_rate_limit_seconds = 900

# Audit Logging
[audit]
# Defaults to $XDG_STATE_HOME/abx/audit.log or ~/.local/state/abx/audit.log.
# file_path = "/absolute/path/to/abx/audit.log"
retention_days = 30
max_output_bytes = 8192

# Database
[database]
type = "sqlite"
# Defaults to $XDG_STATE_HOME/abx/app.db or ~/.local/state/abx/app.db.
# dsn = "/absolute/path/to/abx/app.db"

# Command settings (macOS bash)
[command]
timeout_seconds = 60
# Defaults to $XDG_STATE_HOME/abx/workspace or ~/.local/state/abx/workspace.
# work_dir = "/absolute/path/to/abx/workspace"  # Created automatically if missing
policy_mode = "allowlist"                   # v1 default: deny by default, allow only explicit matches

[[command.policy.rules]]
id = "allow-pwd"
enabled = true
action = "allow"
match_type = "exact"                        # exact, prefix, contains, or regex
pattern = "pwd"
description = "Allow checking the current working directory"

[[command.policy.rules]]
id = "allow-ls"
enabled = true
action = "allow"
match_type = "regex"
pattern = '^ls( .*)?$'
description = "Allow basic directory listing commands"

[[command.policy.rules]]
id = "allow-git-status"
enabled = true
action = "allow"
match_type = "exact"
pattern = "git status"
description = "Allow read-only git status inspection"
```

## 5. Pluggable Interfaces

```go
// Core interfaces (internal/)
type AgentProvider interface {
    Chat(ctx context.Context, messages []Message, tools []Tool) (AgentResponse, error)
}

type Messenger interface {
    Start(ctx context.Context, handler func(Message)) error
    Send(ctx context.Context, recipient, text string) error
}

type Repository interface {
    SaveMessage(ctx context.Context, conversationID, sessionID string, msg Message) error
    GetActiveSessionID(ctx context.Context, conversationID string) (string, error)
    GetHistory(ctx context.Context, conversationID, sessionID string, limit int) ([]Message, error)
    GetActiveHistory(ctx context.Context, conversationID string, limit int) ([]Message, error)
    SaveConversationSummary(ctx context.Context, conversationID, sessionID, summary string) error
    GetConversationSummary(ctx context.Context, conversationID, sessionID string) (string, error)
    GetActiveConversationSummary(ctx context.Context, conversationID string) (string, error)
    SaveSessionPersona(ctx context.Context, conversationID, sessionID, persona string) error
    GetSessionPersona(ctx context.Context, conversationID, sessionID string) (string, error)
    SaveSessionFormat(ctx context.Context, conversationID, sessionID, format string) error
    GetSessionFormat(ctx context.Context, conversationID, sessionID string) (string, error)
    SaveSessionThinkingMode(ctx context.Context, conversationID, sessionID, mode string) error
    GetSessionThinkingMode(ctx context.Context, conversationID, sessionID string) (string, error)
    SaveSessionFallbackDisabled(ctx context.Context, conversationID, sessionID string, disabled bool) error
    GetSessionFallbackDisabled(ctx context.Context, conversationID, sessionID string) (bool, error)
    RotateConversationSession(ctx context.Context, conversationID string) (string, error)
    SavePendingApproval(ctx context.Context, conversationID, sessionID string, approval PendingApproval) error
    GetPendingApproval(ctx context.Context, conversationID, requestID string) (*PendingApproval, error)
    GetActivePendingApproval(ctx context.Context, conversationID string) (*PendingApproval, error)
    ClearPendingApproval(ctx context.Context, conversationID, requestID string) error
}

type CommandExecutor interface {
    Check(command string) error
    Execute(ctx context.Context, command string) (string, error)
}
```

## 6. Project Structure (macOS-friendly)

```
abx/
├── cmd/abx/
│   └── main.go
├── internal/
│   ├── config/           # Loads from ${XDG_CONFIG_HOME:-$HOME/.config}/abx/config.toml
│   ├── agent/
│   │   ├── provider.go
│   │   └── openai/
│   ├── messenger/
│   │   └── signalcli/
│   ├── repository/
│   │   ├── interface.go
│   │   ├── sqlite/
│   │   └── inmemory/
│   ├── executor/         # Simple bash executor for macOS
│   └── handler/
├── pkg/
│   └── types/
├── docs/
│   ├── PROJECT_SPEC.md
│   ├── README.md
│   ├── USER_GUIDE.md
│   └── DEVELOPMENT.md
├── config.toml.example
├── go.mod
└── Makefile              # For easy build & install on macOS
```

## 7. Required Documentation

All project documentation must live under the `docs/` directory. The repository must include the following documentation files as first-class project artifacts:

- **docs/PROJECT_SPEC.md**: Product and implementation specification covering objectives, architecture, security, configuration, and milestone scope.
- **docs/README.md**: Project overview, feature summary, install steps, configuration basics, and quick start instructions.
- **docs/USER_GUIDE.md**: End-user workflow documentation covering trusted number behavior, 1:1 vs group chat behavior, command approval flow, and common usage examples.
- **docs/DEVELOPMENT.md**: Developer-oriented documentation covering project structure, local setup, build/test workflow, configuration details, and implementation notes relevant to contributors.

## 8. Key Behaviors & macOS Specifics

- **Config Loading**: Automatically creates `${XDG_CONFIG_HOME:-$HOME/.config}/abx/` if missing and falls back to the home-based XDG path when `XDG_CONFIG_HOME` is unset.
  - In v1, configuration is file-based only; environment variable overrides are out of scope.

- **signal-cli Integration**:
  - v1 should use `signal-cli daemon` with JSON-RPC as the primary integration mode.
  - In v1, `abx` should connect to an already running local `signal-cli` daemon.
  - A local UNIX domain socket is preferred for JSON-RPC transport in v1. If a TCP listener is used, it must bind only to loopback.
  - Sending and receiving should be performed through structured JSON-RPC requests/events rather than parsing human-readable CLI output.
  - Signal mention metadata from incoming message events must be used for group-chat activation checks.
  - Assumes `signal-cli` is installed via Homebrew (`brew install signal-cli`) and the account is already registered.
  - If JSON-RPC is exposed over TCP instead of a UNIX socket, it must bind only to `127.0.0.1` and use an explicit configured port.

- **Runtime Operation**:
  - `abx` must be implemented as a long-running local service rather than a one-shot command.
  - In development, `abx` should be runnable in the foreground from a terminal.
  - On startup, `abx` should validate configuration, initialize persistence, connect to the configured `signal-cli` daemon, and then begin processing events continuously until shutdown.
  - Shutdown should be graceful: stop receiving new work, finish or cancel in-flight operations safely, and persist any necessary pending state.

- **Message Acceptance Rules**:
  - In a 1:1 conversation, the system must process messages only if the sender number is present in `trusted_numbers`.
  - In a group conversation, the system must ignore messages by default.
  - In a group conversation, the system may consider responding only if both conditions are true:
    1. The sender number is present in `trusted_numbers`.
    2. The message explicitly mentions the bot according to Signal mention metadata exposed by `signal-cli`.
  - In a group conversation, built-in slash commands and `/run` must remain available under the same semantics as direct chat once that explicit mention gate is satisfied.
  - Transport-specific mention prefixes or placeholders may be normalized before command routing, but the explicit mention requirement must still be enforced from transport metadata rather than plain text alone.
  - Messages from untrusted numbers must never trigger command proposals, approvals, or normal conversational responses in any chat context.
  - When explicitly enabled in config, a message from an untrusted number may trigger a separate notification to configured trusted recipients, but that alert must not create a conversation session for the untrusted sender or send any reply back to that sender.

- **Message Classification**:
  - Not all inbound messages are shell-command requests.
  - Trusted messages may be handled in one of three categories:
    1. Normal conversational agent requests
    2. Built-in control commands such as `/version` and `/config`
    3. Shell-command execution requests that require explicit approval
  - Normal conversational agent requests should be answered directly by the configured agent without entering the shell command approval flow.
  - Examples of normal conversational requests include questions such as asking for an explanation, summary, brainstorming help, or general knowledge assistance.
  - In v1, normal conversational agent requests must be answered from the configured model and locally persisted conversation context only.
  - In v1, the system must not invoke external tools or live network lookups for informational questions such as weather, news, or search.
  - If a user asks for current or external information that is not available from local context, the system should respond transparently that live external lookup is not enabled in v1.
  - In v1, shell-command execution should enter through the dedicated `/run` flow rather than implicit command inference from arbitrary conversational messages.
  - `/run` should support both:
    1. direct exact commands such as `/run pwd`
    2. plain-English intent requests such as `/run show the current user`
  - For a plain-English `/run` intent, the agent may recommend exactly one shell command plus a short rationale.
  - Slash-prefixed commands must be routed by the application locally first; `/run` may then invoke the agent only as part of its local recommendation workflow.
  - Only requests that would result in local shell command execution must enter the approval flow.
  - If the system cannot confidently determine whether a request requires shell execution, it should prefer the safer path and require approval before executing any command.

- **Command Execution**: Uses `/bin/bash -c "command"` on macOS. Working directory defaults to `${XDG_STATE_HOME:-$HOME/.local/state}/abx/workspace`.
  - v1 command execution must be deny-by-default.
  - A command may execute only if it matches at least one enabled `allow` policy rule and does not match any enabled `deny` policy rule.
  - Rule evaluation must happen before any approval prompt is considered complete.
  - Policy rule syntax must be validated at startup, and invalid rules must fail fast rather than being ignored silently.
  - Startup validation must treat the following as fatal configuration errors:
    1. Duplicate rule `id` values
    2. Unknown `match_type` values
    3. Invalid regular expressions for `regex` rules
    4. Missing required rule fields
    5. `allowlist` mode with zero enabled `allow` rules

- **Conversation Session Management**:
  - `abx` must maintain conversation session state locally rather than relying on provider-side session memory.
  - SQLite is the default system of record for conversation state in v1.
  - Each conversation must have a stable local `conversationID`.
  - Each conversation must also have a `sessionID` for the active conversation session.
  - A `sessionID` represents one active context window within a stable `conversationID`.
  - For a 1:1 chat, the `conversationID` should be derived from the bot account and the trusted sender.
  - For a group chat, the `conversationID` should be derived from the Signal group identity.
  - Local session state should include recent message history, optional conversation summaries, pending approvals, timestamps, and message metadata needed for trust and routing decisions.
  - On each agent request, `abx` should reconstruct the prompt context from the active `sessionID` within the current `conversationID`.
  - Normal runtime history operations should use the active session by default.
  - Historical sessions must remain queryable for audit and diagnostics after a soft reset.
  - Built-in control commands may be excluded from agent-visible conversation history unless needed for audit or diagnostics.
  - Command proposals, approval decisions, and command results should be stored locally, but command output included in agent context should be selectively truncated or summarized.
  - The implementation should support bounded history windows and optional summarization so long-running conversations do not grow without limit.
  - After restart, `abx` must be able to resume conversation handling from local persisted state.

- **Built-in Control Commands**:
  - The system must support dedicated slash-style commands such as `/help`, `/version`, `/config`, `/agents list`, `/agents status`, `/agents reload`, `/agents persona`, `/agents format`, `/agents thinking`, `/agents fallback`, `/agents switch`, and `/reset`.
  - These commands are handled by the application directly and do not require agent inference to route them.
  - `/help` returns a concise summary of the supported message types and built-in commands.
  - `/version` returns the running application version and, if available, build metadata.
  - `/config` returns a safe normalized runtime summary of the active messaging, agent, MCP, storage, and command-policy configuration.
  - `/agents reload` reloads agent-related configuration from disk for the running process when the runtime supports safe reload.
  - `/agents persona` manages a session-scoped persona instruction that is stored locally and prepended to future agent requests for the active session.
  - `/agents persona` with no argument returns the currently active session persona, if any.
  - `/agents persona <instruction>` stores or replaces the active session persona for that chat session.
  - `/agents persona reset` clears the active session persona.
  - When no custom persona is active for a session, future conversational agent requests should include an explicit neutral instruction to respond normally and not continue any previously used persona implicitly from older turns.
  - `/agents format` manages a session-scoped response-format instruction that is stored locally and prepended to future agent requests for the active session.
  - `/agents format` with no argument returns the currently active session format instruction.
  - `/agents format <instruction>` stores or replaces the active session format instruction for that chat session.
  - `/agents format reset` returns the active session format instruction to the default plain-text format.
  - `/agents thinking` manages a session-scoped thinking override for the active session when thinking control is configured for at least one active agent.
  - `/agents thinking` with no argument returns whether the session is using the agent default thinking behavior or an explicit enabled or disabled override.
  - `/agents thinking enable` and `/agents thinking disable` store a transient session override without polluting agent-visible chat history.
  - `/agents thinking reset` clears the session override and returns to the configured agent default.
  - `/agents fallback` manages a session-scoped fallback setting for agent requests in the active session.
  - `/agents fallback` with no argument returns whether fallback is currently enabled for that session.
  - `/agents fallback disable` disables fallback for future agent requests in that session, so only the primary agent is used.
  - `/agents fallback enable` re-enables fallback for future agent requests in that session.
  - `/agents list` returns the configured primary agent and, when present, the configured fallback agent.
  - `/agents status` checks whether each configured agent is reachable and returns a bounded live status summary that also includes the current session fallback state and, when thinking control is configured, the current session thinking state.
  - `/agents switch` swaps the active primary and fallback agent order for the running process.
  - `/reset` performs a soft reset of the active conversation session for the current chat context.
  - A soft reset must preserve historical records for audit and diagnostics while starting a fresh active conversation context for future agent requests.
  - `/reset` must clear any active pending approval for the current conversation context and archive or detach the previous active summary/history from the new active session.
  - `/reset` must also clear any active session persona, restore the session format to its default plain-text instruction, restore session fallback behavior to enabled, and return session thinking mode to the configured agent default by virtue of creating a new active session with fresh session-scoped state.
  - After `/reset`, the next trusted message in that chat must be handled as the start of a new active conversation session.
  - `/reset` should return a confirmation message such as `Conversation context reset for this chat.`
  - All session-scoped prompt assembly, including normal conversational requests and agent-assisted `/run` command recommendations, must use the same resolved session ID that the inbound message was stored under rather than reloading prompt state from a different active-session pointer later in the request.
  - `/config` must never expose secrets such as API keys, tokens, or full sensitive file paths.
  - `/config` may expose only the following fields in v1:
    1. Messaging provider name
    2. Messaging runtime mode
    3. Primary agent model name
    4. Primary agent contract name
    5. Primary agent request timeout
    6. Optional fallback agent model name
    7. Optional fallback agent contract name
    8. Optional fallback agent request timeout
    9. Thinking-control configured/not-configured state and effective default mode summary
    10. MCP enabled/disabled state and enabled MCP server names
    11. Storage backend name
    12. Command policy mode
    13. Command timeout
    14. Untrusted-message alert enabled/disabled state plus safe summary metadata such as recipient count, preview enabled/disabled state, and rate-limit window
    15. Debug enabled/disabled state
    16. Application version
  - `/config` values should be normalized to their effective runtime values where defaults apply.
  - The fallback section is optional and should be omitted entirely when no fallback agent is configured.
  - In v1, the primary and fallback contract names should describe the agent API contract in use, such as `openai-compatible`, rather than exposing internal locality labels.
  - `/config` must not expose API keys, tokens, raw environment variable values, full filesystem paths, phone numbers, trusted number lists, or exact endpoint URLs.
  - Built-in control commands must follow the same trust rules as all other interactions:
    1. In 1:1 chats, only trusted senders may invoke them.
    2. In group chats, only trusted senders may invoke them, and only when the bot is explicitly mentioned according to Signal mention metadata.

- **Approval Flow**:
  - `/run` receives an exact command.
  - If the `/run` payload is already an executable command under the current policy, the bot should propose that exact command directly.
  - If the `/run` payload is not an exact command, the bot should reject it locally with usage guidance rather than invoking the conversational agent.
  - The bot then sends the confirmation request for the exact command that would be executed.
  - Approval must be bound to a specific pending command ID and short-lived nonce.
  - Any trusted sender who is permitted to interact with the system may approve a pending command by replying with the exact approval token for that request (for example `YES 482731`), not a bare `YES`.
  - Any other trusted response cancels the proposal for that chat.
  - After cancellation, the same message should continue through normal message classification and be handled as a conversational request, built-in control command, or a new shell-command request as appropriate.

- **Persistence**: SQLite file stored under `${XDG_STATE_HOME:-$HOME/.local/state}/abx/app.db` by default.

- **Logging**: Console + optional file logging. Structured output suitable for macOS Terminal.
  - Audit logs must be written to a persistent local file in addition to console output.
  - Audit logs must record request ID, conversation ID, sender number, message type, proposed command, approval decision, approver number, timestamps, exit status, and whether output was truncated.
  - Audit logs must redact secrets when known config keys or token-like values are detected.
  - Command stdout/stderr captured in audit logs must be truncated to a bounded size in v1 to reduce accidental secret retention.
  - Audit log retention, file path, and maximum captured output length must be configurable.

## 9. Security Risks & Mitigations

The v1 trust model must not rely on a phone number alone. Signal reduces casual sender spoofing compared with SMS, but a trusted number is still a weak long-term identity signal. If a trusted user's phone, linked desktop, or Signal registration is compromised, an attacker may inherit the same command and approval power as the legitimate user.

### Key Risks

- **Compromised trusted endpoint**: Anyone with control of a trusted Signal account or linked device can issue and approve commands.
- **Replay / stale token risk**: An approval token can accidentally approve the wrong command if pending approval state is ambiguous, delayed, reused, or not bound tightly enough to the original request.
- **Conversation confusion**: Approvals that are keyed only by phone number may be misapplied across multiple pending requests or chat contexts.
- **Group chat accidental activation**: Without an explicit mention requirement, a trusted sender in a noisy group could trigger unintended bot responses.
- **Number lifecycle risk**: Phone numbers can be reassigned over time, so historical trust in a number may become invalid.

### Required Mitigations for v1

- **Bind approvals to a command ID**: Each proposed command must have a unique request ID and nonce, and approval must reference that exact request.
- **Short approval expiry**: Pending approvals should expire automatically after a short window (for example 5 minutes).
- **Single-use approvals**: Once a request is approved, rejected, or expired, it must not be reusable.
- **One pending approval per conversation context**: Reject or cancel older pending requests before creating a new one for the same chat or conversation context.
- **Strict chat-context validation**: Bind pending approvals to the specific request ID and conversation context so a reply in another chat cannot approve the request.
- **Trusted approver validation**: Any approval must come from a sender who is currently allowed to interact with the system under the configured trust rules for that chat context.
- **Explicit group mention gate**: In group chats, require a valid Signal mention targeting the bot before the bot evaluates the message.
- **Structured audit log**: Record sender number, request ID, proposed command, approval text, decision, and timestamps.
- **Safe configuration disclosure**: Built-in commands such as `/config` may confirm active provider/model selection, but must redact secrets and other sensitive configuration values.
- **Command policy guardrails**: Apply explicit deny/allow rules for especially dangerous shell patterns even after approval.
  - In v1, the command policy must be config-driven and evaluated before execution.
  - In v1, the default and recommended mode is `allowlist`, which blocks all commands unless they match an explicit `allow` rule.
  - The command policy must use structured rule objects rather than ambiguous raw pattern lists.
  - Each rule object must contain:
    1. `id`: stable identifier used in logs and diagnostics
    2. `enabled`: whether the rule is active
    3. `action`: `allow` or `deny`
    4. `match_type`: one of `exact`, `prefix`, `contains`, or `regex`
    5. `pattern`: the match value
    6. `description`: human-readable explanation of the rule
  - Rule semantics must be clearly defined:
    1. `exact`: full command string must match exactly
    2. `prefix`: command string must start with the pattern
    3. `contains`: command string must contain the pattern
    4. `regex`: command string must match the configured regular expression
  - If multiple rules match, any matching `deny` rule must override matching `allow` rules.
  - At minimum, v1 must support denying commands or patterns involving destructive filesystem operations, privilege escalation, network exfiltration tooling, and shell redirection patterns that write outside the configured workspace.
  - If a command matches a blocked rule, the system must refuse execution even if a trusted user approves it.

### Future Hardening (Post-v1)

- Verify Signal-specific identity material exposed by `signal-cli`, if available, instead of trusting only the phone number.
- Add an out-of-band confirmation step for high-risk commands.
- Require stronger operator authentication for sensitive command classes.

## 10. Future Milestones

### MCP Integration (Post-v1 / Transitional Local Support)

- `abx` should support optional Model Context Protocol (MCP) integration as a future extension for safe, structured tool and context access.
- MCP is not required for the core messaging, approval, or shell-command execution flow in v1.
- In the current implementation, `abx` may forward configured MCP integration names to compatible local agent endpoints, but this should be treated as transitional local-agent support rather than complete MCP product support.
- The goal of MCP support is to give agents access to read-oriented capabilities that do not fit the shell approval model well, such as:
  1. Current local date and time
  2. Weather and other live informational lookups
  3. Calendar or reminder data
  4. Local file or project metadata
  5. Other structured internal data sources
- MCP-enabled capabilities should be explicitly configured and disabled by default.
- MCP server definitions should be stored in `config.toml`, with each server independently enabled or disabled by configuration.
- Full MCP tool-call auditing is a post-v1 requirement. The current implementation may log and audit the surrounding chat request, but it does not yet provide first-class per-tool MCP execution audit records.
- MCP access control must be independent from shell command approval. Read-only MCP tools should not automatically inherit permission to execute shell commands.
- Future MCP design should prefer a small allowlisted set of read-only tools before any broader tool execution model is considered.
- The system should clearly distinguish between:
  1. Local prompt/runtime context injected by `abx`
  2. MCP-backed structured context or tool calls
  3. Shell commands that require explicit approval
- If MCP support is added, `/config` should indicate whether MCP is enabled and which tool families are configured, without exposing secrets or sensitive endpoints.
- Initial post-v1 MCP support should prioritize safe utility tools such as `time.now`, `weather.current`, and `system.version` before higher-risk integrations.

### Runtime Packaging (Post-v1)

- `abx` should eventually be able to launch and supervise the local `signal-cli` daemon instead of requiring it to be pre-started by the operator.
- `abx` should eventually support a `launchd` plist and installation workflow for normal macOS background-service usage.
- Future packaging work should define restart policy, stdout/stderr capture, and operator-facing install/update commands for both `abx` and `signal-cli`.

## 11. Non-Goals for v1 (macOS)

- Docker or any containerization
- Multi-platform messaging clients
- Attachment/voice support
- Web UI
- Advanced sandboxing

## 12. Setup Instructions (for `docs/README.md`)

1. **Install signal-cli on macOS**: `brew install signal-cli`
2. **Register your Signal account** with signal-cli (run `signal-cli register +1xxxxxxxxxx` etc.)
3. **Create config directory**: `mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/abx"`
4. **Copy and configure**: `config.toml.example` → `${XDG_CONFIG_HOME:-$HOME/.config}/abx/config.toml` and edit values.
   - Configure trusted numbers and command allow rules.
   - For remote OpenAI use, configure `agent.primary.api_key` and `agent.primary.model`.
   - For local OpenAI-compatible endpoints such as LM Studio, configure `base_url` and `model`; `api_key` may be omitted if the local endpoint does not require one.
   - If you want local MCP-style integrations with a compatible local agent endpoint, enable the desired `[[mcp.servers]]` entries.
5. **Start signal-cli daemon**: Run `signal-cli daemon` in JSON-RPC mode before starting `abx`.
6. **Build**: `make build`
7. **Run**: `./abx` (or move binary to `/usr/local/bin/abx`)
