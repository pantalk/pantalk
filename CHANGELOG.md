# Changelog

All notable changes to Pantalk are documented here, following [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.0.12] - 2026-07-26

### Security

- Stop leaking the daemon's environment into agent processes. Every driver
  previously started its agent with the daemon's environment inherited, which
  handed each agent every bot credential the config had resolved through
  `$NAME` - so a prompt injection arriving over any one service could read the
  tokens for all of them, and a secret named by one agent definition was
  readable by every other. An agent process now receives exactly the
  environment its own definition configures.

### Added

- Add an agent-level `env_inherit` list naming the daemon variables an agent
  should receive, since nothing is inherited by default. A name ending in `*`
  copies every variable with that prefix, an unset name is skipped, and an
  explicit `env` entry wins over an inherited one.
- Allow `zot` without `--allow-exec`. [zot](https://github.com/chatbotkit/zot)
  serves the Agent Client Protocol via `command: zot acp`, so the `acp` driver
  can drive it like any other conforming agent. The allowlist named in the
  error message is now derived from the allowlist itself, so the two cannot
  drift apart.

- Add agent `isolation`, which runs a harness in a container instead of
  alongside the daemon. `isolation: container` is the shorthand; the block form
  takes `mode`, `image`, `workspace` and `runtime` (docker or podman). Pantalk
  compiles the invocation itself, so the agent still names its harness, the
  allowlist still checks that harness rather than the container runtime, and
  `--allow-exec` is not required. Each isolated agent gets its own workspace
  volume. Credentials are forwarded by name, never as `--env NAME=value`, so a
  token never appears in an argv other users on the host can read.
- Disable Codex's own sandbox for isolated agents, since the container is
  already the boundary and Codex's bubblewrap needs user namespaces an
  unprivileged container usually denies. An explicit `codex.sandbox` still wins.
- Accept `command` on every driver as an override of the binary it would
  otherwise run, with the driver still appending its own protocol arguments.
  This covers wrappers, pinned versions, and paths that exist only inside a
  container image. It conflicts with `codex.binary` / `claude.binary`, and an
  overriding command faces the same allowlist as the `command` driver.
- Publish a nostr kind-0 profile on connect, so a bot appears under a name
  instead of a bare public key. New optional `display_name`, `about` and
  `picture` bot fields supply the metadata; `display_name` falls back to the
  bot `name`.
- Announce nostr presence (kind-20001) on connect and refresh it on the
  connector heartbeat, and implement `TypingIndicator` for NIP-29 groups
  (kind-20002). Both are best-effort: relays outside the NIP-29 liveness
  convention reject these kinds, which is logged rather than fatal.

### Fixed

- Keep the nostr session alive when a relay rejects the kind-10050 DM relay
  list. Relays that accept only a fixed kind set — Buzz among them — refused
  the list with `restricted: unknown event kind`, which aborted the session
  before any subscription was made and left the connector reconnecting
  forever. NIP-17 inbox discovery is skipped on those relays; NIP-28 channels
  and NIP-29 groups now work against them.
- Stop refusing an ACP session when the agent does not implement
  `session/set_model`. That method is an unstable part of the protocol, and an
  agent that resolves its own model answers it with "method not found" - which
  the client treated as fatal, so setting `acp.model` locked out every
  conforming agent that does not implement it. The unsupported case is now
  logged once and the session proceeds on the agent's own model; every other
  `session/set_model` failure still fails the session, since an agent that
  implements the method and rejects the model is a real configuration error.
- Escape newlines, carriage returns, and tabs in an iMessage recipient, not
  just in the message body. AppleScript string literals cannot span lines, so
  a recipient containing one of those characters made osascript fail to
  compile the send script. Both values now share one escaping helper.

### Changed

- **Breaking:** an agent process no longer inherits `PATH`, `HOME`, or any
  other daemon variable. Definitions that rely on local CLI authentication,
  settings, skills, or MCP configuration must now name what they need, for
  example `env_inherit: [PATH, HOME, USER, SHELL, TMPDIR, TZ, LANG, 'LC_*']`.
  `HOME` is the one that matters most - the claude, codex, and acp drivers all
  locate local credentials through it.

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
