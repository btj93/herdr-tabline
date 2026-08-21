# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-08-21

First public release. The version reflects early public exposure, not missing
function: the feature set below is complete and verified against Herdr 0.8.2
(protocol 20). Expect the configuration schema to stay at `schema_version = 1`.

### Added

- Go template tab labels with a fixed, sandboxed helper set — no filesystem, environment, or
  network access from a template.
- Project-aware profiles matching on workspace label, tab label, working directory glob,
  foreground process, detected agent, and agent status.
- Three modes: `auto` (the plugin owns the label), `keybind` (write only on request), and
  `off` (observe only).
- Live-order tab renumbering, so `.Tab.Number` follows the tab bar instead of Herdr's raw
  `number`, which does not compact on close or follow a move.
- Event-driven refresh over `events.subscribe` with a polling correctness backstop, replay
  warm-up for stale buffered history, debounced bursts, and reconnect with backoff.
- Per-session state: label history, PID lock, and log filenames are keyed by a hash of the
  socket path, so named sessions never collide.
- Diagnostics: `validate-config` compiles without a socket, `preview` prints the resolved
  profile and quoted label without writing.
- Labels name the detected agent rather than its runtime process. Herdr's own agent
  detection is the only source correct for every agent: the first foreground record
  reads `codex` correctly but `node` or `caffeinate` for Claude, while the
  process-group leader reads `claude` correctly but `node` for Codex.
- Status glyphs match Herdr's own "distinct symbols" indicator set, each one
  individually overridable through `[icons.agent_status]`.
- macOS and Linux support.
