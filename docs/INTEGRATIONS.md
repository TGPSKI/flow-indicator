# Integrations

Every boundary `flow-indicator` has with something outside its own process, what
crosses it, and what happens when the other side is absent.

| boundary | direction | required | detail |
|---|---|---|---|
| harness session stores | read | one of them, to have input | [Harnesses](#harnesses) |
| `sqlite3` | read | only for opencode | [opencode](#opencode-needs-sqlite3) |
| herdr | read + one display write | no | [HERDR.md](HERDR.md) |
| instance records | write, then read back | no | [Instance records](#instance-records) |
| session directory | write | yes | [Session artifacts](#session-artifacts) |
| corpus, labels and replay-set outputs | write | only for those commands | [Selected output paths](#selected-output-paths) |
| OpenAI-compatible endpoint | write turn text, read verdict | no | [INFERENCE.md](INFERENCE.md) |

There is no telemetry, no update check and no daemon. Model requests start at
endpoints an operator names: the configured classifier endpoint, and `annotate
--endpoint`, which is a separate destination with its own flag. The clients use
Go's default redirect policy; a 307 or 308 response can resend a request body to
the redirect target.

## Harnesses

One interface, four coding agents. A session from any of them decodes into the
same canonical records, so every rule, metric and calibration works across all
four without being told which produced it. Each harness owns every field name of
its own format; no other package reads them.

| harness | store | live follow |
|---|---|---|
| `claude-code` | `~/.claude/projects/<project>/*.jsonl` | byte tail |
| `codex` | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` | byte tail |
| `opencode` | `~/.local/share/opencode/opencode.db#<session-id>` | re-query |
| `qwen` | `~/.qwen/projects/<project>/chats/*.jsonl` | byte tail |
| `generic` | any JSONL with `stream_id`, `seq`, `speaker`, `text` | byte tail, or stdin |

Discovery reads the working directory the source itself records, not the encoded
project directory name. Sub-agent transcripts are skipped: claude-code marks
them in-stream, codex writes them as sibling files, qwen puts them under a
sibling `subagents/` directory.

`sessions`, `watch --current` and `watch --last` search all four agent harnesses
unless `--adapter` selects one before discovery. `watch --pane` reads only the
harness mapped from the pane's agent. A named path opens only that path; pass
`--adapter` when its harness differs from the command default.

The file-backed harnesses hold a partial final record until its newline arrives
and never write to the file they follow. opencode has no file to tail — its
transcript is rows, and the newest message grows in place while the agent
streams into it — so it is followed by re-query with the agent's newest message
held back until it is finished. A record the projector has consumed cannot be
amended, so releasing a half-written turn would freeze it that way.

Two asymmetries worth knowing before comparing harnesses:

- **Codex has no read, grep or glob tool.** It reads files by running programs,
  so its verb histogram is nearly all `execute` where another harness would show
  `read` and `search`.
- **opencode transcripts are rows**, so byte offsets in its events refer to the
  store reference, not a file position.

### opencode needs `sqlite3`

This program carries no database driver, and a pure-Go SQLite engine would be
the largest thing in it. The store is read through `sqlite3` in read-only,
WAL-aware mode, so a running opencode is neither disturbed nor read stale.
Without `sqlite3` on `PATH`, opencode sessions fail with that named as the
reason; every other harness is unaffected.

### Adding a source

A new agent is a new file in `internal/harness` plus a decoder in
`internal/adapter`. Nothing else changes: rules, metrics and calibration are
written against canonical records, and a test fails the build if a harness tool
name escapes its own file. `generic` is the escape hatch for anything that can
be projected into JSONL — feed it a file, or pipe it:

```bash
some_stream | flow-indicator watch --adapter generic
```

## herdr

`watch --pane` resolves a pane to the session running in it; `watch
--herdr-pane` pushes four display tokens into a pane's sidebar row. Both are
optional and both need the `herdr` command on `PATH`. Full behaviour, layouts
and failure modes: [HERDR.md](HERDR.md).

## Instance records

The optional regression test `TestCopiedLongSessionWatchModes` reads
`FLOW_INDICATOR_TEST_SOURCE_COPY`. Set it only to a copied transcript; it reads
the recorded 32,504,875-byte audit prefix and exercises watches on further
temporary copies with controlled semantic responses. Production commands do
not read this variable.

`TestRealModelValidation` and `TestRealHybridWatch` additionally require
`FLOW_INDICATOR_TEST_MODEL_CONFIG`, a configuration file naming the actual
endpoint and model. These opt-in tests send copied-source text to that endpoint
and retain private artifacts under `/tmp/flow-indicator-real-*`. The replay test
compares constrained and unconstrained decoding using the baseline profile;
the live test loads the profile beside the named configuration file.
`TestRealSchemaOrder` requires only the model configuration and sends six
synthetic probes with two schema property orders. Its logs and the replay
validation counts are diagnostic measurements, not labeled accuracy gates.

Each running `watch` on a discovered session — a file, or opencode's store —
writes one operational record beside the
session directories, at `$XDG_DATA_HOME/flow-indicator/instances/<id>.json`,
refreshed on the one-second watch heartbeat with a 30 s TTL. Source arrivals
can refresh it too, at most twice per second. It is written to a temporary file and
renamed, so a reader never sees a half-written record.

Other local tools read it only through the public command:

```bash
flow-indicator instances --json
```

```json
{"collected_at": "2026-08-16T18:20:11Z",
 "items": [{
   "version": 1,
   "instance_id": "3f9c1a2b7d4e5f60",
   "pid": 48122,
   "hostname": "workstation",
   "started_at": "2026-08-16T18:02:44Z",
   "reported_at": "2026-08-16T18:20:04Z",
   "expires_at": "2026-08-16T18:20:34Z",
   "stream_id": "d5761fbc-727e-4e52-a7ff-1176cfb85562",
   "harness": "claude-code",
   "source_path": "/home/you/.claude/projects/-home-you-src-parser/d5761fbc.jsonl",
   "mark": "byte 918442",
   "root": "/home/you/src/parser",
   "project": "parser",
   "pane_id": "w2E:p1",
   "workspace_id": "w2E",
   "regime": "DRIFT",
   "turn_index": 23,
   "seq": 411,
   "epoch": 1,
   "elapsed": "42s",
   "trend": "F → D",
   "metrics": {"serialization_inflation": 4.8, "forward_work_share": 0.37,
               "control_plane_burden": 0.58,
               "unresolved_obligation_candidates": 12,
               "repeated_obligations": 1, "repair_depth": 1,
               "dereference_reliability_proxy": null},
   "classifier_capabilities": ["obligation_release"]}]}
```

Rules a consumer can rely on:

- **Liveness is probed, not trusted.** A record is returned only if its hostname
  matches, its TTL has not expired, and a signal-0 probe establishes that its
  process exists. A record one TTL past expiry is deleted on read.
- **Empty is a fact.** `items` is `[]`, never `null`, when no watch is live.
  That is a successful observation that nothing is running, not a stopped
  interaction.
- **Local only.** Records from another hostname are ignored; nothing is shared
  between machines.
- **No transcript content.** Liveness coordinates and a derived metric subset
  only. A metric with no evidence behind it serializes as `null`, never `0`.
- **`pane_id` is the `--herdr-pane` argument**, the pane this meter draws into.
  `workspace_id` comes from `HERDR_WORKSPACE_ID`. Both are empty without herdr.

`instances` requires `--json`; there is no human-readable form to parse by
accident.

## Session artifacts

Every `watch` or `replay` session artifact lands under
`$XDG_DATA_HOME/flow-indicator/sessions/<stream-id>/`. The five `.jsonl` files
are append-only and are the source of truth; the rest is disposable and rebuilt
from them.

| file | for a consumer |
|---|---|
| `observations.jsonl`, `classifications.jsonl`, `obligations.jsonl`, `repairs.jsonl`, `metrics.jsonl` | the event log; schema in [EVENTS.md](EVENTS.md) |
| `metrics.json` | every snapshot, one per turn |
| `summary.json` | counts, regimes seen, extremes |
| `report.md` | rendered narrative |
| `timeline.csv` | one row per turn, for a spreadsheet or plot |
| `source.json` | source path, byte offset, record count |

## Selected output paths

Three commands write outside the data directory only when the operator names a
destination:

- `corpus --build --out <dir>` writes a manifest and, with `--copy`, transcript
  copies under that directory;
- `annotate --out <dir>` writes candidate label files under that directory;
- `replay-set --output <file> --anchors <file>` writes one JSONL row per session
  and source anchors. Without `--anchors`, the anchor path is derived beside
  `--output`.

Session, instance and corpus files are created 0600 under 0700 directories.
`replay-set` and `annotate` write their outputs under the process umask. Stored text defaults to a
160-character snippet per record, governed by `privacy.store_text`,
`privacy.store_snippets` and `privacy.snippet_chars`.

## Classifier endpoint

`openai-compatible`, `hybrid` and `deferred` post turn text to whatever chat-completions
endpoint is configured, with the key from `FLOW_INDICATOR_API_KEY` as a bearer
token. `none` and `heuristic` send nothing, and `heuristic` is the default.
Request boundary, provenance and replay semantics: [INFERENCE.md](INFERENCE.md).
When `classifier.disable_thinking` is true, the request adds
`chat_template_kwargs.enable_thinking=false`, an extension accepted by vLLM.

## Environment

| variable | read by | effect |
|---|---|---|
| `XDG_CONFIG_HOME` | config | `config.json` and `profile.json` location; falls back to `~/.config` |
| `XDG_DATA_HOME` | store, opencode | session, instance and opencode store location; falls back to `~/.local/share` |
| `CODEX_HOME` | codex harness | where Codex rollouts are enumerated |
| `HERDR_WORKSPACE_ID` | instance record | `workspace_id` field |
| `FLOW_INDICATOR_API_KEY` | classifier, `annotate` | bearer token for the configured endpoint |
| `NO_COLOR` | live views | draws without ANSI attributes, same as `--no-color` |

Color is also off automatically when stdout is not a terminal, so a redirected
capture holds plain text.
