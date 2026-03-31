# ABX Project Specification

## 1. Project Overview

**abx** is a lightweight, single-binary Go application that connects a locally installed signal-cli on macOS to configurable agents (starting with OpenAI-backed agents). The system is fully local, runs natively on macOS, enforces mandatory user approval for every bash command, and uses a standard macOS configuration location.

### Key Constraints for v1:

- Runs on macOS (tested with recent macOS versions + bash)
- Uses locally installed signal-cli (no Docker)
- Configuration file located at `~/.config/abx/config.toml`
- Simple os/exec for bash commands (no sandboxing)
- All commands require explicit approval using a request-bound token such as `YES 482731`
- Single binary, minimal dependencies

## 2. Core Requirements

- **Language & Build**: Go 1.23+, single static binary optimized for macOS (GOOS=darwin GOARCH=arm64 or amd64)
- **Platform**: macOS only for v1. Commands executed via macOS bash
- **Messaging**: `signal-cli` local daemon with JSON-RPC as the primary integration mode
  - In 1:1 chats, respond only when the sender is in `trusted_numbers`
  - In group chats, ignore all messages unless the sender is in `trusted_numbers` and the bot is explicitly mentioned according to Signal mention metadata
- **Agent**: OpenAI as the primary agent provider (with OpenAI-compatible endpoint support for fallback/local LLMs like Ollama)
  - In v1, agent responses must come from the configured model plus locally available conversation context only
  - In v1, external tools for live data retrieval are out of scope
- **Configuration**: TOML file at `~/.config/abx/config.toml`
- **Runtime Model**: `abx` runs as a long-lived local process that continuously handles Signal events, approvals, and command execution
- **Persistence**: Abstracted repository (SQLite default, in-memory for testing)
- **Security**:
  - Trusted Signal numbers only
  - Trusted numbers are necessary but not sufficient; phone numbers are a weak trust anchor on their own
  - Mandatory approval before any bash command execution
  - Full audit logging
- **Command Execution**: Simple `exec.CommandContext` using `/bin/bash` on macOS with configurable timeout and working directory

## 3. High-Level Architecture

```
User (trusted Signal number)
    ↓ E2EE
signal-cli (installed locally on macOS)
    ↓ JSON-RPC events and commands
Go Application `abx` (single macOS binary)
    ├── Config Loader (~/.config/abx/config.toml)
    ├── Messenger: SignalCLIAdapter
    ├── Agent: OpenAIAdapter (primary) + Fallback logic
    ├── Repository (abstracted: SQLite / InMemory)
    ├── Approval State Machine
    └── Command Executor (bash via os/exec)
```

## 4. Configuration

**Default location**: `~/.config/abx/config.toml`

### Example config.toml:

```toml
# Messaging Configuration
[messaging]
provider = "signal-cli"

[messaging.signal_cli]
binary_path = "/usr/local/bin/signal-cli"   # or /opt/homebrew/bin/signal-cli on Apple Silicon
account = "+16505551234"                    # Bot's Signal phone number
data_dir = "~/.local/share/signal-cli"
rpc_mode = "json-rpc"                       # v1 primary mode
rpc_socket = "~/.local/share/signal-cli/json-rpc.sock"
# If using TCP instead of a UNIX socket, configure loopback-only settings such as:
# rpc_host = "127.0.0.1"
# rpc_port = 7583

# Agent Configuration
[agent.primary]
provider = "openai"
api_key = "sk-..."
model = "gpt-4o-mini"

[agent.fallback]
provider = "openai"
base_url = "http://localhost:11434/v1"      # Example: Ollama
model = "llama3.2"

# Security
[security]
trusted_numbers = [
  "+15551234567",
  "+15557654321"
]

# Audit Logging
[audit]
file_path = "~/.local/share/abx/audit.log"
retention_days = 30
max_output_bytes = 8192

# Database
[database]
type = "sqlite"
dsn = "~/.local/share/abx/app.db"

# Command settings (macOS bash)
[command]
timeout_seconds = 60
work_dir = "~/abx/workspace"                # Created automatically if missing
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
    RotateConversationSession(ctx context.Context, conversationID string) (string, error)
    SavePendingApproval(ctx context.Context, conversationID, sessionID string, approval PendingApproval) error
    GetPendingApproval(ctx context.Context, conversationID, requestID string) (*PendingApproval, error)
    GetActivePendingApproval(ctx context.Context, conversationID string) (*PendingApproval, error)
    ClearPendingApproval(ctx context.Context, conversationID, requestID string) error
}

type CommandExecutor interface {
    Execute(ctx context.Context, command string) (string, error)
}
```

## 6. Project Structure (macOS-friendly)

```
abx/
├── cmd/abx/
│   └── main.go
├── internal/
│   ├── config/           # Loads from ~/.config/abx/config.toml
│   ├── agent/
│   │   ├── provider.go
│   │   └── openai/
│   ├── messenger/
│   │   └── signalcli/
│   ├── repository/
│   │   ├── interface.go
│   │   ├── sqlite/
│   │   └── inmemory/
│   ├── approval/
│   ├── executor/         # Simple bash executor for macOS
│   └── handler/
├── pkg/
│   └── types/
├── docs/
│   ├── README.md
│   ├── USER_GUIDE.md
│   └── DEVELOPMENT.md
├── config.toml.example
├── go.mod
└── Makefile              # For easy build & install on macOS
```

## 7. Required Documentation

All project documentation must live under the `docs/` directory. The repository must include the following documentation files as first-class project artifacts:

- **docs/README.md**: Project overview, feature summary, install steps, configuration basics, and quick start instructions.
- **docs/USER_GUIDE.md**: End-user workflow documentation covering trusted number behavior, 1:1 vs group chat behavior, command approval flow, and common usage examples.
- **docs/DEVELOPMENT.md**: Developer-oriented documentation covering project structure, local setup, build/test workflow, configuration details, and implementation notes relevant to contributors.

## 8. Key Behaviors & macOS Specifics

- **Config Loading**: Automatically creates `~/.config/abx/` directory if missing. Uses `os.UserHomeDir()` + TOML parser (`github.com/pelletier/go-toml/v2` recommended).
  - In v1, configuration is file-based only; environment variable overrides are out of scope.

- **signal-cli Integration**:
  - v1 should use `signal-cli daemon` with JSON-RPC as the primary integration mode.
  - `abx` may launch and supervise the local `signal-cli` daemon process, or connect to an already running local daemon.
  - A local UNIX domain socket is preferred for JSON-RPC transport in v1. If a TCP listener is used, it must bind only to loopback.
  - Sending and receiving should be performed through structured JSON-RPC requests/events rather than parsing human-readable CLI output.
  - Signal mention metadata from incoming message events must be used for group-chat activation checks.
  - Assumes `signal-cli` is installed via Homebrew (`brew install signal-cli`) and the account is already registered.
  - If JSON-RPC is exposed over TCP instead of a UNIX socket, it must bind only to `127.0.0.1` and use an explicit configured port.

- **Runtime Operation**:
  - `abx` must be implemented as a long-running local service rather than a one-shot command.
  - In development, `abx` should be runnable in the foreground from a terminal.
  - For normal macOS usage, `abx` should support being run under `launchd` so it can start automatically and restart if it exits unexpectedly.
  - On startup, `abx` should validate configuration, initialize persistence, connect to or launch `signal-cli`, and then begin processing events continuously until shutdown.
  - Shutdown should be graceful: stop receiving new work, finish or cancel in-flight operations safely, and persist any necessary pending state.

- **Message Acceptance Rules**:
  - In a 1:1 conversation, the system must process messages only if the sender number is present in `trusted_numbers`.
  - In a group conversation, the system must ignore messages by default.
  - In a group conversation, the system may consider responding only if both conditions are true:
    1. The sender number is present in `trusted_numbers`.
    2. The message explicitly mentions the bot according to Signal mention metadata exposed by `signal-cli`.
  - Messages from untrusted numbers must never trigger command proposals, approvals, or normal conversational responses in any chat context.

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
  - Only requests that would result in local shell command execution must enter the approval flow.
  - If the system cannot confidently determine whether a request requires shell execution, it should prefer the safer path and require approval before executing any command.

- **Command Execution**: Uses `/bin/bash -c "command"` on macOS. Working directory defaults to `~/abx/workspace`.
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
  - The system must support dedicated slash-style commands such as `/version`, `/config`, and `/reset`.
  - These commands are handled by the application directly and do not require agent inference to route them.
  - `/version` returns the running application version and, if available, build metadata.
  - `/config` returns a safe configuration summary that confirms which agent provider and model are currently configured.
  - `/reset` performs a soft reset of the active conversation session for the current chat context.
  - A soft reset must preserve historical records for audit and diagnostics while starting a fresh active conversation context for future agent requests.
  - `/reset` must clear any active pending approval for the current conversation context and archive or detach the previous active summary/history from the new active session.
  - After `/reset`, the next trusted message in that chat must be handled as the start of a new active conversation session.
  - `/reset` should return a confirmation message such as `Conversation context reset for this chat.`
  - `/config` must never expose secrets such as API keys, tokens, or full sensitive file paths.
  - `/config` may expose only the following fields in v1:
    1. Primary agent provider name
    2. Primary agent model name
    3. Whether a fallback agent is configured
    4. Fallback agent provider name
    5. Fallback agent model name
    6. Application version
  - `/config` must not expose API keys, tokens, raw environment variable values, full filesystem paths, phone numbers, trusted number lists, or exact endpoint URLs.
  - Built-in control commands must follow the same trust rules as all other interactions:
    1. In 1:1 chats, only trusted senders may invoke them.
    2. In group chats, only trusted senders may invoke them, and only when the bot is explicitly mentioned according to Signal mention metadata.

- **Approval Flow**:
  - Agent proposes command → Bot sends confirmation request.
  - Approval must be bound to a specific pending command ID and short-lived nonce.
  - Any trusted sender who is permitted to interact with the system may approve a pending command by replying with the exact approval token for that request (for example `YES 482731`), not a bare `YES`.
  - Any other response cancels the proposal.

- **Persistence**: SQLite file stored under `~/.local/share/abx/app.db` by default.

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

## 10. Non-Goals for v1 (macOS)

- Docker or any containerization
- Multi-platform messaging clients
- Attachment/voice support
- Web UI
- Advanced sandboxing

## 11. Setup Instructions (for `docs/README.md`)

1. **Install signal-cli on macOS**: `brew install signal-cli`
2. **Register your Signal account** with signal-cli (run `signal-cli register +1xxxxxxxxxx` etc.)
3. **Create config directory**: `mkdir -p ~/.config/abx`
4. **Copy and configure**: `config.toml.example` → `~/.config/abx/config.toml` and edit values (especially API key and trusted numbers)
5. **Build**: `go build -o abx ./cmd/abx`
6. **Run**: `./abx` (or move binary to `/usr/local/bin/abx`)
