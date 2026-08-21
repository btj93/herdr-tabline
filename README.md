# Herdr Tabline

Render Herdr tab labels from Go templates, with project-aware profiles, live-order tab
numbering, and event-driven refresh backed by a polling safety net.

A tab label becomes something like ` 2: api > codex ` — position, directory, and whatever
is actually running in the tab's focused pane.

## Requirements

- Herdr 0.7.0 or newer, running with its control socket available.
- Go 1.22 or newer to build.
- macOS or Linux.

## Install

Link the plugin from a local directory. Point Herdr at wherever you cloned or copied it:

```bash
herdr plugin link --path /path/to/herdr-tabline
```

Herdr runs the manifest's build step, producing `bin/herdr-tabline`. Confirm the plugin is
registered:

```bash
herdr plugin list
```

## Quick start

Copy the example configuration into the plugin's config directory and validate it:

```bash
cp config.example.toml "$HERDR_PLUGIN_CONFIG_DIR/config.toml"
herdr-tabline validate-config
```

With no configuration file at all the plugin uses built-in defaults, which reproduce the
label format ` {{ .Tab.Number }}: {{ .Pane.Directory }} > {{ .Process.Name }} `.

Commands:

| Command | Purpose |
|---|---|
| `start` | Start the background daemon for this session. Safe to call repeatedly. |
| `stop` | Signal this session's daemon to exit. |
| `rename-current` | Render and apply the label for one tab, now. |
| `validate-config` | Compile the configuration and report problems. Needs no socket. |
| `preview` | Print the resolved profile and label for one tab without renaming anything. |
| `version` | Print `herdr-tabline 1.0.0 schema 1`. |

`daemon` also exists but is internal: `start` invokes it in a detached process. It is
deliberately absent from the manifest's action list.

### Environment

The plugin reads only what Herdr injects:

- `HERDR_SOCKET_PATH` — the control socket. Required by everything except `validate-config`.
- `HERDR_PLUGIN_CONFIG_DIR` — directory holding `config.toml`.
- `HERDR_PLUGIN_STATE_DIR` — directory for per-session label state, PID lock, and log.
- `HERDR_TAB_ID` — the invoking tab. Nullable upstream; see below.
- `HERDR_PLUGIN_CONTEXT_JSON` — the full invocation context.

The target tab is resolved in three steps: `HERDR_TAB_ID`, then `tab_id` inside
`HERDR_PLUGIN_CONTEXT_JSON`, then the snapshot's focused tab. Only when all three are empty
does the command fail. This matters because Herdr's invocation context legitimately carries
no tab when an action is triggered globally — failing there would make `keybind` mode
unusable from a global binding. A malformed `HERDR_PLUGIN_CONTEXT_JSON` is skipped rather
than treated as fatal, so a future Herdr schema addition cannot break the command.

## Configuration

Every field, with its default:

| Field | Default | Notes |
|---|---|---|
| `schema_version` | `1` | Required. Only `1` exists. |
| `mode` | `"auto"` | One of `auto`, `keybind`, `off`. |
| `template` | ` {{ .Tab.Number }}: {{ .Pane.Directory }} > {{ .Process.Name }} ` | Go template. |
| `poll_interval` | `"2s"` | Between 100ms and 1m. |
| `refresh_debounce` | `"80ms"` | Between 0 and 2s. |
| `max_width` | `0` | `0` means unlimited; otherwise 1–1024 cells. |
| `[aliases.<name>]` | empty | Arbitrary named string maps for the `alias` helper. |
| `[icons.agent_status]` | see below | Feeds the `statusIcon` helper. |
| `[[profiles]]` | none | Conditional overrides; see below. |

Unknown keys are rejected, so a typo is reported instead of silently ignored.

## Profile precedence

Profiles are evaluated in declaration order, and every matching profile applies in turn, so
a later profile overrides an earlier one. A profile may override `mode`, `template`, and
`max_width` only; aliases and icons are global in schema version 1. `.Profile` reports the
name of the last profile that overrode something, or `default`.

Inside `[profiles.match]`, categories combine with AND and entries within a category with
OR. Available matchers are `workspace_label_regex`, `tab_label_regex`, `cwd_glob`,
`process_regex`, `agent_regex`, and `agent_status`.

**`tab_label_regex` is a trap under `auto` mode.** It matches the *source* label — the last
label the plugin did not write — which under `auto` freezes at whatever Herdr reported when
the daemon first saw the tab. It only changes again if you rename the tab by hand. For
matching live state, prefer `cwd_glob`, `process_regex`, `agent_regex`, or `agent_status`.
`tab_label_regex` is genuinely useful in `keybind` and `off` modes.

## Modes

- `auto` — the plugin owns the label and rewrites it whenever the rendered value differs
  from what Herdr currently shows. A manual rename is observed, recorded as the new source
  label, and then overwritten on the next refresh. This is intentional.
- `keybind` — the plugin never writes on its own. Labels change only when you invoke
  `rename-current`.
- `off` — the plugin never writes. It still observes manual renames so profile matching
  stays accurate.

## Keybinding

Bind the action in Herdr's own configuration:

```toml
[[keys.command]]
key = "prefix+shift+r"
type = "plugin_action"
command = "herdr-tabline.rename-current"
description = "render this tab's label"
```

## Template variables

Templates receive one context per tab.

### `.Tab`

`.Tab.ID`, `.Tab.Label`, `.Tab.CurrentLabel`, `.Tab.RenderedLabel`, `.Tab.Index`,
`.Tab.Number`, `.Tab.NativeNumber`, `.Tab.PaneCount`, `.Tab.Focused`.

`.Tab.Label` is the source label — the newest label this plugin did not write.
`.Tab.CurrentLabel` is what Herdr shows right now. `.Tab.RenderedLabel` is what the plugin
wrote last.

### `.Workspace`

`.Workspace.ID`, `.Workspace.Label`, `.Workspace.Number`, `.Workspace.NativeNumber`,
`.Workspace.Focused`, `.Workspace.TabCount`, `.Workspace.PaneCount`,
`.Workspace.ActiveTabID`, `.Workspace.AgentStatus`, `.Workspace.Tokens`.

`.Workspace.Tokens` holds metadata tokens reported through `workspace.report_metadata`.

Worktree data, present only when Herdr reports one: `.Workspace.Worktree.Present`,
`.Workspace.Worktree.RepoKey`, `.Workspace.Worktree.RepoName`,
`.Workspace.Worktree.RepoRoot`, `.Workspace.Worktree.CheckoutPath`,
`.Workspace.Worktree.IsLinkedWorktree`. There is no branch field — Herdr does not report one.

### `.Pane` and `.Panes`

`.Pane` is the tab's layout-focused pane, which is what the label describes. In a split tab
where no pane holds global focus, the layout still designates one; that is the pane used.

`.Pane.ID`, `.Pane.Label`, `.Pane.Title`, `.Pane.Focused`, `.Pane.FocusedInTab`,
`.Pane.Cwd`, `.Pane.ForegroundCwd`, `.Pane.EffectiveCwd`, `.Pane.Directory`,
`.Pane.TerminalID`, `.Pane.TerminalTitle`, `.Pane.TerminalTitleStripped`, `.Pane.Agent`,
`.Pane.DisplayAgent`, `.Pane.AgentStatus`, `.Pane.StateLabels`, `.Pane.Tokens`.

`.Pane.Focused` is global focus; `.Pane.FocusedInTab` is focus within this tab.
`.Pane.EffectiveCwd` prefers the foreground process's directory over the pane's, and
`.Pane.Directory` is its basename. Range over `.Panes` for every pane in the tab.

### `.Process`, `.Processes`, and `.ProcessByPGID`

`.Process.PID`, `.Process.Name`, `.Process.Argv0`, `.Process.Argv`, `.Process.CommandLine`,
`.Process.Cwd`. Range over `.Processes` for all foreground records.
`.ProcessByPGID.PID`, `.ProcessByPGID.Name`, `.ProcessByPGID.Argv0`, `.ProcessByPGID.Argv`,
`.ProcessByPGID.CommandLine`, `.ProcessByPGID.Cwd`.

**`.Process` is the first foreground record, not the process-group leader.** This is
load-bearing. `.Process.Name` and `.Process.Argv0` describe that same selected record;
`coalesce .Process.Argv0 .Process.Name` is the best command name available for it, but it
may still read `node` or `caffeinate`, and it is not a reliable pane or agent identity. The
process-group leader lives in `.ProcessByPGID`, where a Claude pane shows the version-string
pair such as `2.1.232`. Templates that want the detected coding agent should use
`.Agent.Kind` or `coalesce .Agent.DisplayName .Agent.Name .Agent.Kind`.

The compatibility default template keeps `.Process.Name` to preserve the previous plugin's
first-record formatting rule. That is not a promise of byte-identical output: live-order
numbering and layout-focused pane selection deliberately produce different, more correct
values in exactly the cases the old plugin got wrong.

### `.Agent`

`.Agent.Active`, `.Agent.Kind`, `.Agent.Name`, `.Agent.DisplayName`, `.Agent.Status`,
`.Agent.InteractiveReady`, `.Agent.LaunchPending`, `.Agent.StateChangeSeq`, `.Agent.Title`,
`.Agent.TerminalTitle`, `.Agent.TerminalTitleStripped`, `.Agent.StateLabels`,
`.Agent.Tokens`, `.Agent.HasSession`.

`.Agent.HasSession` reports whether Herdr holds a resumable session for the agent, without
exposing the session identifier.

### Other

`.Shell.PID`, `.Shell.TTY`, `.Profile`, `.Now`.

## Helpers

Only this fixed set is available. Templates cannot reach the filesystem, the environment, or
the network.

Strings: `lower`, `upper`, `title`, `trim`, `replace`, `contains`, `hasPrefix`, `hasSuffix`,
`join`, `matches`, `default`, `coalesce`, `first`, `last`.

Widths: `truncate`, `truncateMiddle`, `padLeft`, `padRight`.

Paths: `basename`, `dirname`, `cleanPath`, `homeRelative`.

Lookups: `alias`, `statusIcon`, `formatTime`.

`alias "process" .Process.Name` reads `[aliases.process]` and falls back to the original
value. `statusIcon .Agent.Status` reads `[icons.agent_status]`.

## Aliases and icons

```toml
[aliases.process]
zsh = "shell"

[icons.agent_status]
working = "●"
blocked = "!"
done = "✓"
idle = "○"
unknown = "?"
```

## Validation and preview

```bash
herdr-tabline validate-config   # compile and report; no socket needed
herdr-tabline preview           # resolved profile, mode, and quoted label; writes nothing
```

`preview` prints the label in quotes so leading and trailing spaces are visible.

## Hot reload

The configuration file is re-read before every refresh. A valid change takes effect on the
next pass, including changes to `poll_interval` and `refresh_debounce`. An invalid edit is
reported once and the last valid configuration stays in force — the daemon does not exit and
does not fall back to defaults.

## Refresh model

Structural changes arrive over `events.subscribe`: tab and pane creation, closure, renames,
moves and focus changes, workspace reordering and renames, and layout updates.

`pane.updated` is deliberately **not** subscribed. Agent title animation produces roughly ten
of those per second, and the poll already covers the mutable pane metadata they carry.

`poll_interval` is the correctness backstop, not a fallback. Herdr provides no guaranteed
global subscription for foreground-process changes, so `.Process.Name` cannot be kept current
from the event stream alone.

Two behaviors are worth knowing:

- **Replay warm-up.** A new subscription may replay stale buffered history, and Herdr sends
  no replay-complete marker. The daemon therefore takes one fresh authoritative snapshot
  immediately after the subscription is acknowledged, then waits for the stream to be quiet
  for one full `poll_interval` before enabling event-driven refreshes. Polling runs normally
  throughout. Replayed payloads are never treated as current state; snapshots are
  authoritative.
- **Degradation.** If the subscription fails outright, or the stream never settles, the
  daemon keeps working on the poll interval alone rather than exiting. A dropped stream is
  reconnected on a 250ms → 5s backoff, followed by a forced refresh and a fresh warm-up.

After warm-up, events mark the session dirty and arm the `refresh_debounce` timer, so a
burst collapses into a single snapshot.

## Tab reorder behavior

`.Tab.Number` is derived from the tab's position in the snapshot's tab array, so moving a tab
renumbers it on the next refresh. `.Tab.NativeNumber` is Herdr's raw `number` value, which
does not compact when tabs are closed and does not follow a move.

This depends on Herdr's snapshot array being display order. If numbering ever looks wrong,
check that assumption directly:

```bash
herdr api snapshot | jq '.result.snapshot.tabs[] | {tab_id, number, label}'
```

If the array order stops matching what the tab bar shows, that is the cause.

## Session restore

The manifest starts the daemon from a `[[startup]]` hook and again on `pane.created`, so a
restored session picks the daemon back up. `start` treats an already-running daemon as
success rather than an error, which is what makes repeated invocation safe.

State is per session: the socket path is hashed into the state, lock, and log filenames, so
named Herdr sessions never share label history or fight over one PID lock.

## Styling limitation

Labels are plain text. Herdr's `tab.rename` accepts a string, so there is no way to set
color, bold, or any other attribute from this plugin. Icons and separators in a template are
literal characters, and any ANSI escape in a rendered label is stripped rather than
interpreted.

## Troubleshooting

- **Labels never change.** Check `mode`. In `keybind` and `off` the plugin does not write on
  its own. Then confirm the daemon is running with `herdr-tabline stop` — it reports whether
  one was found.
- **A manual rename keeps disappearing.** That is `auto` mode behaving correctly. Use
  `keybind` if you want manual names to persist.
- **A profile never matches.** If it keys on `tab_label_regex` under `auto`, see the warning
  in *Profile precedence*.
- **Labels lag a few seconds.** Foreground-process changes are covered by `poll_interval`,
  not by events. Lower it if you need faster reaction.
- **Nothing works at all.** Run `herdr-tabline validate-config`. A configuration fault exits
  with status 2 and names the problem; a runtime failure exits 1.
- **Where are the logs?** In `HERDR_PLUGIN_STATE_DIR`, named `daemon-<session>.log`.

## Live smoke test

This is opt-in and mutates a live session — it renames real tabs. Do not run it as part of
the normal test suite.

```bash
herdr-tabline validate-config
herdr-tabline preview            # inspect the label before applying it
herdr-tabline rename-current     # writes one tab
herdr-tabline start
# open a second tab, move it, and watch both labels renumber
herdr-tabline stop
```

## Platform support

macOS and Linux. The transport uses Unix domain sockets and the daemon lock uses `flock`;
neither has a Windows implementation in this release.

## Development

```bash
make check   # fmt-check, vet, build, test, race — everything non-live
```

Individual targets: `build`, `test`, `race`, `vet`, `fmt-check`.

Tests run against a fake newline-delimited JSON socket server and never touch a live Herdr
session. See `CONTRIBUTING.md`.

## Publication

This directory is already a self-contained Go module whose path is
`github.com/btj93/herdr-tabline`. Publishing is `git init`, commit, and push to that
repository root — no import rewriting and no file moves. `.github/workflows/ci.yml` only
becomes active once the directory is a repository root.

To transfer the project to a different owner, do it as one deliberate operation:

```bash
go mod edit -module github.com/<new-owner>/herdr-tabline
grep -rl 'github.com/btj93/herdr-tabline' . \
  | xargs sed -i '' 's|github.com/btj93/herdr-tabline|github.com/<new-owner>/herdr-tabline|g'
go build ./... && go test ./...
```

## License

MIT. See `LICENSE`.
