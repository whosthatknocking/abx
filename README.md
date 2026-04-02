# abx

`abx` is a local-first service for connecting messaging apps to configurable AI agents, with support for controlled interactions with the local system.

The project is designed to stay transport-agnostic and provider-agnostic over time, while the current v1 implementation is macOS-first and uses Signal via `signal-cli` as the initial messaging adapter.

## What It Does

- Bridges trusted messaging conversations to agent-backed assistance
- Supports conversational requests and controlled local task execution
- Compacts longer chats with local conversation summaries plus recent context
- Uses deny-by-default shell execution with explicit approval
- Keeps conversation state, configuration, and runtime local

## Documentation

- [Project Overview](./docs/README.md)
- [Project Specification](./docs/PROJECT_SPEC.md)
- [User Guide](./docs/USER_GUIDE.md)
- [Development](./docs/DEVELOPMENT.md)

## Versioning

`abx` uses the checked-in `VERSION` file as the default build version source. GitHub releases are published by pushing a matching tag such as `v0.1.0`.

## License

MIT, see [LICENSE](./LICENSE).
