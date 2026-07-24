# Changelog

All notable changes to Pantalk are documented here, following [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.0.11] - 2026-07-24

### Added

- Add a native `acp` driver that runs any Agent Client Protocol server named
  by `command` - for example Kimi Code via `command: kimi acp` - as one
  persistent process with durable per-conversation sessions, optional model
  selection, and a configurable tool-approval policy.
- Add an agent-level `env` map, honored by every driver. Entries are appended
  to the environment the agent process inherits from the daemon, and a `$NAME`
  value is resolved from the daemon's environment at startup so a config can
  name a secret without containing one. Among other things this points the
  claude driver at any endpoint speaking the Anthropic Messages protocol.
- Allow `kimi` as a command-driver binary without `--allow-exec`.

## [0.0.10] - 2026-07-24

### Added

- Add XMPP/Jabber direct-message and multi-user-chat support over SASL and
  StartTLS, including contact presence, XEP-0085 typing indicators, XEP-0199
  server pings, and reporting of whether each configured room accepted the
  join.
- Add Twitch channel chat over IRC/TLS with IRCv3 tags, native replies, and
  OAuth authentication. Outbound messages are paced to 20 per 30 seconds so a
  multi-line agent reply does not trip Twitch's chat ban.
- Add signed Nostr messaging through configurable relays, including NIP-17
  direct messages, NIP-28 channels, and relay-scoped NIP-29 group chat.

### Fixed

- Only conversation messages start an agent turn. Connector lifecycle events -
  status and heartbeat - no longer reach a harness, where a binding written as
  `when: true` spawned a turn on every reconnect.
- Treat a channel ID beginning with "D" as a direct message on Slack only.
  Applied to every provider it misread any channel merely named with a leading
  D, such as a Zulip stream named `design`, and answered every message in it as
  though addressed to the bot.
- Neutralize CR, LF, and NUL in outbound IRC protocol lines, so message text or
  a channel name cannot inject additional IRC commands.

## [0.0.9] - 2026-07-23

### Added

- Publish version-matched Pantalk container images for Linux amd64 and arm64
  through GitHub Container Registry.
- Include a credential-free local connector configuration in the container
  image for immediate smoke testing.

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
