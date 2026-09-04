# Commands

Thirteen subcommands in one binary. `flow-indicator help <command>` prints the
same flags this document lists; what it adds here is when to reach for each one.

| command | use it when |
|---|---|
| [`sessions`](#sessions) | you want to know what agent sessions exist on this machine |
| [`watch`](#watch) | an agent is running now and you want the meter beside it |
| [`instances`](#instances) | another local tool needs to know which watches are live |
| [`replay`](#replay) | a session is finished and you want its artifacts |
| [`report`](#report) | you want the narrative for a session already stored |
| [`inspect`](#inspect) | one turn looks wrong and you want its source and evidence |
| [`replay-set`](#replay-set) | you have many finished sessions and want one table |
| [`corpus`](#corpus) | you are assembling a labeled set and need it sealed |
| [`label`](#label) | you want units for a human labeler to fill in |
| [`annotate`](#annotate) | you want a model to fill those units in first |
| [`calibrate`](#calibrate) | you want a score for the rules against those labels |
| [`config`](#config) | you want to create or inspect configuration |
| [`version`](#version) | you are reporting a problem and need to name the build |

## Conventions

**Common flags.** `replay`, `replay-set`, `watch`, `report`, `inspect` and
`calibrate` accept:

| flag | default | meaning |
|---|---|---|
| `--config <path>` | XDG config path | configuration file; a path you name that does not exist is an error, the default one missing means defaults |
| `--data-dir <path>` | XDG data path | storage root |
| `--profile <path>` | `profile.json` beside the configuration file | marker lexicon overlay ([PROFILES.md](PROFILES.md)); a path you name that does not exist is an error |

`instances` accepts `--data-dir` only. `sessions`, `corpus`, `label` and
`annotate` accept neither: none of them read or write a session store.

**Harness names.** `claude-code`, `codex`, `generic`, `opencode`, `qwen`.
Rarely typed: `sessions` and `watch` find sessions themselves, and a session
named by identifier carries its own harness.

**Exit codes.**

| code | cause |
|---|---|
| 0 | success; `help`; `sessions` finding nothing; `Ctrl-C` during `watch`; `replay-set` with per-source failures recorded as rows |
| 1 | a command returned an error, printed bare to stderr |
| 2 | no command, an unknown command, or a flag parse failure |

---

## Looking around

### `config`

Create the default configuration file or inspect the configuration in force.

```bash
flow-indicator config init
flow-indicator config show
flow-indicator config init --config ./flow-indicator.json
```

`init` creates the parent directory and writes the complete defaults to the XDG
configuration path. It refuses to overwrite an existing file. `show` prints
the complete effective configuration; when the default file is absent, that is
the built-in defaults. Pass `--config <path>` to either action to use another
file.

### `version`

Print the build this binary came from.

```bash
flow-indicator version        # or --version, -v
```

`make build` and `make install` stamp the version and commit; a `go build` with
no flags reports `dev` plus the revision the toolchain recorded.

### `sessions`

Every agent session on this machine, newest first, with a handle short enough to
retype.

```text
WHEN     HARNESS      SESSION     PROJECT
now      codex        019ffd5a    ~/src/parser
now      claude-code  c0d33334    ~/src/flow-indicator
5m ago   opencode     ses_002a9   ~/src/parser
```

| flag | default | meaning |
|---|---|---|
| `--all` | `false` | every session, not the most recent 15 |
| `--here` | `false` | only sessions recorded for this working directory |
| `--adapter <name>` | all | only one harness |

Sub-agent threads are left out, and so is any transcript recording no working
directory: both are streams no operator is sitting in. At most three rows come
from one project, so a tool that spawns a session per task cannot fill the page;
`--here` and `--all` lift that cap. A harness that cannot be enumerated is a
note, not a failure. Finding nothing prints `no sessions found` and exits 0.

The identifier in that table is the whole interface — any unique prefix works,
and it carries its own harness, so `watch`, `report` and `inspect` need no path.

### `instances`

The live-watch read-back, for other local tools.

```bash
flow-indicator instances --json
```

| flag | default | meaning |
|---|---|---|
| `--json` | `false` | required; there is no human-readable form |
| `--data-dir <path>` | XDG data path | storage root |

Reads local watch records only — no sessions, no source, no agents, no models.
Shape, liveness rules and the empty case: [INTEGRATIONS.md](INTEGRATIONS.md#instance-records).

---

## Watching a live session

### `watch`

Follow a session while it is being written and draw the meter.

```bash
flow-indicator watch --last          # the most recent session on this machine
flow-indicator watch --current       # the session for this working directory
flow-indicator watch --pane auto     # the agent pane sharing this pane's tab
flow-indicator watch 019ffd5a        # one named session, any harness
flow-indicator watch --adapter codex ~/.codex/sessions/2026/08/13/rollout-*.jsonl
some_stream | flow-indicator watch --adapter generic
```

**Which selector.** They differ in what they key on, and the difference matters
when more than one agent is running:

| selector | keys on | pick it when |
|---|---|---|
| `--current` | the working directory the session records | one agent per repository, shell sitting in it |
| `--last` | most recently written session on the machine | the meter's shell is not in the project |
| `--pane <id\|auto>` | the herdr pane | two agents share a directory, or you want restarts followed |
| `<session-id>` | the identifier from `sessions` | anything not current — an older session, another project |
| `<file>` | the path | a transcript outside the known stores |
| stdin | the pipe | a stream with no file at all |

`--current` prints the candidates and exits rather than guess when two sessions
for the directory were written within two seconds of each other. It never
reaches into another project. `--pane` is the disambiguation `--current` cannot
make; it also re-resolves every 5 s and follows the agent's next session across
a restart. See [HERDR.md](HERDR.md).

| flag | default | meaning |
|---|---|---|
| `--current` | `false` | the session recorded for this working directory |
| `--last` | `false` | the most recently written session on this machine |
| `--pane <id\|auto>` | `""` | the session the agent in a herdr pane is running |
| `--herdr-pane <id\|auto>` | `""` | also push phase, turn, elapsed and trend into that pane's sidebar row |
| `--herdr-workspace <id\|auto>` | `""` | push the same tokens onto that workspace's space row |
| `--tail-only` | `false` | follow from the end, building no state first |
| `--full` | `false` | the one-screen audit view instead of the side-pane meter |
| `--no-color` | `false` | no ANSI attributes; `NO_COLOR` is honoured |
| `--width <n>` | `0` | side-pane inner width in columns |
| `--adapter <name>` | `claude-code` | narrow discovery, or decode a named file as this |
| `--root <path>` | `""` | directory action targets are made relative to |
| `--from-start` | `false` | with a named file, read from the beginning |
| `--offset <n>` | `-1` (end) | with a named file, start at a byte offset |
| `--stream-id <id>` | harness id | override the stream identifier |
| `--force` | `false` | replace an existing session directory |

**Where it starts.** A session chosen through discovery is replayed from its beginning to
build state — about 0.2 s on a 6.5 MB session, 0.4 s on 20 MB — because a meter
that starts halfway through shows a regime derived from a fragment. Bootstrap
reads the source and never writes to it. A bare *path* starts at the end
instead, since a path is not always a session; `--from-start` and `--offset` are
start points, not resumes, and state is built from that byte forward.

`--adapter` only narrows discovery when you actually pass it. Passing one that
contradicts a `--pane` result is an error, not an override.

**Ctrl-C** records that observation stopped, where it reached, and what was left
open, then writes the projections and exits 0. It concludes nothing about the
interaction: an open repair stays open.

---

## Reading a result

### `replay`

Read a finished session and write its projections. The same code path `watch`
runs, without the terminal.

```bash
flow-indicator replay --adapter generic fixtures/recent-thrash/input.jsonl
flow-indicator replay --adapter opencode ~/.local/share/opencode/opencode.db#ses_abc123
```

| flag | default | meaning |
|---|---|---|
| `--adapter <name>` | `generic` | harness that wrote the source |
| `--root <path>` | `""` | directory action targets are relative to |
| `--stream-id <id>` | harness id | override the stream identifier |
| `--semantic-session <dir>` | `""` | replay strictly against semantic results a previous run retained |
| `--force` | `false` | replace an existing session directory |
| `--quiet` | `false` | write the files without the summary line |

Exactly one source. Prints one line and the session directory:

```text
RECOVERY SI 4.8x    warn CPB 58%   OBL 12  DRP 67%    REPAIR depth 1
```

Reach for `replay` over `watch` when the session is over, when you want a
session read more closely under `openai-compatible` (0.3–0.5 s per record, too
slow for a live pane), or when you want the artifacts without occupying a
terminal.

### `report`

Print the stored report for a session, rendered from its events.

```bash
flow-indicator report --session recent-thrash
```

`--session <stream-id>` is required.

### `inspect`

Source drilldown for one turn: source path, byte offset, record SHA-256,
snippet, observations, classifications, state transitions with the rule and
license that decided each, and the metrics that turn produced.

```bash
flow-indicator inspect --session recent-thrash --turn 19
flow-indicator inspect --session recent-thrash --turn 19 --micro
```

| flag | default | meaning |
|---|---|---|
| `--session <id>` | required | the session |
| `--turn <n>` | required | the turn |
| `--micro` | `false` | redraw the side-pane meter as it stood at that turn |
| `--no-color`, `--width <n>` | | as in `watch` |

This is the command that answers "why did it say that". Every anchor
`replay-set` writes drills back to source through it.

### `replay-set`

Replay a manifest of sessions into one table.

```bash
flow-indicator replay-set --adapter claude-code \
  --manifest sessions.txt --output results.jsonl
```

| flag | default | meaning |
|---|---|---|
| `--manifest <file>` | required | one source path per line; blank lines and `#` comments ignored |
| `--output <file>` | required | JSONL, one row per session |
| `--anchors <file>` | `<output>-anchors.jsonl` | source coordinates |
| `--adapter <name>` | `claude-code` | harness that wrote the sources |

Each row carries counts, regimes seen, the first transition to each non-FLOW
state, and extremes. The anchors file carries the source path, sequence number
and byte offset of every correction, interruption, repair opening, thrash
transition, the first non-FLOW transition, and the three highest-inflation
turns.

A source that fails to decode becomes a row with an `error` field; the run
continues and exits 0. Session directories are replaced without asking, since
each is rebuilt from its source.

It emits no score, no health label and no ranking.

---

## Calibration

Four commands in sequence. They exist because the classified families are
calibrated against labeled sessions rather than tuned until the numbers look
right. Read [METRICS.md](METRICS.md) before using them.

### `corpus`

Survey sessions, then seal the selected ones into a manifest.

```bash
flow-indicator corpus --scan ~/.claude/projects                      # survey only
flow-indicator corpus --scan ~/.claude/projects --build --out corpus/
```

| flag | default | meaning |
|---|---|---|
| `--scan <dir>` | harness default | directory to survey |
| `--harness <name>` | `claude-code` | which harness's format to read |
| `--build` | `false` | write the manifest; without it, survey and take nothing |
| `--out <dir>` | `""` | corpus directory; required by `--build` |
| `--copy` | `false` | copy sessions in instead of referencing them where they lie |
| `--per-project <n>` | `2` | at most this many sessions from one project; 0 means no cap |
| `--min-turns <n>` | `12` | fewest operator turns a session must carry |
| `--max-bytes <n>` | `33554432` | skip sessions larger than this |
| `--quiet-for <dur>` | `1h0m0s` | skip sessions written to more recently than this |

Sessions are referenced where they lie and sealed by hash; duplicates are
detected by content. The dev/holdout split is a function of each session
identifier, stratified by project, so nobody chooses which sessions the holdout
gets, and a session already in the manifest keeps the split it had. The manifest
is written 0600 under a 0700 directory.

### `label`

Emit the units of one family for a labeler to fill in. Source text and no
verdict: a labeler shown what the build decided agrees with the build, and
labels produced that way measure the agreement rather than the rule.

```bash
flow-indicator label --manifest corpus/manifest.json --family obligation \
  --sample-per-mille 150 > units.jsonl
```

| flag | default | meaning |
|---|---|---|
| `--manifest <file>` | required | the sealed corpus |
| `--family <name>` | `obligation` | `obligation`, `obligation_pair`, `recovery`, `pollution` |
| `--session <id>` | whole split | one session instead |
| `--split dev\|holdout` | `dev` | which side |
| `--sample-per-mille <n>` | `0` | this many operator turns per thousand; 0 emits all |

Units go to stdout, the summary to stderr, so a redirect captures only units.

### `annotate`

Run a model over those units and write candidate labels.

```bash
flow-indicator annotate --manifest corpus/manifest.json --out labels-model/ \
  --endpoint http://127.0.0.1:8000/v1/chat/completions --model your-local-model
```

| flag | default | meaning |
|---|---|---|
| `--manifest <file>` | required | the sealed corpus |
| `--out <dir>` | required | where candidate labels are written |
| `--model <name>` | required | model to send |
| `--endpoint <url>` | `http://127.0.0.1:8000/v1/chat/completions` | OpenAI-compatible chat completions |
| `--family <name>` | all four | one family instead |
| `--split dev\|holdout` | `dev` | which side |
| `--sample-per-mille <n>` | `0` | this many operator turns per thousand |

Two passes run per family, differing in order and wording, so their agreement is
evidence about the codebook rather than about the sampler. Files land at
`<out>/<split>-<family>-<pass>.jsonl` and name the model that produced them. A
pass that assigns one class to everything is called out as measuring nothing.

A model judgement is not ground truth; it is a candidate an adjudicator accepts
or rejects. The key comes from `FLOW_INDICATOR_API_KEY`.

### `calibrate`

Score the rules against the filled-in labels.

```bash
flow-indicator calibrate --labels labels/ --manifest corpus/manifest.json --split dev
```

| flag | default | meaning |
|---|---|---|
| `--labels <dir>` | required | the label files |
| `--manifest <file>` | required | the sealed corpus |
| `--split dev\|holdout` | `dev` | which side to score |
| `--classifier heuristic\|configured` | `heuristic` | which evidence path to score |
| `--reason <text>` | `""` | why the holdout is being opened; required for `--split holdout` |
| `--json` | `false` | the run record as JSON instead of the table |

Every run prints the artifact, corpus manifest, rule and label-set hashes it ran
under, and every score is printed with its operator-facing consequence beside it
— "the inventory is inflated 3.1x", "one repair episode in four was seen". A
score that improves while that does not is a score measuring the harness.

The split is by session, never by turn: turns inside a session are not
independent. Opening the holdout requires a stated reason and is recorded.

---

## Recipes

**Meter beside a running agent, one repository, one agent.**

```bash
cd ~/src/parser && flow-indicator watch --current
```

**Meter beside a running agent, several agents in one repository.**

```bash
flow-indicator watch --pane auto
```

**No pane to spare for the meter.** Push the phase into the agent's own sidebar
row instead:

```bash
flow-indicator watch --pane w2E:p1 --herdr-pane w2E:p1
```

**A session ended badly and you want to know where it turned.**

```bash
flow-indicator sessions
flow-indicator replay --adapter codex <path>      # or: watch already stored it
flow-indicator report  --session 019ffd5a
flow-indicator inspect --session 019ffd5a --turn 19
```

**Read a finished session more closely than markers allow.** Set
`classifier.mode` to `openai-compatible` in the config, then `replay`. Leave the
live pane on `heuristic` or `hybrid`. What that sends and to where:
[INFERENCE.md](INFERENCE.md).

**Compare many past sessions.**

```bash
flow-indicator sessions --all --adapter claude-code
# put the paths you want in sessions.txt
flow-indicator replay-set --manifest sessions.txt --output results.jsonl
```

**Ask what the machine is measuring right now.**

```bash
flow-indicator instances --json
```
