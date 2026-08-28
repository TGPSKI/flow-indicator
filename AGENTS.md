# AGENTS.md

Guidance for AI coding agents working in this repository. Read this before any
change.

## What this is

`flow-indicator` measures how much of an operator's serialization goes into
steering an agent rather than advancing work. It reads an interaction
transcript, appends events, projects state, and prints metrics. It is an
instrument: it never acts on the stream it measures.

Repository: `github.com/TGPSKI/flow-indicator`. Binary: `flow-indicator`.

This file and `docs/METRICS.md` are the contract. Where they disagree, record
the contradiction and the resolution under "Judgement calls" in
`docs/METRICS.md`.

## Gates a change must pass

1. **Observable claims only.** No output may assert hidden model state. Not
   "the model forgot" or "context broke"; instead "an obligation was repeated",
   "repair depth reached 3", "dereference proxy missed". If evidence does not
   support a value, emit `unknown`. Never infer through the gap.
2. **Provenance on every result.** Every value is `observed`, `classified`, or
   `derived`. A classified value carries classifier name, version, rule hash,
   source turn, and confidence. No metric exists without source events behind
   it.
3. **Append facts, project state.** The five `.jsonl` files are append-only. A
   changed interpretation appends another event; it never rewrites one.
   `summary.json`, `metrics.json`, `report.md` and `timeline.csv` are
   disposable projections and must be reproducible from the event files.
4. **Deterministic core.** Same input, same config, same classifier output must
   produce the same events, state, metrics and report, byte for byte. Nothing
   random, nothing clock-based, no map iteration order in output.
5. **Zero third-party dependencies.** `go.mod` has no `require` block and there
   is no `go.sum`. No framework, ORM, TUI library, plugin system or telemetry
   SDK. ANSI for the terminal, `encoding/json` for data, `net/http` for the
   optional classifier.
6. **No intervention.** Nothing in this program may act on, prompt, stop, or
   modify the agent session it measures. The one outbound write is
   `watch --herdr-pane` / `--herdr-workspace`, which pushes four display
   tokens with a TTL into herdr's own sidebar. It reads no herdr state,
   changes no pane, and touches no server lifecycle; anything beyond a
   display token is intervention.

## Architecture

The processing order is fixed. Packages may not bypass it.

```text
source record -> adapter -> canonical record -> observation -> observed events
  -> classifier -> classified events -> state projector -> transition events
  -> metrics -> derived events -> render
```

| package | owns | must not |
|---|---|---|
| `internal/stream` | canonical `Record`, `SourceRef`, normalization, file tail | know any source format |
| `internal/adapter` | every source field name, one file per format | compute metrics |
| `internal/harness` | where each agent's sessions live, how to enumerate, decode and follow one | interpret a record |
| `internal/observe` | byte, character, word, line, quote, repeat, overlap, timing counts | interpret meaning |
| `internal/classify` | the marker classifier, the model classifier, the worker lane | mutate state; compile a pattern of its own |
| `internal/profile` | the shipped baseline lexicon and the operator overlay merged over it | hold a structural rule |
| `internal/event` | the envelope, deterministic IDs, kind constants | import `state` |
| `internal/state` | obligations, repair episodes, epochs, regime, the projector | parse source formats |
| `internal/metrics` | arithmetic over counts, unknown handling | import `state` or read records |
| `internal/store` | append-only files, session layout, kind routing | interpret payloads |
| `internal/config` | the operator-tunable windows, thresholds, classifier and privacy keys | carry a value the contract fixes |
| `internal/render` | report, timeline, inspect, live view | classify or change state |
| `internal/labeler` | model-backed candidate labeling, its prompts | assert ground truth |
| `internal/labels` | ground truth, span alignment, scoring, Cohen's kappa | replay a session |
| `internal/calibrate` | corpus manifest, split, replay for scoring, run records | change a rule |
| `pkg/panel` | ANSI column arithmetic, the box, the measured table, in-place repaint, terminal width | know what a number means |

`state` imports `metrics`, never the reverse. `classify` takes `ObligationRef`
and `RepairRef` copies rather than importing `state`, which keeps the cycle
open.

`pkg/panel` is the terminal layer with the meter taken out of it, exported so
other programs can draw the same instrument over their own numbers. It imports
nothing but the standard library, and `go list -deps ./pkg/panel` is the check.
Anything that reads a `Snapshot`, names a phase, or decides that a rising
number is bad belongs in `internal/render`; the seams are `panel.Table`'s
`Style` callback and `panel.Directions`, which carry those decisions in from
the caller.

Two files sit outside the contract's tree because they are cross-cutting within
their package: `internal/metrics/metrics.go` (the `Value` type and `Snapshot`)
and `internal/state/payloads.go` (one struct per event kind).

## Working rules

- **The lexicon and the structural rules are separate, and only the lexicon is
  tunable.** Which words a person reaches for when they correct, stop or
  constrain lives in `internal/profile`: a baseline embedded in the binary and
  an operator overlay merged over it. Whether a sentence has a subject before
  its marker, sits inside a fence, or re-edits a path just written is
  structural, stays in code, and carries no dial.
- **No rule file compiles a pattern of its own.** Patterns come from the
  profile in force. `internal/classify/neutrality_test.go` fails the build when
  one escapes.
- **Changing any marker requires bumping `HeuristicVersion`.** Stored events
  must stay attributable to the rules that produced them. The table is at
  version 5. The whole table is fingerprinted with SHA-256, and an overlay
  changes that hash, so a session classified under one is never mistaken for a
  session classified under the baseline.
- **A harness owns its own format.** Adding an agent means one file under
  `internal/harness` and, for a format with its own record envelope, one under
  `internal/adapter`. Nothing downstream may learn which harness produced a
  record, and no rule may name a harness's tool. A test fails the build when a
  tool name escapes its adapter.
- **A late semantic result is evidence, not a revision.** The hybrid worker
  lane stores completions with their provenance; it never revises a regime the
  projector has already rendered. Queue depth, drops and latency are
  measurements of the instrument, and no regime rule reads them.
- **Adding an event kind means three edits**: the constant in
  `internal/event/kinds.go`, the payload struct in `internal/state/payloads.go`,
  and the routing case in `store.FileFor`. Then document the kind in
  `docs/EVENTS.md`. Routing is by exact kind: candidate kinds share the
  `repair_` prefix with state transitions, so a prefix rule misfiles them.
- **Unknown is a value, not zero.** Use `metrics.Value`; it marshals to `null`
  and its `AtLeast`/`AtMost` return false when unknown. Never substitute a
  made-up baseline.
- **Do not tune a threshold to make a fixture pass.** Fixtures are prose and may
  be rewritten to be realistic; rules must stay general. If a fixture exposes a
  bad rule, change the rule and record why in `docs/METRICS.md`.
- **Test against a copy of a session file, never a live one.**

## Domain vocabulary

| term | meaning |
|---|---|
| operator turn | a human record that is not a sidechain record; the unit every metric counts |
| obligation candidate | a sentence stating a requirement, keyed by its normalized form. Unresolved means stated and not resolved since, never known to be in force |
| repeat | byte equality after normalization; similarity is a `near_repeat_candidate`, never a repeat |
| pointer | a compact operator reference (file, task, alias, namespace, operation). Each has its own bounded outcome opportunity; one correction settles at most the reference it evaluates |
| repair episode | opens on a correction; deepens only on a later correction whose established target key equals its own; closes provisionally then durably |
| epoch | the span between resets; a reset ends one and starts fresh rolling baselines |
| regime | FLOW, DRIFT, RECOVERY, THRASH, RESET, evaluated in that reverse precedence |

Prefer: record, event, candidate, classification, repair, repeat, source,
metric, unknown. Avoid: intelligence, understanding, cognitive, frustration,
alignment quality, context health, smart detection.

## Development workflow

```bash
make check        # gofmt -l, go vet, go mod verify
make test         # go test ./...
make test-race    # go test -race ./...
make lint         # golangci-lint run
make ci           # check + test-race + lint: the full gate
make replay       # replay all five fixtures into ./.replay/
make cover        # coverage profile, opened in the browser
```

Fixture expectations live in `fixtures/<name>/expect.json` and are asserted by
`TestGoldenReplay` in `cmd/flow-indicator/main_test.go`. Only the fields present
in a fixture's `expect.json` are checked; `TestReplayIsByteStable` proves two
replays of the same source produce identical event files.

## Errors and comments

Errors state operation, object and cause:

```text
claude-code: decode record at offset 18442: unexpected EOF
config: thresholds.thrash_repair_depth must be >= 1, got 0
```

Not "an error occurred" or "invalid configuration detected".

Comments explain invariants, not mechanics:

```go
// Event IDs must remain stable across replay.
```

Not `// Loop over the records.`

## Documentation

| file | holds |
|---|---|
| `docs/METRICS.md` | the six families, their decision rules and judgement calls |
| `docs/EVENTS.md` | the event envelope and every kind |
| `docs/COMMANDS.md` | every subcommand, its flags and when to reach for it |
| `docs/HERDR.md` | `watch --pane` resolution and the `--herdr-pane` token contract |
| `docs/PROFILES.md` | the marker lexicon, overlay merge rules, parameters, discriminators, rule hash |
| `docs/INTEGRATIONS.md` | every process boundary: stores, subprocesses, instance records, endpoint, environment |
| `docs/INFERENCE.md` | the semantic tier: modes, what is sent, validation, provenance, replay |
| `docs/CALIBRATION.md` | the labeling and scoring loop, split discipline, and what it has measured |

A change to a flag, a subprocess call, an instance-record field or an
environment variable updates the document that owns it.

Every document in this repository, including internal notes, follows
`words-are-cheap`: prefer nouns and verbs to adjectives, exact dates to vague
recency, one sentence to three. If a line does not change the reader's
decision, delete it.

## Subprocesses

Two programs are shelled out to in three roles, all bounded and all optional.
Adding another needs an argument that the standard library cannot do the job.

| program | why | failure |
|---|---|---|
| `sqlite3` | opencode keeps transcripts as rows; read-only, WAL-aware, bounded at 10 s. A pure-Go engine would be the largest thing in the binary | named in the error, with the reason |
| `herdr pane current`, `herdr agent list` | resolving `watch --pane` to an agent pane and its session | typed: unresolved waits and re-probes, fatal exits |
| `herdr pane report-metadata` | `watch --herdr-pane` pushes the sidebar row | ignored; the row expires on its own |

## Scope

The first build is complete: five fixtures replay, the six metric families
separate a healthy stream from a degraded one, every displayed metric traces to
source turns, four harnesses are followed live, and no automatic intervention
exists.

Still out of scope: cross-session analytics, a web UI, remediation, prompt
optimization, daemonization, remote telemetry. The classified families are the
open work — calibration against labels, not more surface. Adding a harness is
in scope when its transcript is on the operator's machine; adding a fifth thing
to do with a transcript is not.
