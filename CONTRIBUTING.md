# Contributing

## Setup

```bash
git clone https://github.com/TGPSKI/flow-indicator
cd flow-indicator
make build
```

Go 1.26 or later. There is nothing else to install: the project is stdlib-only
and has no `go.sum`.

## Commands

| command | what it does |
|---|---|
| `make build` | compile `./flow-indicator` |
| `make install` | install the binary into `GOBIN` |
| `make test` | `go test ./...` |
| `make test-race` | `go test -race ./...` |
| `make check` | gofmt, `go vet`, `go mod verify` |
| `make lint` | `golangci-lint run` |
| `make ci` | check + test-race + lint; the local half of the CI gate, which also builds, replays every fixture and asserts zero dependencies |
| `make replay` | replay all five fixtures into `.replay/` |
| `make cover` | coverage profile, opened in the browser |
| `make clean` | remove the binary, `coverage.out` and `.replay/` |

## Constraints on any change

These are the rules a change is judged against. `AGENTS.md` states them in
full; the short form:

1. **No third-party dependencies.** A pull request that adds a `require` line
   needs to argue why the standard library cannot do the job.
2. **No claims about hidden model state.** Output describes observable things:
   correction density, repeat counts, repair depth, serialization inflation. If
   evidence is missing, the value is `unknown`.
3. **Every value carries provenance** — `observed`, `classified` or `derived` —
   and every classified value carries its classifier name, version and rule
   hash.
4. **Event files are append-only.** A changed interpretation appends another
   event. Only `summary.json`, `metrics.json`, `report.md` and `timeline.csv`
   may be overwritten, and they must be reproducible from the event files.
5. **Replay is deterministic.** `TestReplayIsByteStable` fails if two replays of
   the same source differ by a byte.
6. **The instrument does not intervene.** Nothing may act on the session it
   measures.

## Where changes go

| change | where |
|---|---|
| a new marker phrase | `internal/profile/baseline.json`, and bump `HeuristicVersion`. No rule file compiles a pattern of its own |
| a new structural rule | `internal/classify`, with no phrase list and no dial |
| a new agent to read | one file in `internal/harness`, plus one in `internal/adapter` if the record envelope is its own |
| a new event kind | `internal/event/kinds.go`, a payload in `internal/state/payloads.go`, a case in `store.FileFor`, a row in `docs/EVENTS.md` |
| a new source format field | the adapter that owns it, never a core package |
| a metric | `internal/metrics`, as arithmetic over counts, with an explicit unknown rule |
| a threshold | `internal/config/config.go` if the operator should tune it, a named constant in `internal/state/regime.go` if the contract fixes it |

## Fixtures

`fixtures/<name>/` holds `input.jsonl`, `expect.json` and a `README.md` stating
the fixture's purpose and provenance. `expect.json` carries only load-bearing
expectations; absent fields are not checked.

Fixture prose may be rewritten to be more realistic. Thresholds may not be
tuned to make a fixture pass. If a fixture exposes a bad rule, change the rule
and record the reasoning under "Judgement calls" in `docs/METRICS.md`.

## Pull requests

- `make ci` passes.
- New behaviour has a test that would fail without it.
- Documentation that the change makes wrong is updated in the same commit,
  including `docs/EVENTS.md` and `docs/METRICS.md`.
- Commit subject: one imperative line naming what changed, under 72 columns —
  `Bound an open repair episode`. The body states what was measured, what it
  cost, and how it was verified. `checkpoint`, `second pass` and `fixes` name
  nothing a reader can act on.
- Prose follows `words-are-cheap`: if a line does not change the reader's
  decision, delete it.

## Scope

`AGENTS.md` states the scope. Cross-session analytics, a web UI, remediation,
prompt optimization, daemonization and remote telemetry are out of it. Reading a
new agent's transcripts is in scope; a new thing to do with a transcript is not.
Open an issue describing the measurement you cannot make without the feature.
