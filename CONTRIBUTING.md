# Contributing

## Verification

Every change must pass:

```bash
make check
```

That runs `fmt-check`, `vet`, `build`, `test`, and `race`. CI runs the same target, so a
green `make check` locally is the contract.

## How the tests work

The transport tests do not talk to Herdr. They start a temporary Unix domain socket and act
as a fake newline-delimited JSON server, asserting the exact request shape and feeding back
recorded fixtures from `internal/herdrapi/testdata/`. The controller tests drive a fake
client with a scriptable snapshot, and the runner tests drive a fake clock so debounce,
settle, and poll behavior is deterministic rather than timing-dependent.

Consequences worth respecting:

- Tests **must never** mutate a live Herdr session. No test may rename a real tab, start a
  real daemon against the user's socket, stop a running server, or write into the real
  `HERDR_PLUGIN_STATE_DIR`. Use `t.TempDir()` and the fake socket helpers.
- Tests must not depend on wall-clock timing. Inject the clock.
- Every synchronization point needs a bound. A test that can hang instead of failing costs
  the whole package its diagnostic.

When fixing a bug, first write the test that fails because of it, then fix it. A test that
passes both before and after the fix is coverage, not a regression guard — say so rather
than claiming otherwise.

## The live smoke test

Some behavior can only be confirmed against a running Herdr. That check is opt-in, manual,
and never part of `make check`, because it renames real tabs:

```bash
herdr-tabline validate-config
herdr-tabline preview            # inspect before applying
herdr-tabline rename-current     # writes one tab
herdr-tabline start
# open a second tab, move it, confirm both labels renumber
herdr-tabline stop
```

Run it in a scratch workspace you do not mind relabelling, and stop the daemon afterwards.

## Style

Match the surrounding code. Comments explain why a decision was made, especially where the
obvious approach is wrong — the protocol and timing constraints here are full of those.
