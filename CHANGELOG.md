# Changelog

All notable changes to Pantalk are documented here, following [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.0.6] - 2026-07-23

### Added

- Add a credential-free local connector with explicit inbound injection and interactive chat commands.
- Add a native persistent Codex app-server driver with thread start/resume, final-response delivery, cancellation, and safe approval-request handling.
- Add a native Claude Code driver with streaming-JSON result handling and durable session resume through the locally configured CLI.
- Add durable per-agent conversation-to-runtime-session mappings in SQLite.
- Add `pantalk local`, a zero-config interactive Codex or Claude testing mode with safe defaults, persistent local sessions, and an optional ephemeral state mode.

### Changed

- Allow agents to select a `command`, `codex`, or `claude` driver and attach explicitly to configured bots.
- Derive agent conversation scope from provider, bot, DM, channel, and thread identifiers instead of exposing a global session-scope setting.
- Prevent bot-originated local messages from notifying agents or creating response loops.

## [0.0.5] - 2026-07-23

### Added

- Add attachment storage and Telegram file handling.
- Add emoji reactions and typing indicators for supported messaging platforms.
- Add formatted message rendering and segmentation across connectors.

### Changed

- Use a pure-Go SQLite driver so release binaries remain portable static executables.
- Update Go and networking dependencies.

### Fixed

- Ensure packaged `pantalkd` binaries can open and initialize their SQLite database.
