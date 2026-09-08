# Changelog

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Hybrid watch rebuilds semantic projections off the ingestion loop, coalesces
  obsolete work, and commits bounded deltas instead of copying event history.
- Every eligible hybrid/deferred record has a durable disposition. Bootstrap
  uses markers across all harnesses; semantic queues serve new arrivals.
  Timeouts are terminal and queued work is recorded as canceled on shutdown.
- Semantic prompt version 6 uses lowercase context fields, explicit byte
  lengths and correction targets, source-derived new obligation keys, and
  exact outstanding keys for semantic repeats. `classifier.constrained_json`
  enables JSON-schema decoding on supporting endpoints.
- The semantic prompt specifies literal boolean/null repair verification and
  forbids placeholder obligations or evidence inferred from unavailable images.
- The full view shows validated/eligible coverage, failures, candidate inventory,
  dereference unknowns, recovery status and observed expansion counts.
  The full view retains shared columns and compact labels; model failures share
  the coverage line and recovery status is behind `--model-details`.
- The TUI omits the CLASS column; reports and events retain provenance.
- Forced watches remove stale projections before drawing. Instance records
  refresh on the one-second heartbeat independently of sidebar TTL refresh.
- Calibration records include the profile hash and semantic prompt hash.

### Added

- .agents/skills/user-onboarding/SKILL.md guides local installation and later
  configuration updates through privacy choices, optional profiles and semantic
  endpoints, session discovery, and a first replay or watch.

- .agents/skills/calibration-loop/ routes corpus sealing, candidate annotation,
  human adjudication, and calibration through merged artifacts.

- .agents/skills/transcript-triage/ resolves transcript-coordinate mismatches
  before source-order investigation and evidence-preserving remediation.

## [0.4.1] - 2026-09-03

### Added

- `flow-indicator config init` writes the complete default configuration to the
  XDG path, creates its parent directory and refuses an overwrite.

- `flow-indicator config show` prints the complete effective configuration,
  including built-in defaults when no default configuration file exists.

## [0.4.0] - 2026-08-28

### Added

- `watch --herdr-workspace <id|auto>`: push the meter's four tokens onto a
  workspace's space row through `herdr workspace report-metadata`. herdr draws
  a pane's sidebar row only for panes it has promoted to agents, so a meter
  watching an unpromoted pane pushed tokens no row would ever render; the
  space row exists for every workspace.

- `auto` on both reporter flags. `--herdr-pane auto` reuses `--pane auto`
  discovery — the agent pane sharing this pane's tab, widening to its
  workspace, two candidates an error naming each — and `--herdr-workspace
  auto` is the workspace this process runs in, from `HERDR_WORKSPACE_ID` with
  `herdr pane current` as the fallback.

- `docs/HERDR.md`: the sidebar row config that renders the tokens. herdr
  displays a custom token only where `[ui.sidebar]` rows name it, and its
  default rows name none, so pushes landed in the daemon and drew nothing
  until that block existed.

### Fixed

- Sidebar pushes after a meter restart were silently dropped. herdr keeps each
  source's report `--seq` as a high-water mark that outlives the reporting
  process, and the reporter's counter restarted at 1. Seq is now wall-clock
  nanoseconds, the scheme herdr's own hook integrations use.

## [0.3.0] - 2026-08-17

### Added

- FLOW and DRIFT audit-view screenshots in the README.

- `docs/COMMANDS.md`, `docs/HERDR.md`, `docs/INTEGRATIONS.md`,
  `docs/PROFILES.md` and `docs/INFERENCE.md`. `docs/LOCAL_MODELS.md` is rolled
  into `INFERENCE.md`, which adds the local-versus-cloud endpoint boundary, what
  is sent per turn, and the validation that rejects a model reply rather than
  repairing it.

- `flow-indicator version`. The Makefile has always passed
  `-X main.version` and `-X main.commit`, and neither symbol existed, so every
  stamped build discarded them silently. Both exist now, `version` prints them,
  and a build made without them says `dev` plus the revision the toolchain
  recorded rather than claiming a release.

- `docs/CALIBRATION.md`: the process around the calibration commands — why the
  loop exists, how a corpus is sealed and split, what a candidate label is and
  is not, which layer may be tuned, what the runs have measured so far, and the
  failure modes that produce a number about nothing.

- `qwen` harness. Qwen Code's transcript shares Claude Code's lineage — one
  JSONL file per session, a uuid/parentUuid chain, cwd on every record — but its
  message envelope (`parts`, `functionCall`/`functionResponse`) does not agree
  with it, so it decodes through its own adapter rather than reusing Claude's.
  Sub-agent streams live under a sibling `subagents/` directory. Four coding
  agents now decode into the same canonical records.

- `flow-indicator instances` and `watch --herdr-pane`. A file-backed `watch`
  writes one local operational record — liveness coordinates and a small derived
  metric subset, never transcript text or event data — that other local tools
  read through `instances --json`. An empty list is a successful observation
  that no watches are live, and a record whose process is gone is excluded after
  a TTL probe rather than reported as a stopped interaction. `--herdr-pane`
  separately pushes four display tokens into a herdr sidebar row with a 30s TTL,
  so the phase is visible without a pane of its own; a failed push is ignored,
  and the row expires rather than outliving the process.

- `flow-indicator replay-set` replays a manifest of sessions into one table: one
  JSONL row per session — counts, regimes seen, first transition to each
  non-FLOW state, extremes — and a second file of anchors carrying the source
  path, sequence number and byte offset of every correction, interruption,
  repair opening, thrash transition and the three highest-inflation turns. Every
  anchor drills back to source through `inspect`. It runs the ordinary replay
  path and emits no score, no health label and no ranking.

- `watch --pane <id|auto>`: pane-scoped session discovery under herdr.
  `--current` keys on the working directory, and two agents in one directory
  made it a coin flip — the measured fleet had three agent panes recording the
  same cwd (2026-08-15). The session id is herdr's `agent_session` join,
  uniformly for every agent: each installed herdr integration reports it, and
  no agent-specific scraping exists (an earlier draft scraped a claude env
  variable and a codex state database; both were second data paths that rot
  per agent and were removed). `auto` picks the agent pane sharing the
  invoking pane's tab, widening to the workspace; ambiguity prints the
  `--pane` choices and exits. A pane whose integration has not reported yet
  is waited on with a printed reason rather than failed. The watch is keyed
  to the pane: it re-resolves every five seconds and follows a new session —
  an agent restart — from its beginning; only a positive resolution to a
  different id switches, and errors or empty gaps change nothing.

- Hybrid local-model classification for live watches. The marker tier remains
  immediate and authoritative for the live projector; a bounded worker stores
  late semantic completions with source, classifier and input provenance. The
  views expose semantic completion, queueing, failures, drops and latency as
  operational facts that no regime rule reads.

- An action vocabulary. Adapters read a record's tool calls into a fixed, small
  set of verbs — `read`, `write`, `execute`, `search`, `ask`, `unknown` — with
  the paths each named, relative to the session root. The Claude Code adapter
  excluded tool calls from a record's text on the grounds that a call is not a
  message. That is right about text and wrong about evidence: every question the
  classified families struggle with is a question about what the agent did, and
  571 such calls in one seed session reached nothing. Actions are observed, not
  classified, and every harness tool name stays inside its own adapter.
- `flow-indicator calibrate` scores what the rules said against what a labeler
  judged, per family: confusion matrix, precision, recall, F1, and the
  operator-facing consequence beside them. Every run prints the artifact, corpus
  manifest, rule and label-set hashes it ran under. A score that cannot name its
  own subject is not evidence.
- `flow-indicator label` writes the units of one family for a labeler to fill
  in. It prints the source text and no verdict: a labeler shown what the build
  decided agrees with the build. `--sample-per-mille` draws a sample over
  operator turns rather than sentences — the turn is the unit a labeler reads
  anyway, so one pass over it yields every family, and selection is a hash of the
  record identifier so it refers to nothing the build decided.
- `flow-indicator corpus` surveys a directory of harness sessions and seals the
  selected ones into a manifest. Sessions are referenced where they lie rather
  than copied: the seal is the hash, and duplicating transcripts doubles what has
  to be protected without making the seal stronger. The dev/holdout split is a
  function of each session's identifier, stratified by project, so nobody chooses
  which sessions the holdout gets.
- Repair episodes open on structure as well as on words. A repair opens when the
  response cycle following an operator turn writes to a path the immediately
  preceding cycle also wrote — the agent re-editing what it just produced, right
  after the operator spoke. It reads no vocabulary and has no free parameter, and
  it finds the corrections that carry no correction marker. A re-instruction
  such as "install this to userspace instead of opt" is one, and the marker
  table could not see it.
- A harness layer. `internal/harness` discovers and decodes sessions from Claude
  Code, OpenAI Codex CLI, opencode and the generic format behind one interface,
  so every rule, metric and calibration works across all of them without being
  told which produced a transcript. An adapter turns bytes into records, which is
  the right shape for a file being tailed and the wrong shape for a store kept in
  SQLite.
- Profiles. The operator-variable lexicon moved out of the rules and into a
  profile: a baseline embedded in the binary, and an optional operator overlay
  merged over it. The structural rules stayed in code, because whether a sentence
  has a subject before its marker is not a fact about a person. An overlay
  changes the classifier's hash, so classifications made under one are never
  confused with the baseline's.
- `flow-indicator annotate` runs a model over emitted units and writes candidate
  labels. Two passes per family, differing in order and wording, so their
  agreement is evidence about the codebook rather than about the sampler; a pass
  that collapses to one class is called out as measuring nothing.
- `Capabilities()` on the `Classifier` interface, naming the facts a classifier
  can establish. Every snapshot carries the declaration, and the views render an
  unestablishable fact differently from an unknown measurement. The repair
  pollution row read `—` on every turn of every heuristic session, which is what
  a measurement that kept coming back unknown looks like; it now says it was not
  measured. Same for the obligation counts that need a verification tier.
- Fixture `released-obligation`, holding the one property no other fixture has:
  the unresolved inventory falls and then recovers. The three naturalistic
  sessions in the review corpus contain no operator release language at all,
  which is why this is synthetic.

### Changed

- The obligation codebook has a third class. `task` is a one-shot instruction —
  asked for, done, discharged — and it used to be forced into `description`,
  where it hid the family's real gap inside a single recall number. It scores as
  a negative and appears in its own confusion row. Existing label files load
  unchanged; labels collected under the two-class codebook are not comparable to
  labels collected under this one, so a score names the label set it ran against.

- `unlabelable` means the class cannot be read, never "this is not a sentence".
  A unit marked unlabelable is dropped before any true or false positive is
  counted, so a rule firing on pasted output was invisible — 115 of 822 units in
  one run carried a marker. A verb-less fragment places no requirement, so it is
  `description`.

- The pollution unit looks forward. It showed an operator turn and the cycle
  *before* it while asking what the agent did about that turn: question and
  evidence named different turns, and two labeling passes agreed at κ −0.14,
  worse than chance. The unit now carries the response — writes, failed calls,
  what the agent said, what the operator said next — and reaches κ 0.17 with no
  rule changed.

- Obligation units carry their turn's line count, and the labeler is shown it
  past the same 40-line threshold the classifier reads as bulk paste. 83% of one
  corpus's labeled directives came from turns over that size. The codebook
  decides such a sentence on its addressee: a rule for the agent inside a long
  specification is a directive; a report of what some other team must do is not.


- Repair pollution is decided from the write set. The target was repaired when a
  write action named a path the correction named; the response expanded scope
  when the write set holds a path the correction did not name. Both are exact,
  with no threshold and no phrase list, and the default classifier now declares
  `verified_repair`. This retires the "I also updated" marker on any record that
  carried actions. A correction naming no path still leaves pollution `unknown`,
  and the premise now says which of the three unknowns it is.
- Obligation candidates are decided by structure, not by word presence. A marker
  makes a sentence a candidate; a sentence inside a fence or block quote, or one
  with a subject before its marker and no address to the agent, is read as
  describing rather than directing and never reaches the inventory. Word presence
  was being used to decide a speech act: `` `the runner` never sends the
  provenance challenge `` is a description of a program, and it was becoming a
  requirement.
  Two known costs are recorded and pinned in tests — a directive stated in the
  third person, and a request and a constraint in one sentence.
- Obligation repeats compare content tokens rather than the normalized sentence.
  Exact comparison matched nothing a real operator writes twice, because an
  operator restating a requirement rewords it. Negations collapse to one token so
  polarity is preserved: dropping them would make "always run the tests" and
  "never run the tests" the same requirement.

- Obligation resolution. A candidate can leave the unresolved inventory, so the
  count can go down. Before this it was monotonic: it counted what the operator
  asked for, never removed anything, and stopped carrying information after the
  first few turns.
  - `released` is the operator withdrawing a requirement. The marker tier
    establishes it: a release marker plus enough of the candidate restated
    alongside it to say which candidate is meant. A bare "never mind" names none
    and releases none, and a release that fits two candidates equally resolves
    neither.
  - `satisfied` and `superseded` need a semantic classifier. Supersession is not
    a rule over the obligation surface, though it looks like one: token overlap
    cannot separate "only edit internal/worker" replacing "only edit
    internal/api" from "do not touch the release workflow" standing beside "do
    not touch the config", and a threshold low enough to catch the first drops a
    live requirement on the second.
  - Three guards stand between a classified resolution and the inventory. An
    agent turn never resolves an obligation, whatever it says about its own
    work. A classifier that has not declared it can establish a kind of
    resolution cannot assert one. A resolution stating no evidence is refused
    rather than stored with an empty license.
  - A resolved candidate the operator states again is revived under its original
    identity, with its repeat count intact, rather than introduced a second
    time. New events: `obligation_released`, `obligation_revived`.
- A sentence that withdraws a requirement no longer states one. "You can drop
  the scope rule" had been introducing a scope obligation on the word "scope",
  growing the inventory on the turn meant to shrink it.
- Heuristic classifier version 4 to 5; prompt version 3 to 4.

### Fixed

- Live instance discovery now probes a PID with signal 0 instead of reading
  `/proc/<pid>`. macOS has no `/proc`, so every live watch there was excluded
  from `instances --json` despite a current TTL and matching hostname.

- `--adapter` now constrains discovery before any harness store is read. It no
  longer enumerates every harness and filters afterward, so selecting a
  file-backed harness cannot invoke `sqlite3`. Pane resolution reads only the
  pane's mapped harness, and a named path no longer scans stores to infer its
  format.

- Per-command help now lists the common `--config`, `--data-dir` and `--profile`
  flags on all six commands that accept them. `calibrate --classifier` is shown
  as the `heuristic|configured` mode selector, not a path.

- The labeler no longer hides two endpoint failures. A reply cut off at the
  token limit parsed as malformed JSON, was retried identically, and was then
  abandoned 25 units at a time behind a count nobody reconciled. `finish_reason`
  is now read: truncation halves the batch instead of repeating the request, an
  empty message body is an error, any other non-`stop` reason is named, and
  abandoned units are announced.

- The operator profile overlay is loaded. `profile.Resolve` had no caller, so
  `$XDG_CONFIG_HOME/flow-indicator/profile.json` changed nothing and every run
  classified under the shipped baseline. `watch`, `replay`, `replay-set` and
  `calibrate` now resolve the overlay from the directory of the configuration
  file in use and hand one ruleset to every tier — the heuristic classifier, the
  marker half of `openai-compatible`, and the marker tier inside `hybrid` — so a
  session cannot carry two lexicons. `--profile <path>` names one explicitly and
  errors when it does not exist. An overlay changes the rule hash a run reports;
  `cmd/flow-indicator/profile_test.go` holds the wiring down by asserting the
  hash differs with and without one.


- `calibrate` reported the direction of its own error backwards. The
  operator-facing consequence assumed over-reporting and printed "inflated
  0.19x" for a fivefold under-report. It now states the direction it measured
  and names how many misses carried no marker at all.

  Found by the first scoring run (2026-08-13): a model labeled 11,910
  obligation sentences and 771 turns per family, twice each, under instructions
  differing in order and wording. Three of four families came back below the
  codebook's 0.7 reliability floor and are unusable as a scoring surface —
  `obligation_pair` kappa 0.00, `recovery` 0.15, `pollution` -0.13. Two are
  diagnosable as defects in the unit definition: the pair family asks whether a
  turn repeats an outstanding requirement while showing only the previous turn,
  and the pollution family asks about the cycle answering the previous turn
  while showing the cycle answering this one. Against the one usable family the
  rules score precision 0.865 and recall 0.162, and 619 of 727 misses carried no
  marker at all: the marker table is the ceiling. Nothing is promoted to ground
  truth and no rule was changed on the strength of it.

- A queued operator message with something attached decodes again. Claude Code
  writes `attachment.prompt` as an array of content blocks when the operator
  pastes an image alongside their words, and the adapter declared it a string;
  the unmarshal error abandoned the whole chunk, so ten sessions in a local
  history were entirely unreadable and a live watch would have failed the same
  way. Only the text blocks are read — the image arrives as base64 in the same
  array, and counting it as operator serialization would charge tens of thousands
  of characters to one paste.

## [0.2.0] - 2026-08-12

MVP release.

### Changed

- Three state transitions concluded more than their premises supported. Each is
  narrowed to what the evidence establishes, and the change costs metric
  availability.
  - `repair_deepened` requires the new correction's established target key to
    equal the open episode's. An episode being open while a correction arrives
    is coexistence in time, not target identity. A correction with an unknown or
    different target no longer deepens the open episode: that episode stops
    accruing and ends `unknown`, and the correction opens an episode of its own.
    An unknown target is not equal to another unknown target.
  - A compact reference resolves inside its own bounded outcome opportunity. A
    turn evaluates a reference only once the agent has acted since it was sent,
    and the first such turn is the operator's own response to what the agent did
    with it. A later correction settles a reference only when it names the same
    target; otherwise the reference expires `unknown`. One correction can no
    longer be reported as the failure of every reference outstanding at the time.
  - `active_obligations` is `unresolved_obligation_candidates`, and the status
    `active` is `unresolved`. Nothing in this build establishes that a stated
    requirement remains in force, so the inventory says what it is: candidates
    stated and not resolved since. The known-active obligation surface is
    unknown. `median_active_age_turns` is `median_candidate_age_turns`.
- `observation_stopped` carries only observed facts: why watching ended, the
  byte offset reached, and the last record read. The projector's inventory of
  state left open moved to a new derived event, `state_at_observation_stop`.
  Reporting projected state as observed had classed the projection as something
  read out of the source.
- A repair episode carries `last_activity` and `closed` in place of `end`.
  `last_activity` moves while the episode is open; `closed` is written only when
  the episode leaves the open state and is cleared when a later correction
  reopens it. An episode is never both `open` and carrying a closing timestamp.
- `repair_seconds` measures a closed episode to `closed` and an open one to
  `last_activity`, and `last_activity` moves on every record attributed to the
  episode rather than on operator turns alone. Only operator turns had moved it,
  so an episode whose agent worked for eight minutes and closed eighteen minutes
  later reported the sixty seconds between the trigger and the correction.
  Durations for closed episodes in the naturalistic corpus rise.
- `repair_status` events carrying an episode status now also carry the episode,
  so a reader folding the log forward sees the status. The pollution assessment
  that shares this event kind reports under `pollution_status`, not `status`: a
  reader keying on the shorter name had counted an episode whose outcome was
  never established as a cycle assessed as unknown.
- The Claude adapter reads a tool result's answers as operator input only when
  the result is linked to an `AskUserQuestion` call. The link is the source's
  own `tool_use_id`. Any other tool's result carrying an `answers` object stays
  tool output.
- The semantic classifier is no longer sent agent records with no repair episode
  open. Their semantic fields fold only into an open correction response cycle,
  so the answer could reach nothing. `PromptVersion` is 3: the context object
  sent with the prompt changed shape.
- `fixtures/recent-thrash` reports three episodes at depth 1 rather than one at
  depth 3, and no longer reaches THRASH. Its three corrections name three
  different targets. No threshold or input changed; see the fixture README.
- Semantic claims narrowed where the first pass asserted more than the evidence
  supported. Each item is a smaller claim, not a new capability.
  - Permission to continue (`continue`, `proceed`, `go ahead`, `carry on`,
    `keep going`) is `continuation`, no longer `acceptance`. It does not close a
    repair episode and does not resolve a compact reference.
  - An episode that runs out of its durability window, or that is open when the
    source ends, gets status `unknown` with a recorded reason. `abandoned` is
    now a status nothing emits.
  - A correction matching an episode awaiting durability reopens that episode at
    greater depth. It no longer also opens a second episode.
  - `serialization_inflation` is measured against prior eligible turns only; the
    current turn joins the baseline after it is measured. The baseline is
    bounded by `window.rolling_turns`.
  - An agent's repair claim is recorded as `claimed_repaired`. `target_repaired`
    stays null in heuristic mode, so `repair_pollution_status` is `unknown`
    rather than `clean`.
  - Repair pollution reads the whole correction response cycle, every agent and
    tool record up to the next operator turn, not the first assistant record.
  - `obligation_violated` requires the correction's identified target to be that
    obligation. `obligation_repeated` carries `during_correction`.
  - RESET requires a marker naming a session transition. The bare nouns
    `handoff` and `new session` no longer reset an epoch; `hard stop` and
    `stop here` are stop markers only.
  - Heuristic marker table is version 2: added `i'm saying` and `i did not` as
    tier-one correction markers, against recorded operator language.

### Added

- Transition licenses. Every derived state transition stores the premise that
  permitted it, in `license`, `evidence` or `reason`, and `inspect --turn <n>`
  prints them under `transition license`. Provenance reachability is necessary
  and not sufficient: an unlicensed transition is reachable too. There is no
  `valid` flag; the gate is `internal/state/license_test.go` and review.
- `observation_stopped` event. A signalled watcher records the byte offset it
  reached, and concludes nothing. It no longer calls `Finish`, which had emitted
  `repair_abandoned` and resolved outstanding pointers to `unknown` on `Ctrl-C`.
- Claude adapter decodes operator input modes: typed input, `AskUserQuestion`
  answers, shell escapes, slash commands, interrupt markers, and command output.
  Structured answers now count as operator control; `<bash-stdout>` and
  `<local-command-stdout>` no longer count as operator serialization.
- Semantic classifier output is rejected for overlapping, out-of-order, or
  rune-splitting spans; text the model left uncovered is tiled as OTHER.
  Records with no semantic surface and turns over 32 KiB are not sent.
- `fixtures/SHA256SUMS` freezes every fixture input, enforced by
  `TestFixtureInputsAreFrozen`.

## [0.1.0] - 2026-08-12

### Added

- `replay`, `watch`, `report` and `inspect` commands over one binary.
- Adapters: `claude-code` and `generic`.
- Deterministic observation: bytes, characters, words, lines, quoted fraction,
  exact repetition, lexical overlap, timing.
- Heuristic classifier, version 1: a single versioned marker table with a
  SHA-256 fingerprint recorded on every classification.
- Optional `openai-compatible` classifier with strict output validation.
  Invalid output is recorded and processing continues.
- Append-only event store with deterministic event IDs and per-kind routing.
- Obligation surface, repair episodes, epochs, and the five-state regime with
  fixed precedence.
- The six metric families, each with an explicit unknown rule.
- One-screen ANSI live view, markdown report, `timeline.csv`, turn drilldown.
- Four frozen fixtures with expectations: `healthy`, `recent-thrash`,
  `bounded-rescue`, `large-data-dump`.

### Notes

- An open repair episode stops accruing after
  `window.correction_durability_turns` eligible operator turns without closing
  evidence, and its status becomes `unknown`. Left unbounded it accumulated
  recovery characters without limit and latched the regime at THRASH; found
  replaying a 7337-record session. The bound is mechanical and is not a finding
  about the episode. See "Judgement calls" in `docs/METRICS.md`.
