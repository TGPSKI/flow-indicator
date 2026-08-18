# Events

Every fact `flow-indicator` records is one event in one envelope. Event files
are append-only. A changed interpretation is a new event, never an edit.

## Envelope

Defined in `internal/event/event.go`. Schema version is the constant
`event.Version`, currently `1`.

| Field | JSON | Type | Meaning |
|---|---|---|---|
| Version | `version` | int | Event schema version |
| ID | `id` | string | Deterministic identifier, 32 hex characters |
| StreamID | `stream_id` | string | Stream the event belongs to |
| Seq | `seq` | uint64 | Source record ordinal, 1-based |
| Epoch | `epoch` | uint64 | Epoch at emission, incremented by a reset |
| Kind | `kind` | string | One of the kinds below |
| Source | `source` | object | Provenance: `adapter`, `path`, `offset`, `record_sha256` |
| Class | `class` | string | `observed`, `classified` or `derived` |
| Payload | `payload` | object | Kind-specific, shapes in `internal/state/payloads.go` |

`source.offset` is the absolute byte offset of the record in the source file.
`source.record_sha256` is the SHA-256 of the source line as it appeared.

## Identifiers

```
id = hex(sha256(stream_id \0 seq \0 kind \0 ordinal \0 schema_version))[:32]
```

`ordinal` counts events of the same kind within one source record, held by
`event.Builder`. Nothing random and nothing clock-based enters the derivation.

Replaying the same source with the same config and the same classifier output
must produce the same identifiers. Determinism is what makes two replays
comparable, makes a session diffable, and makes an event addressable from a
report without an index.

## Actions

An adapter reads a record's tool calls into a fixed, small verb vocabulary —
`read`, `write`, `execute`, `search`, `ask`, `unknown` — defined in
`internal/stream/action.go`. Targets are paths made relative to the session root
where the source names one; a path outside the root keeps its absolute form,
because being outside is the fact a scope rule reads.

Actions are **observed**. The adapter read them out of the source and nothing
interpreted them. They ride on `record_observed` as `action_count`,
`failed_actions`, `path_targets` and `write_paths`.

Every harness tool name lives inside that harness's adapter and nowhere else. A
rule that named one would inherit that harness's vocabulary the way the marker
rules inherit English's; `TestRulesNameNoHarnessTool` fails the build if one
does. An unrecognized tool maps to `unknown` and is counted, never guessed into
another verb: a call mapped to the wrong verb puts paths into a write set that
nothing established were written, and the write set is what pollution and scope
violation are decided from.

## Evidence classes

| Class | Qualifies | Examples |
|---|---|---|
| `observed` | Computed from the source bytes with no interpretation | bytes, characters, words, lines, quoted characters, exact repetition, timestamps, source offsets |
| `classified` | A named, versioned classifier's interpretation. Every payload carries `provenance` with `classifier`, `classifier_version`, `classifier_hash`, `source_turn`, `confidence` | segments, correction candidacy, pointer type, obligation candidates, expansion signals |
| `derived` | Calculated from observed and classified events | state transitions, regime, every metric |

## Kinds

Kind constants live in `internal/event/kinds.go`. Routing is by exact kind in
`store.FileFor`: candidate kinds share the `repair_` prefix with state
transitions, so a prefix rule would file a candidate as a state change.

| Kind | Class | Payload | File | Emitted when |
|---|---|---|---|---|
| `record_observed` | observed | `observedPayload` | `observations.jsonl` | Every record, first event of the record. Carries the record's action counts and path targets |
| `observation_stopped` | observed | `observationStoppedPayload` | `observations.jsonl` | The observer was signalled to stop. Carries only what the source established: why watching ended, the byte offset reached, and the last record read |
| `operator_interrupt` | observed | `interruptPayload` | `observations.jsonl` | The operator interrupted the agent mid-cycle. The harness authored the marker; the operator authored no text |
| `segments_classified` | classified | `segmentsPayload` | `classifications.jsonl` | An operator turn produced segments |
| `correction_candidate` | classified | `correctionPayload` | `classifications.jsonl` | The turn matched a correction marker |
| `stop_candidate` | classified | `markerPayload` | `classifications.jsonl` | The turn matched a stop marker |
| `reset_candidate` | classified | `markerPayload` | `classifications.jsonl` | The turn matched a session-reset marker |
| `pointer_candidate` | classified | `pointerPayload` | `classifications.jsonl` | The turn carried a compact reference |
| `obligation_candidate` | classified | `obligationCandidatePayload` | `classifications.jsonl` | One per sentence stating a requirement on the agent. A marker makes a sentence a candidate; the `directive` structure decides whether it is one, and a sentence read as describing never reaches the inventory |
| `near_repeat_candidate` | classified | `nearRepeatPayload` | `classifications.jsonl` | Token Jaccard ≥ 0.80 against an unresolved obligation candidate that the content-token key did not already match as a repeat |
| `repair_expansion_candidate` | classified | `expansionPayload` | `classifications.jsonl` | An agent turn carried expansion markers or a repair claim |
| `classifier_failed` | classified | `failurePayload` | `classifications.jsonl` | The classifier returned an error. A safe fallback result returned with the error is retained; otherwise the turn contributes no classified evidence |
| `semantic_classification_completed` | classified | `classify.Completion` | `classifications.jsonl` | A bounded local-model worker finished, failed, timed out or was canceled for one source record. It carries job identity, input hash, classifier provenance, latency and any validated result. It is deferred evidence: the live projector does not apply it |
| `obligation_introduced` | derived | `obligationPayload` | `obligations.jsonl` | A normalized obligation key was seen for the first time in this epoch. The candidate enters the unresolved inventory; nothing here establishes that the requirement is in force |
| `obligation_repeated` | derived | `obligationRepeatPayload` | `obligations.jsonl` | The requirement was stated again, matched on the normalized sentence or on the content-token key; `during_correction` says whether the turn was also a correction |
| `obligation_violated` | derived | `obligationPayload` | `obligations.jsonl` | A correction's identified target key is this obligation's key. A repeat inside an unrelated correction is not enough |
| `obligation_released` | derived | `obligationResolvedPayload` | `obligations.jsonl` | The operator withdrew the requirement. Needs `obligation_release` in the classifier's capabilities |
| `obligation_satisfied` | derived | `obligationResolvedPayload` | `obligations.jsonl` | The requirement was met, established by something other than the agent's account of its own work. Needs `obligation_satisfaction`; the marker tier does not have it |
| `obligation_superseded` | derived | `obligationResolvedPayload` | `obligations.jsonl` | A later requirement replaced this one. Needs `obligation_supersession`; the marker tier does not have it |
| `obligation_revived` | derived | `obligationRevivedPayload` | `obligations.jsonl` | A resolved candidate was stated again. It returns to the inventory under its original identity, and `from_status` names what it came back from |
| `obligation_expired` | derived | `obligationPayload` | `obligations.jsonl` | Declared, never emitted in this build; a reset marks obligations unknown through `epoch_advanced` |
| `repair_opened` | derived | `repairPayload` | `repairs.jsonl` | A correction opened an episode at depth 1. Either a correction marker in an operator turn, or a re-edit: the response cycle wrote a path the immediately preceding cycle also wrote. `repair.structural` says which, and the `license` states the comparison that fired |
| `repair_deepened` | derived | `repairPayload` | `repairs.jsonl` | A correction arrived whose established target key equals the open episode's. A correction merely coexisting with an open episode does not produce this |
| `repair_provisionally_closed` | derived | `repairPayload` | `repairs.jsonl` | After the agent answered, the next operator turn accepted or moved work forward |
| `repair_durably_closed` | derived | `repairPayload` | `repairs.jsonl` | The recurrence window passed without the same target returning |
| `repair_recurred` | derived | `repairPayload` | `repairs.jsonl` | A correction matched the target key of an episode awaiting durability; that episode reopens at greater depth under its own ID, and no second episode is created |
| `repair_reset` | derived | `repairPayload` | `repairs.jsonl` | A reset closed an open episode; never counted as clean recovery |
| `repair_abandoned` | derived | `repairPayload` | `repairs.jsonl` | Declared, never emitted in this build: nothing produces evidence that an operator abandoned an episode. An episode that ran out of window, or that was open at the end of the source, gets status `unknown` through `repair_status` |
| `repair_status` | derived | `pollutionPayload`, `repairStatusPayload` | `repairs.jsonl` | Two payload shapes share this kind. A pollution assessment carries `pollution_status` and covers one correction cycle: every agent and tool record up to the next operator turn. An episode status carries `status`, `reason` and the episode, and is written when target equivalence cannot be established, the durability window elapsed with no closing evidence, a later correction could not be established as belonging to the episode, an epoch ended first, or the source ended with the episode open. The two status fields are spelled differently on purpose: a reader keying on one name must not count the other |
| `pointer_resolved` | derived | `pointerOutcomePayload` | `metrics.jsonl` | A compact reference resolved to `success`, `failure` or `unknown` |
| `epoch_advanced` | derived | `epochPayload` | `metrics.jsonl` | A reset ended the epoch; lists the obligations marked unknown |
| `regime_changed` | derived | `regimePayload` | `metrics.jsonl` | The regime differs from the previous operator turn. Carries `rule`, the identifier of the comparison that fired, and `license`, that comparison in words. The same identifier is on every `metrics_computed` snapshot, so a reader states the rule that ran instead of re-deriving one from the values |
| `trend_emerged` | derived | `trendPayload` | `metrics.jsonl` | One metric crossed between quality bands and held the new band for `window.trend_durability_turns` consecutive operator turns. Carries the bands as words, the direction in cost terms, and `license`, the comparison in words. Nothing reads it back: no regime rule consults a trend |
| `state_at_observation_stop` | derived | `stateAtObservationStopPayload` | `metrics.jsonl` | The projector's inventory of state the observer left open: the open episode, unresolved references, episodes awaiting durability, the unresolved obligation inventory, and the projected epoch. Every count is an open question, never an outcome |
| `metrics_computed` | derived | `metrics.Snapshot` | `metrics.jsonl` | One per operator turn; only `trend_emerged` may follow it |

## Emission order

`internal/state/projector.go` owns the order. Per record:

1. `record_observed`
2. `classifier_failed`, if the classifier errored
3. Classified candidates, in this order: segments, correction, stop, reset,
   pointer, obligations, near repeats, expansion
4. Operator turns only, state transitions in this order:
   1. pollution settlement of the previous cycle (`repair_status`)
   2. pointer outcomes (`pointer_resolved`)
   3. durability settlement (`repair_durably_closed`, `repair_status`)
   4. obligations, resolutions first so that a turn both withdrawing a
      requirement and stating it again ends with the requirement standing
      (`obligation_released`, `obligation_satisfied`, `obligation_superseded`,
      then `obligation_introduced`, `obligation_revived`,
      `obligation_repeated`, `obligation_violated`)
   5. repair transitions (`repair_status` when a correction retires an episode
      it cannot be tied to, then `repair_opened`, `repair_deepened`,
      `repair_recurred`, `repair_provisionally_closed`, `repair_status`)
   6. reset, when the turn carried a reset marker (`repair_reset`,
      `repair_status`, `pointer_resolved`, `epoch_advanced`)
5. `regime_changed`, when the regime moved
6. `metrics_computed`
7. `trend_emerged`, for each metric whose band crossing became durable on this
   turn. Trends are read off the snapshot just recorded, so they follow it.

At a real source end, `Projector.Finish` emits the outstanding `repair_status`,
`pointer_resolved` with outcome `unknown`, and `repair_status` with status
`unknown` for an episode still open.

`semantic_classification_completed` is outside the per-record pipeline because
the local worker finishes after the source reader has continued. Its identity
includes the classifier and immutable input hash, so two semantic attempts for
one source sequence remain separate append-only facts. Strict replay can select
one; the live projector never mutates past state from its arrival.

`summary.json` and `report.md` project retained semantic completions into a
separate operational section. Their completion counts and latency percentiles
are not domain metrics and cannot change a regime, repair, obligation or
pointer outcome.

Strict replay can select a completion only when its `stream_id`, `source_seq`,
classifier identity and `input_hash` exactly match the reconstructed classifier
input. Zero matches and multiple matches are errors, rather than a fallback or
arrival-order choice.

An observer that is signalled does not call `Finish`. `Projector.StopObservation`
emits exactly two events and nothing else, because two kinds of fact are
involved:

1. `observation_stopped`, **observed** — why watching ended, the byte offset it
   reached, and the ordinal of the last record read. All three are directly
   established from the source and the observer.
2. `state_at_observation_stop`, **derived** — the projector's inventory of what
   was left open. These are counts of projected state, and filing them as
   observed would class the projection as something read out of the source.

A watcher stopping means observation stopped; it does not mean the repair was
abandoned, the reference failed, the obligations ended, or the interaction
reached EOF. The state left open stays open, and the inventory settles none of
it.

An event carrying a reset is stamped with the epoch it closes;
`epoch_advanced` carries `from` and `to`, and later events carry the new epoch.

## Transition license

Provenance reachability — source to observed to classified to derived to metric
— is necessary and not sufficient. An unlicensed transition is reachable too. So
every derived repair transition stores the premise that permitted it, in a
`license` field, and the payloads that settle something store an `evidence` or
`reason` string for the same purpose. `inspect --turn <n>` prints them under
`transition license`.

The question each premise answers is: what observed or classified facts license
*this exact* derived transition? A transition whose premise is weaker than its
conclusion is the defect the field exists to expose. In particular:

| Not evidence of | Conclusion it must not license |
|---|---|
| An unknown correction target | Target equivalence with anything |
| An episode being open | This correction belonging to that episode |
| A later correction | Failure of every outstanding compact reference |
| An unresolved obligation candidate | A requirement known to remain in force |
| The observer stopping | The source reaching EOF |
| An agent's repair claim | Verified repair of the target |
| An agent saying it met a requirement | The requirement being satisfied |
| A release marker | Which requirement was released |
| Two requirements sharing most of their words | One replacing the other |
| A count of zero under a classifier that cannot establish the fact | The fact never happening |

The field is prose for a human to read, not a flag for a program to trust. There
is deliberately no `valid` boolean: the gate lives in the tests
(`internal/state/license_test.go`) and in review.

## Append only

State transition events carry the whole projected object. The last event for an
identifier is its current state, so a reader folds the log forward and never
needs a rewrite. An obligation that turns out to have been violated produces
`obligation_violated`; the earlier `obligation_introduced` stays as it was
written.

## Session directory

Under `$XDG_DATA_HOME/flow-indicator/sessions/<stream-id>/`, falling back to
`~/.local/share`. The stream identifier is sanitized to one path element by
`store.SanitizeID`.

| File | Role |
|---|---|
| `source.json` | Source provenance: adapter, path, bytes, record count, committed offset |
| `observations.jsonl` | Append-only. Observed events |
| `classifications.jsonl` | Append-only. Classified events |
| `obligations.jsonl` | Append-only. Obligation transitions |
| `repairs.jsonl` | Append-only. Repair transitions |
| `metrics.jsonl` | Append-only. Pointer outcomes, epochs, regime changes, per-turn metrics |
| `summary.json` | Projection. Rebuilt from the event files |
| `metrics.json` | Projection. Every per-turn snapshot as one array |
| `report.md` | Projection. `render.Session.Report` |
| `timeline.csv` | Projection. One row per operator turn |

The five `.jsonl` files are the source of truth. The four projections are
disposable and are overwritten on every replay and at the end of a watch.

`replay` refuses to write into a session that already holds events; `--force`
removes the event files first. Appending a second replay of one source would
double every count.
