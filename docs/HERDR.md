# herdr integration

herdr is a local daemon that knows which agent runs in which terminal pane.
`flow-indicator` uses it for two separate things, and neither is required to run
the meter:

| flag | direction | herdr command |
|---|---|---|
| `watch --pane <id\|auto>` | read | `herdr agent list`, `herdr pane current` |
| `watch --herdr-pane <id>` | write | `herdr pane report-metadata` |

The read side answers "which session is this pane running". The write side puts
four display tokens in that pane's sidebar row. They are independent: either can
be used alone, and `--pane auto --herdr-pane <id>` uses both.

Without herdr, `watch --current`, `watch --last` and `watch <session-id>` work
unchanged. `--pane` requires the `herdr` command on `PATH`; `--herdr-pane` does not, and a
missing command means the sidebar row never appears. `--pane`
additionally requires the daemon to be answering.

## Why pane instead of directory

`--current` keys on the working directory. Two agents working in one repository
record the same working directory, so the selection is a coin flip — the fleet
this was written against had three agent panes reporting the same cwd on
2026-08-15. The pane is the exact key.

## `--pane`: resolving a pane to a session

```bash
flow-indicator watch --pane auto      # the agent pane sharing this pane's tab
flow-indicator watch --pane w2E:p1    # a named pane
```

Three steps, in order.

**1. Choose the pane.** An explicit id must appear in `herdr agent list`; if it
does not, the error lists every agent pane as a `--pane` argument. `auto` reads
`herdr pane current` for the asking process's own tab and workspace, then
filters the agent list to the same `tab_id`. A tab with no agent widens to the
same `workspace_id`, for the layout where the meter has a tab of its own. Two
candidates are an error naming each; nothing is guessed.

**2. Pane to session id.** The identifier is herdr's `agent_session` join and
nothing else:

```json
{"pane_id": "w2E:p1", "tab_id": "w2E:t1", "workspace_id": "w2E",
 "agent": "claude",
 "agent_session": {"kind": "id", "source": "herdr:claude",
                   "value": "d5761fbc-727e-4e52-a7ff-1176cfb85562"}}
```

Each installed herdr agent integration reports its own session id into the
daemon, so one field answers for claude, codex, opencode and qwen alike. An
earlier version scraped the pane's processes instead — a session environment
variable for claude, an open state database for codex. Each was a second data
path for the same fact, correct for one agent and one version of it, and the
claude one was measured working only mid-tool-call. That is gone. A pane the
integration has not reported is waited on, never scraped.

**3. Session id to harness.** `agent` maps to a harness — `claude` →
`claude-code`, `codex`, `opencode`, `qwen` — and that harness's own discovery
finds the session by id. From there the watch is the same loop every other mode
runs. Passing `--adapter` for a different harness than the pane runs is an
error, not an override.

The pane and the chosen session are printed before the watch starts. Nothing in
this path acts on the pane it resolves.

### Waiting versus failing

Two conditions are temporary, and the meter waits through them, re-probing every
2 s after printing what it is waiting for:

- the pane has no `agent_session` yet — identity arrives with the pane's next
  activity;
- the id is reported but the harness store has not seen the session yet, which
  happens when a just-started agent's transcript postdates its pane record.

```text
pane: herdr records no session for w2J:p1 (opencode) yet
waiting: identity arrives with the pane's next activity
```

Everything else exits: no such pane, more than one adjacent agent pane, an agent
with no harness, `herdr` not on `PATH`, the daemon not answering within the 3 s
query timeout.

### Following an agent restart

The watch is keyed to the pane, not to the session first resolved. Every 5 s the
pane is re-resolved. A positive resolution to a *different* session id ends the
current observation — writing its projections as an ordinary stop — and the loop
follows the new session from its beginning.

Resolution errors and an empty session id change nothing. A pane between one
agent exiting and its replacement starting looks exactly like a quiet pane from
outside, so only a new id acts. `--tail-only` applies to the first session only;
a session that appears mid-watch is new and is replayed whole.

## `--herdr-pane`: the sidebar row

The meter does not need a pane of its own. `--herdr-pane` pushes the phase into
another pane's herdr sidebar row:

```bash
flow-indicator watch --pane auto --herdr-pane w2E:p1
```

Four tokens, and nothing else:

| token | value |
|---|---|
| `flow_phase` | the regime word: `FLOW`, `DRIFT`, `THRASH`, `RECOVERY` |
| `flow_turn` | `turn <n>` |
| `flow_elapsed` | time since the last record, short form |
| `flow_trend` | the most recent band crossing, e.g. `F → D` |

Each push is one `herdr pane report-metadata <pane> --source flow-indicator
--seq <n> --ttl-ms 30000` with a `--token name=value` per field. An empty field
is pushed as `--clear-token`, so a row never keeps a value that stopped holding.

Timing and failure:

- **TTL 30 s, refresh 10 s.** The row is pushed when it changes and again on the
  refresh cadence to hold the TTL open, so a slow turn never blanks it.
- **Dies with the process.** On `Ctrl-C` the row is cleared explicitly, and if
  the meter is killed the TTL expires it. The sidebar never shows a phase that
  stopped being true.
- **Never blocks the draw loop.** One worker owns the subprocess; if a push is
  still running the next row is dropped, because the frame after it is a better
  row than the one that could not be sent. Each push is bounded at 3 s.
- **Failures are ignored.** herdr may not be running. A meter that cannot draw a
  sidebar row is still a meter.

This is the only thing written to herdr. It reads no herdr state, changes no
pane, and touches no server lifecycle. Anything beyond a display token would be
intervention, which `AGENTS.md` forbids.

## Layouts

**Meter in a split beside the agent.** The common case; the meter draws its own
side-pane view and needs no sidebar row.

```bash
flow-indicator watch --pane auto
```

**No pane for the meter.** Run it anywhere and give the agent's pane the row.
`--pane auto` will not find an adjacent agent from a detached shell, so name the
pane on both flags:

```bash
flow-indicator watch --pane w2E:p1 --herdr-pane w2E:p1
```

**Several agents, one repository.** Name each pane; `--current` cannot separate
them.

```bash
flow-indicator watch --pane w2E:p1
flow-indicator watch --pane w2J:p1
```

## Instance records under herdr

A `watch` on a discovered session writes one operational record per run, readable through
`flow-indicator instances --json`. Two of its fields come from herdr:

- `pane_id` is the `--herdr-pane` argument, not the `--pane` target: it names
  the pane this meter is drawing into.
- `workspace_id` is read from the `HERDR_WORKSPACE_ID` environment variable,
  which herdr sets in the panes it owns.

Both are empty when the flags and the environment are absent. See
[INTEGRATIONS.md](INTEGRATIONS.md) for the record's full shape and liveness
rules.

## Verifying the integration

```bash
herdr agent list | grep agent_session   # the join --pane depends on
flow-indicator watch --pane auto        # prints the pane and session it picked
flow-indicator instances --json         # what other local tools can read
```

A pane whose agent has never reported an `agent_session` has no integration
installed for that agent, or has not been active since it started. The first is
fixed in herdr, the second by waiting.
