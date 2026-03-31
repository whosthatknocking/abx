# User Guide

## Chat Types

- In direct chats, only trusted numbers may interact with `abx`.
- In group chats, only trusted numbers may interact and the bot must be explicitly mentioned by Signal mention metadata.

## Before Using `abx`

- Make sure your bot Signal account is already registered with `signal-cli`.
- Start `signal-cli` in JSON-RPC daemon mode before starting `abx`.
- By default, `abx` expects a local UNIX socket at `~/.local/share/signal-cli/json-rpc.sock`.

## Message Types

- Normal questions are sent to the configured agent.
- `/version`, `/config`, and `/reset` are built-in control commands.
- `/run <command>` proposes a shell command and requires an approval token before execution.

## Conversational Requests

- Trusted users can ask normal questions such as explanations, summaries, or brainstorming prompts.
- In v1, responses come only from the configured model plus locally stored conversation context.
- Live external lookups such as current weather or news are not available in v1.

## Approval Flow

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
