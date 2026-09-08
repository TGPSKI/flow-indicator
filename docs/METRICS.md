# Metrics

Six families. Every metric is a function of stored events, has an evidence
class, and has an explicit unknown rule. `unknown` marshals to `null` and never
satisfies a threshold.

Arithmetic lives in `internal/metrics`; the inputs are assembled in
`Projector.measure` (`internal/state/projector.go`). Metrics are computed on
operator turns only, one `metrics.Snapshot` per turn.

## Operator turns

A record counts as operator serialization when its speaker class is `human`, it
is not a sidechain record, and its input mode is one aimed at the agent
(`stream.Record.IsOperatorTurn`).

Two ordinals appear on every snapshot and they count different things. `seq` is
the ordinal of the record the snapshot was computed from, across every record in
the stream, so it advances on agent and tool records too. `turn_index` counts
operator turns only. A reader asking "which turn is this" means `turn_index`;
`seq` is for locating the record in the source.

A harness records more than typing under its user record type, and the modes are
evidence of different acts. Claude Code's, read from real session files:

| Mode | How it appears | Speaker class | Operator turn |
|---|---|---|---|
| `typed` | plain string or text blocks | human | yes |
| `structured_answer` | `toolUseResult.answers` on the tool result for an `AskUserQuestion` | human | yes |
| `shell_escape` | `<bash-input>` | human | no |
| `cli_command` | `<command-name>` | human | no |
| `interrupt` | a text block reading `[Request interrupted by user]` | system | no |
| — | `<bash-stdout>`, `<local-command-stdout>`, `<task-notification>` | tool | no |
| — | tool results, `toolUseResult` present | tool | no |

Structured answers are operator control that the harness happened to encode
inside tool plumbing. Only the answers are the operator's; the questions were
the agent's, and the record carries the answer values alone.

The tool result must be linked to a call of `AskUserQuestion` for its answers to
count as operator input. The link is the source's own: the result block names the
call it answers, and the call named its tool. A result from any other tool that
happens to carry an `answers` object stays tool output, because nothing about it
was typed by the operator.

The answers arrive as a JSON object keyed by the agent's question, and a JSON
object carries no order. They are joined in **lexical order of those keys**,
which is neither the order the questions were asked nor an order the source
records. Lexical order is chosen only so that a replay of the same file produces
the same text; nothing may read it as evidence of the operator's sequence.

Shell escapes and slash commands are operator-authored but aimed at the harness,
not at the agent. Charging their characters to the cost of steering the agent
would inflate every transmission metric. Command output was authored by neither
party and is speaker class `tool`.

An interruption and a shell escape are not corrections. They are recorded as
what they are; classification follows content.

## Family 1 — transmission load

| Metric | Formula | Class | Unknown when |
|---|---|---|---|
| `user_chars` | UTF-8 rune count of the turn | observed | never |
| `quoted_fraction` | quoted chars / chars | observed | never; 0 for an empty turn |
| `repeat_fraction` | repeat chars / chars | observed | never; 0 for an empty turn |
| `baseline_chars` | median `user_chars` of the eligible **prior** turns in the window | derived | fewer than `minimum_control_baseline` prior eligible turns |
| `serialization_inflation` | `user_chars / baseline_chars` | derived | baseline unknown or zero |

Quoted characters are fenced blocks, quote-prefixed lines, and paired inline
backtick spans. Repeat characters are the characters of lines whose normalized
form already appeared in an earlier operator turn inside the window; lines
shorter than 8 normalized characters are excluded, because short agreement is
noise.

A turn is eligible for the control baseline when it is a non-correction steering
turn outside an open repair episode. That is the whole rule, and it is not a
success test: these turns are not known to have worked, they are the turns not
visibly spent on repair.

The baseline holds prior turns only. The turn being measured joins it after its
own snapshot is computed, because a turn inside its own denominator measures
nothing — its inflation is pulled toward 1 by its own size. The baseline is
bounded by `window.rolling_turns` and cleared by a reset, so it describes recent
practice rather than the session.

A turn carrying a compact reference is held out until its outcome is known: it
joins the baseline on `success`, and on `unknown` as well, because a window that
closed without evidence says nothing against the turn. Only `failure` discards
it. No baseline is invented before the minimum is met.

## Family 2 — control-plane burden

Segment labels and their buckets, from `internal/classify/classifier.go`:

| Bucket | Labels |
|---|---|
| FORWARD | `forward_work`, `new_task`, `new_evidence` |
| CONTROL | `scope_constraint`, `negative_constraint`, `positive_constraint`, `stop_condition`, `meta_process`, `handoff` |
| RECOVERY | `correction`, `referent_disambiguation`, `temporal_disambiguation`, `namespace_disambiguation`, `restart_reconstruction`, `handoff_after_failure` |
| RESTATE | `restated_prior_state` |
| OTHER | `other`, and any label not listed above |

Unlabeled text is OTHER. It stays in the denominator and is never counted as
forward work.

`handoff_after_failure` exists because a bucket must be a pure function of a
label: a handoff issued while a repair episode is open carries its own label
rather than a conditional bucket.

| Metric | Formula | Class | Unknown when |
|---|---|---|---|
| `forward_work_share` | FORWARD / total | derived | total is 0 |
| `control_plane_burden` | (CONTROL + RECOVERY + RESTATE) / total | derived | total is 0 |
| `restate_burden` | RESTATE / total | derived | total is 0 |

`total` is every classified operator character in the window, OTHER included.
The window is the last `rolling_turns` operator turns of the current epoch.

The heuristic classifier never produces `namespace_disambiguation`; only a
semantic classifier does.

## Family 3 — obligation surface

This family reports an inventory of candidates, not a set of requirements known
to remain in force. The two are different claims, and only the first is
supported. A candidate is here because the operator stated it and nothing since
resolved it; that is a fact about the instrument's evidence.

**The known-active obligation surface is unavailable. It is unknown, not zero
and not the inventory count.** Resolution removes candidates from the inventory;
it never establishes that the rest are in force.

### What removes a candidate

Four kinds of evidence, and which are reachable depends on the classifier. Each
snapshot carries `classifier_capabilities`, so a zero can be read against what
the build could establish.

| Status | Evidence | Capability | Marker tier |
|---|---|---|---|
| `released` | The operator stated the requirement no longer applies | `obligation_release` | yes |
| `superseded` | A later requirement replaced this one | `obligation_supersession` | no |
| `satisfied` | The requirement was met, established by something other than the agent's account of its own work | `obligation_satisfaction` | no |
| `unknown` | A reset destroyed the evidence that would have resolved it | — | yes, via `epoch_advanced` |

Three guards stand between a classified resolution and the inventory, because
this is the only path that drops something the operator asked for.

1. **Operator turns only.** An agent turn never resolves an obligation. "I ran
   the suite and it passed" is the agent's account of its own work, which is the
   same claim as `claimed_repaired` and is not evidence either time. Whatever
   establishes satisfaction comes from the operator's side of the exchange.
2. **The capability check.** A classifier that does not declare it can establish
   a kind of resolution does not get to assert one.
3. **The premise.** A resolution stating no evidence is refused rather than
   stored with an empty license. The license is what a reader audits when a
   requirement stops being counted.

A release marker says a requirement was dropped and does not say which one. The
binding is the rest of the sentence: at least half of the candidate's tokens
restated alongside the marker, at least two of them, and exactly one candidate
clearing that bar. A bare "never mind" names no candidate and releases nothing,
which is the right answer rather than a missed one — it shares the word "never"
with "never touch the release workflow" and says nothing whatever about it.

Supersession is not a rule over the obligation surface, though it looks like
one. Token overlap cannot separate a replacement from an independent second
requirement: "only edit internal/worker" against "only edit internal/api" and
"do not touch the release workflow" against "do not touch the config" have the
same shape and the same overlap, and only the first pair is a replacement. What
separates them is whether the two objects are alternatives within one dimension,
which is a semantic judgement. A similarity threshold set low enough to catch
the first pair drops a live requirement on the second, so the marker tier does
not attempt it.

### Revival

A resolved candidate the operator states again returns to the inventory under
the identity it had before: same id, repeat count incremented,
`obligation_revived` naming the status it came back from. A second candidate
with a fresh identity would report a first statement where the session shows a
re-assertion, and would lose the round trip.

An obligation is keyed by its normalized sentence: case folded, whitespace
collapsed, surrounding punctuation trimmed. Comparison is exact on that key.
Similarity is not identity — a token Jaccard of 0.80 or higher against an
unresolved candidate emits `near_repeat_candidate`, which only a semantic
classifier may promote to a repeat.

Unresolved means status `unresolved` or `violated`. A violated candidate stays
in the inventory: being violated once does not resolve it.

| Metric | Formula | Class | Unknown when |
|---|---|---|---|
| `unresolved_obligation_candidates` | count of unresolved | derived | never |
| `new_obligations` | introduced this turn | derived | never |
| `repeated_obligations` | unresolved candidates with `repeat_count > 0` | derived | never |
| `violated_obligations` | count with status `violated` | derived | never |
| `released_obligations` | count with status `released` | derived | never |
| `superseded_obligations` | count with status `superseded` | derived | never; 0 under the marker tier, which cannot establish supersession |
| `satisfied_obligations` | count with status `satisfied` | derived | never; 0 under the marker tier, which cannot establish satisfaction |
| `mean_repeat_count` | mean `repeat_count` over unresolved | derived | no unresolved candidates |
| `median_candidate_age_turns` | median age in operator turns over unresolved | derived | no unresolved candidates |
| `obligation_repeat_ratio` | repeated unresolved / unresolved | derived | never |

`median_candidate_age_turns` measures how long the inventory has been carried,
not how long any requirement has been in force.

ORR is 0, not unknown, when the inventory is empty: with nothing recorded there
is nothing to repeat, and that is an answer.

An obligation becomes `violated` only when a correction's identified target key
is that obligation's key. A repeat inside a correction turn is recorded as a
repeat — `obligation_repeated` carries `during_correction` — and that is the
whole supported statement. An operator correcting a file path while restating an
unrelated scope rule has said nothing about the scope rule.

The linkage rule is narrow on purpose, and it costs recall. On a real session
where the operator restated the same constraint four times over two and a half
hours, `repeated_obligations` is 0: each restatement was worded differently, and
exact-key comparison finds none of them. The near-repeat threshold did not fire
either. This family reports what it can key exactly; the rest needs a semantic
classifier.

## Family 4 — dereference reliability proxy

A compact reference is held for `dereference_outcome_turns` of its own outcome
opportunities and resolved from what the operator did next about *that*
reference.

Each reference has its own opportunities, and one operator turn is not all of
them at once. A turn counts as an opportunity for a reference only once the
agent has acted since that reference was sent: an operator turn the agent has
not answered evaluates nothing, and does not spend the window. Of the turns that
do evaluate it, the first is the operator's own response to what the agent did
with that reference.

| Outcome | Evidence |
|---|---|
| `failure` | The operator's first response to the agent's work on this reference corrected the agent |
| `failure` | A later correction whose established target key equals this reference's key |
| `success` | The operator's first response to that work accepted it or moved the work forward |
| `unknown` | No evidence about this reference inside its outcome window, or the epoch or the source ended first |

A later correction that is neither of those settles nothing here. It is one
operator judgement about one thing, and reading it as the failure of every
reference that happened to be outstanding would report a single correction as
several dereference misses. Where linkage is unavailable the outcome is
`unknown`.

| Metric | Formula | Class | Unknown when |
|---|---|---|---|
| `dereference_reliability_proxy` | success / (success + failure) | derived | no resolved outcome |

Unknown outcomes are excluded from both sides. Absence of a correction is not
success; success requires positive evidence. Display it as a proxy: it reports
what the operator did next, not whether a reference resolved.

## Family 5 — recovery telemetry

An episode opens when a correction targets preceding agent behaviour. Depth
starts at 1 and increments on each later correction *established as being about
the same target*: both the open episode and the new correction carry a target
key, and the keys are equal.

An episode being open while a correction arrives is coexistence in time, not
target identity. A correction with an unknown target, or with a different
established target, does not deepen the open episode. The open episode stops
accruing and ends `unknown`, because nothing established how it ended, and the
new correction opens an episode of its own. An unknown target stays unknown; it
is not equal to another unknown target.

| Metric | Formula | Class | Unknown when |
|---|---|---|---|
| `repair_depth` | corrections in the episode | derived | never |
| `repair_chars` (RCC) | operator characters inside the episode | derived | never |
| `repair_records` (RTC) | records inside the episode | derived | never |
| `repair_seconds` | `closed` − `start`, or `last_activity` − `start` while the episode is open | derived | either endpoint has no timestamp, or the later one precedes the earlier |
| `repair_magnification` (RM) | RCC / pointer characters of the trigger | derived | the trigger carried no pointer |

Lifecycle: `open` → `provisionally_closed` → `durably_closed`, with `reset` and
`unknown` as terminal alternatives. `abandoned` is a declared status with no
emitter: nothing in this build produces evidence that an operator abandoned an
episode.

**Membership bounds attribution.** An episode stops being the thing in play when
the operator is no longer visibly on it. Acceptance and forward work close it,
as they always did; so does a turn that carries none of the evidence that the
operator is still working the episode. That evidence is a continuation, a stop
marker, or characters in the CONTROL, RECOVERY or RESTATE buckets — "continue"
is permission to keep going on *this*, "stop" is halting *this*, and "only touch
internal/worker" is constraining *this*.

The close is provisional; durability still has to be earned in the recurrence
window, and the accrual bound remains the final mechanical limit. Two failure
modes this rule sits between, both observed in natural sessions:

- Without it, an episode opened by a mis-click stayed open through a closing
  stretch of unrelated task assignment and charged 1,089 characters of new work
  to it as recovery cost, reading THRASH over a session the operator rated best.
- Attributing only the characters the classifier labeled would have been worse:
  classifier silence would then decide *against* recovery just as wrongly as the
  bug decided *for* it, and a genuine high-cost repair whose re-serialization
  lands in OTHER would vanish. Unknown classification decides neither. Membership
  decides attribution; classification supplies the evidence for opening and
  closing the episode.

Two timestamps, because an episode has two different moments to record.
`last_activity` is the last record attributed to the episode; it moves while the
episode is open and says nothing about ending. `closed` is written only when the
episode leaves the open state, and is cleared again when a later correction
reopens it. An episode is never both `open` and carrying a closing timestamp.

`repair_seconds` measures to whichever of the two the episode has. A closed
episode ran until it left the open state, so it is measured to `closed`; an
episode still open has no end yet, so it is measured to `last_activity` and the
duration is partial. Attribution decides `last_activity`: every record inside
the episode moves it — operator, agent and tool alike — under the same boundary
that counts the episode's characters and records. The turn that closes the
episode is outside it, so `last_activity` stays at the last record inside while
`closed` carries the moment of closure.

- Provisional close needs the agent to have answered and the next operator turn
  to be a non-correction carrying explicit acceptance or forward work. An
  episode does not close because an assistant spoke, and it does not close on
  permission to continue: "continue" grants the agent leave to keep going and
  says nothing about whether the correction was met.
- Durable close needs `correction_durability_turns` eligible operator turns with
  no recurrence of the same target key. Without a target key, equivalence cannot
  be established and the episode ends `unknown`.
- A correction whose target key matches an episode awaiting durability reopens
  that episode at greater depth under its own ID. It is one episode with a
  recurrence, not two episodes: two records would say the operator corrected two
  different things.
- An episode with no closing evidence for `correction_durability_turns` turns
  stops accruing and ends `unknown`, with the reason recorded. The stop is
  mechanical — an episode left open would grow RCC without limit and latch the
  regime — and it is not a finding. Elapsed turns establish that the episode was
  unresolved when the window ran out, and nothing more.
- An episode still open at the last byte of the source ends `unknown` for the
  same reason. The source ending is not the operator giving up.
- A reset closes an open episode as `reset`. It is never counted as clean
  recovery.

## Family 6 — repair pollution

The whole response to a correction is read, not only the first assistant record:
every agent and tool record from the correction up to the record immediately
before the next operator turn. `cycle_records` in the event payload says how
many that was. An expansion announced three tool calls after the fix counts.

```
REC = new_scope + new_tasks + new_validation + new_constraints
```

Two different facts about the agent's response are recorded separately.

| Field | Meaning |
|---|---|
| `claimed_repaired` | The agent's text asserts the repair — "Reverted it.", "Removed it." |
| `target_repaired` | The repair is established. Null unless something establishes it. |

No marker in agent text verifies repair; the agent describing its own work is a
claim about that work. `target_repaired` is set from what the agent did:

- **repaired** when a `write` action in the cycle named a path the correction
  named;
- **expansion** when the cycle's write set holds a path the correction did not
  name.

Both are exact. No threshold, no phrase list. They need the record to carry
actions, which means an adapter that emits them — see the action vocabulary in
`internal/stream/action.go`.

| Status | Rule |
|---|---|
| `clean` | target repaired, REC = 0, no immediate correction |
| `polluted` | target repaired, REC > 0 |
| `failed` | target not repaired |
| `unknown` | nothing establishes whether the target was repaired |

`unknown` covers four different situations, and the `evidence` string on the
event says which:

| Situation | Why it is unknown |
|---|---|
| The correction named no path | There is no target to check a write against. Substituting the write set for a target the operator never named is the defect this vocabulary exists to catch. |
| It named a path, the cycle carried no observed write | Nothing to read. A source whose adapter emits no actions is always here. |
| It named a path and the writes missed it | Measured, and no named outcome fits. |

A build whose classifier cannot establish repair at all is rendered apart from
an unknown measurement. It says so — "not measured here" in the live view, "not
established by this classifier" in the report — rather than taking the unknown
glyph. The glyph would otherwise stand on every turn of every heuristic session
and read as a measurement that kept coming back unknown, when nothing was
measured. Each snapshot carries `classifier_capabilities` so a stored session
read back later can still tell the two apart.

One assessment is emitted per correction cycle, so an episode with three cycles
has three `repair_status` events. `summary.json` carries the full list per
episode in `pollution_by_repair`: a clean last cycle does not erase an earlier
polluted one.

## Regime

`internal/state/regime.go`. Precedence is fixed and evaluated in this order.
There is no composite score.

| Regime | Rule |
|---|---|
| RESET | The turn carries a session-reset marker |
| THRASH | An episode is open and any of: depth ≥ `thrash_repair_depth`; RCC ≥ 5 × `baseline_chars`; repeated unresolved candidates ≥ `repeated_obligations_warn` and SI ≥ `serialization_warn` and FWS ≤ 0.40 |
| RECOVERY | An episode is open |
| DRIFT | Not recovering and any of: 2 dereference failures within 8 eligible operator turns; 2 operator control actions within 8; SI ≥ `serialization_warn` with an obligation repeated within 8 |
| FLOW | No rule above applies |

Every evaluation returns a `Decision`: the regime, the identifier of the
comparison that fired, and that comparison in words. The identifier is one of

```text
flow  reset  recovery_open
thrash_repair_depth  thrash_recovery_chars  thrash_obligation_inflation
drift_dereference  drift_control_actions  drift_serialization_obligation
```

It is stored on every `metrics_computed` snapshot as `regime_rule`, in
`timeline.csv`, and on the `regime_changed` event alongside the license. The
side-pane meter formats its reason from that identifier. Nothing re-derives a
reason from the metric values: several are known on any turn and only one
comparison decided the state, so naming a known metric that no rule read would
tell the operator something false about why the meter moved.

An operator control action is a correction or an interruption. Both are the
operator spending a turn on the agent's behaviour instead of on the work, and
the rule counts them together.

Constants this document fixes rather than exposing as config:

| Constant | Value | Use |
|---|---|---|
| `thrashRecoveryMultiple` | 5 | RCC multiple of the baseline that is thrash on its own |
| `thrashForwardShare` | 0.40 | Forward-work share at or below which the third thrash rule can fire |
| `driftWindowTurns` | 8 | Width of the drift window, in eligible operator turns |
| `driftEventCount` | 2 | Failures or control actions inside that window that constitute drift |

All three drift rules read the same window. The obligation half of the third
rule was a per-epoch flag until this pass: one repeat made it true for the rest
of the epoch, so every later turn large enough to clear the inflation threshold
reported drift on evidence hundreds of turns old. One natural session drifted 37
times on a single repeat, the last of them 3,836 records later.

An unknown metric never satisfies a threshold. `Value.AtLeast` and
`Value.AtMost` are false for unknown, so a stream with no baseline cannot reach
THRASH by character volume and a stream with no inflation figure cannot reach
DRIFT by inflation.

A reset closes any open episode, marks unresolved obligations unknown,
increments the epoch, and starts fresh rolling baselines. Session-global
counters — pointer outcomes, the episode list — are kept.

## Configuration

`internal/config/config.go`. Loading is strict: an unknown key or an invalid
value is an error, never a silent fallback.

| Key | Default | Tunes |
|---|---|---|
| `window.rolling_turns` | 30 | Baseline window, bucket window, observation history |
| `window.correction_durability_turns` | 10 | Durable close, and the bound on an episode with no closing evidence |
| `window.dereference_outcome_turns` | 3 | How long a pointer waits for its outcome |
| `window.trend_durability_turns` | 2 | Consecutive turns a quality band must hold before a trend is reported |
| `thresholds.minimum_control_baseline` | 8 | Eligible turns required before `baseline_chars` is known |
| `thresholds.thrash_repair_depth` | 3 | Depth that is thrash on its own |
| `thresholds.serialization_warn` | 3.0 | SI threshold in the third thrash rule and the third drift rule |
| `thresholds.serialization_high` | 6.0 | Display only: the live view marks SI `warn` at `serialization_warn` and `high` here. No regime rule consumes it |
| `thresholds.repeated_obligations_warn` | 2 | Repeated unresolved obligation candidates in the third thrash rule |
| `classifier.mode` | `heuristic` | `none`, `heuristic`, `openai-compatible`, `hybrid` or `deferred` |
| `classifier.endpoint`, `classifier.model` | empty | Required when the mode is `openai-compatible`, `hybrid` or `deferred` |
| `classifier.live_deadline_ms` | 200 | Per-job deadline for the bounded hybrid or deferred worker; never a source-ingest deadline |
| `classifier.workers`, `classifier.max_queue` | 1, 32 | Bounded local-model concurrency and backlog in hybrid and deferred modes |
| `classifier.disable_thinking` | false | vLLM request option that asks a reasoning model to return `message.content` directly |
| `privacy.store_text` | false | Store full record text in events |
| `privacy.store_snippets` | true | Store a bounded excerpt |
| `privacy.snippet_chars` | 160 | Excerpt size |

## Quality bands and trends

A band is a coarse reading of a measurement that already exists: `good`, `mid`,
`bad`, or `unknown` when the measurement does not exist yet. Bands decide only
what gets reported as a crossing. No regime rule consults a band, so a band
cannot smuggle a second opinion into the state machine.

| Metric | good | mid | bad |
|---|---|---|---|
| `serialization_inflation` | `< serialization_warn` | `< serialization_high` | `>= serialization_high` |
| `control_plane_burden` | `<= 0.25` | `<= 0.50` | `> 0.50` |
| `forward_work_share` | `>= 0.60` | `>= 0.35` | `< 0.35` |
| `dereference` | `>= 0.80` | `>= 0.50` | `< 0.50` |
| `repair_depth` | `0` | `< thrash_repair_depth` | `>= thrash_repair_depth` |

Serialization and repair depth reuse the configured thresholds, so a band and a
regime rule cannot disagree about where a line is. The three fractions are
judgement calls, listed below.

A `trend_emerged` event is one metric crossing between bands and holding the
new band for `window.trend_durability_turns` consecutive operator turns
(default 2). One turn in a worse band is a turn; the same band held is a
direction. Three rules bound what is reported:

- A metric becoming measurable is not a crossing. There was no earlier band for
  it to have left.
- A return to the confirmed band before the crossing became durable resets the
  run. A round trip is not reported.
- An unmeasured turn holds the run rather than resetting it. A gap in the
  evidence is not a recovery.

## Judgement calls

18. **Hybrid projection updates are deltas, not checkpoints.** A background
   replay compares the selected reading with the preceding complete reading.
   Each `semantic_projection_delta` replaces one changed source record's
   projected events, split into parts targeting 256 KiB. A final
   `semantic_projection_updated` commits the parts and names newly selected
   completions. Uncommitted parts are ignored. Source-only arrivals can append
   changed records but never copy unchanged history. A checkpoint containing
   all snapshots and inventories was rejected because it repeats growing
   history. The five event files reproduce all four disposable projections
   without the source or a model call. Legacy full-history updates remain
   readable.

   One rebuild runs at a time. Arrivals invalidate an unfinished build; an
   obsolete result is discarded before publication, then the newest input is
   rebuilt. The screen keeps the preceding complete reading. Replaying the
   same records and retained selection produces identical deltas; live receipt
   timing and coalescing determine which intermediate selections were observed.

19. **Bootstrap uses markers; semantic work starts at the live tail.** Every
   eligible historical record receives a durable `dropped` disposition with
   its bootstrap reason. Historical turns never occupy the live queues.
   File-backed and database-backed harnesses identify their initial boundary
   through the same interface; classification never reads their formats.
   A timeout is terminal, with no automatic retry, so one eligible record has
   one terminal outcome. Coverage describes the instrument and never enters a
   regime comparison.

20. **Semantic obligation identities come from evidence.** A new candidate
   returns an empty key and exact source text; the core normalizes that text.
   A nonempty key asserts a semantic repeat and must copy an outstanding key.
   Invented keys and display IDs are rejected. Similar wording alone still
   establishes no identity under the marker tier.

17. **Late semantic evidence can update a named projection.** The original
   marker projection and each semantic completion remain append-only facts.
   In `hybrid` mode, a completed result appends a source-ordered
   `semantic_projection_updated` event built from the completions received so
   far; the renderer selects its committed projection. This resolves the prior
   contract wording that late results were evidence but never a revision:
   evidence still is not rewritten, while the disposable projection now names
   exactly which evidence changed it. `deferred` retains the former
   non-updating behavior for observers who want the marker reading held fixed.

Where a rule could be read two ways, this is what was decided and why. Each
entry states the reading that was rejected, so a change to one of them is a
change somebody can argue with.

1. **Correction markers are two tiers.** Tier one targets the agent's last
   behaviour (`^no`, `wrong`, `that's not`, `not what i`, `i said`, `i'm
   saying`, `i meant`, `why are you`, `you just`, `i did not`, `did not ask`,
   `i told you`) and is correction evidence on its own. Tier two is restatement
   language (`again`, `as i said`, `i already`, `this is the Nth time`) and
   escalates to a correction only when the same turn repeats an already-unresolved
   obligation. Reading `do not` and `don't` as correction markers was
   rejected: taken literally, every first-time constraint would open a repair
   episode and inflate depth. A first-time constraint is a constraint.

   `i'm saying` and `i did not` were added in the second pass against recorded
   operator language: "i did not say push." and `"let me test that claim" IM
   SAYING DELETE YOUR STUPID TESTS`. Case and profanity are not the signal and
   are not matched on.

   Recall is bounded, and the bound is recorded rather than patched. A
   correction phrased as re-instruction — "install this to userspace instead of
   opt" — carries no marker and is not detected. Neither is heavy mistyping:
   "i dontt hink you got the righ tone". A pattern written for one such line
   fits that typo, not the language.

2. **`wait for instructions` is a stop marker, not a reset. So are `hard stop`
   and `stop here`.** Reading them as either was defensible. RESET requires a
   marker that names a session transition being performed: `context clear`,
   `clear context`, `model switch`, `new chat`, or a phrase that hands the work
   off to or starts a new session. The bare nouns `handoff` and `new session`
   are not markers — "write a handoff document" and "new session handling is
   documented here" are ordinary work, and a lexical occurrence is not a state
   transition.

3. **A repair claim is not a verified repair.** The agent turn answering a
   correction sets `claimed_repaired` when it contains a repair verb
   (`reverted`, `removed`, `restored`, `closed`, `deleted`, `rolled back`, …)
   while an episode is open. `target_repaired` stays null: the agent describing
   its own work does not establish that the target the operator named was
   repaired. See family 6.

4. **FWS, CPB and RSB are windowed.** They are computed over the last
   `rolling_turns` operator turns of the current epoch, not over the whole
   session. A session-length denominator would bury a recent shift under an
   hour of earlier work.

5. **An open episode is bounded mechanically, and the bound is not a finding.**
   With no closing evidence for `correction_durability_turns` eligible operator
   turns, the episode stops accruing and ends `unknown`. Left open it would grow
   RCC without limit and latch the regime at THRASH; that is the reason for the
   bound. It is not a reason to call the episode abandoned. Elapsed turns
   establish that the episode was unresolved when the window ran out.

   The first pass reached the same bound by naming the outcome `abandoned` and
   by counting `continue`, `proceed`, `go ahead`, `carry on` and `keep going` as
   acceptance. Both were removed: permission to continue is not acceptance that
   a repair succeeded, and a timeout is not abandonment. The latching problem is
   solved by the accrual bound alone.

6. **RCC counts every operator character inside the episode.** A large legitimate paste inside an open episode therefore inflates
   it and can reach the thrash multiple on its own. The `large-data-dump`
   fixture proves only that length alone does not move the regime *outside* an
   episode.

7. **The marker table ships as heuristic version 5.** Its phrases live in
   `internal/profile/baseline.json`, not in a rule file; an operator overlay is
   merged over them. Any change to a phrase or to how a marker is applied
   requires bumping `HeuristicVersion`. Every classification also carries a
   SHA-256 fingerprint covering the version and the profile in force, so two
   builds that disagree on the rules are distinguishable even if the version
   constant was not bumped, and a session classified under an overlay is never
   mistaken for one classified under the baseline. `inspect` prints the value a
   given classification ran under; this document pins no literal, because a
   pinned hash rots silently.

8. **Segmentation is per sentence and tiles the turn.** Boundaries are newlines
   and sentence-final punctuation followed by whitespace; trailing whitespace
   belongs to the span it follows. Segment offsets therefore cover every byte of
   the turn, and bucket shares sum over the whole text.

9. **Metrics are computed on operator turns only.** Agent and tool records
   produce observations and, where applicable, classified candidates, but no
   `metrics_computed` event. The metric families measure operator serialization.

10. **Sidechain human records are not operator turns.** Text a harness records
    as a user message inside a nested agent stream was written by an agent.
    Counting it would inflate every transmission and control metric. The adapter
    marks it with the canonical `sidechain` metadata key; core packages read the
    key, never the adapter's own fields.

11. **Neither is a shell escape or a slash command.** They are operator-authored
    but aimed at the harness. The adapter marks them with the canonical
    `input_mode` metadata key, and `IsOperatorTurn` reads that key. Command
    output is speaker class `tool`; before this rule, `<bash-stdout>` counted as
    operator serialization and inflated the baseline with machine text.

12. **A compaction summary is not operator serialization.** When a conversation
    is compacted the CLI writes the context summary under the user record type,
    marked `isCompactSummary`. The operator typed the command; the harness
    composed the text, which runs to tens of thousands of characters. Counting
    it made a routine compaction the largest steering turn of a session: in one
    natural session three summaries carried 50,616 characters, drove
    serialization inflation to 450× and the final regime to THRASH. The adapter
    reads the flag, so nothing here matches text.

13. **A structured answer is operator control.** The operator's answers to an
    `AskUserQuestion` arrive inside the tool result for that call. Discarding
    them because the harness encoded them in tool plumbing would drop real
    operator steering. Only the answer values are kept.

14. **Watcher shutdown concludes nothing.** `Finish` settles open state as
    `unknown` at a real source end. A signalled observer calls `StopObservation`
    instead, which emits one `observation_stopped` event carrying the byte
    offset and an inventory of what was left open. Observation stopping is not
    the interaction ending, and `--offset N` starts a fresh projector at byte N
    rather than restoring state — it is a start point, not a resume.

15. **Band boundaries for the three fractions.** `control_plane_burden` at 0.25
    and 0.50, `forward_work_share` at 0.60 and 0.35, and `dereference` at 0.80
    and 0.50 are not derived from anything; they are chosen so that a crossing
    is uncommon enough to be worth reporting and common enough to be reached.
    They decide only what `trend_emerged` reports. Nothing else reads them, and
    moving them changes no metric and no regime.

16. **Live instance records are operational projections.** A watch on a
    discovered session
    writes a short-lived record under `instances/`, a sibling of `sessions/`.
    It carries process liveness, stream coordinates and seven existing derived
    values for local collection. It is not an event, does not enter a replay or
    metric calculation, and carries no transcript text. This narrowly releases
    the initial scope deferral for the operator's live local stack integration;
    it adds no remote telemetry or cross-session scoring.
