# Changelog

All notable changes to Pantalk are documented here, following [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.0.8] - 2026-07-23

### Fixed

- Send a safe, actionable reply when a native Codex or Claude Code turn fails
  or completes without a usable response, while retaining detailed errors only
  in daemon logs.

## [0.0.7] - 2026-07-23

### Added

- Add ordered per-bot agent bindings with expression-based routing, reusable
  Codex, Claude, and command agent definitions, and first-match message
  dispatch.
- Add bot-scoped scheduled agent bindings using `at()` and `every()`, IANA
  timezones, stable local schedule conversations, prompts, external
  destinations, and duplicate-minute suppression.
- Add alias-aware channel expressions so Slack routes can match either raw
  channel IDs or friendly `#channel` names.

### Changed

- Move `when` and bot selection from global agent definitions into each bot's
  `agents` list. The old `agents[].bots` and `agents[].when` configuration is
  intentionally rejected.
- Normalize direct-message routing across supported messaging connectors and
  keep DMs independent from ordinary channel allowlists where the provider
  exposes a distinct direct-message type.

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
