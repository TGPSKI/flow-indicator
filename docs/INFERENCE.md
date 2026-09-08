# Inference

The marker classifier reads words. A correction that carries no marker —
"you broke a lot of stuff", "we have to start from scratch" — is invisible to
it, and four sessions in the review corpus fail exactly that way. The semantic
tier is the opt-in for that class: one turn at a time, out to a model, under a
schema strict enough that a bad answer is rejected rather than folded in.

This document covers what leaves the machine, where it goes, what comes back,
and what happens when it does not.

## One protocol, two destinations

`flow-indicator` speaks the OpenAI chat-completions shape and carries no
provider SDK. Anything answering that shape works: llama.cpp, vLLM, Ollama's
compatible endpoint, LM Studio, or a hosted API.

**The endpoint is local by configuration convention, not by enforcement.**
Nothing in the program checks that the host is on the loopback interface, and
nothing stops a key and a public URL. Choosing an endpoint appropriate for the
transcript is the operator's decision, and `SECURITY.md` treats sending to any
endpoint other than the configured one as a vulnerability — not sending to the
configured one, whatever it is.

| | local endpoint | cloud endpoint |
|---|---|---|
| what leaves the machine | nothing | the turn text, up to 4 prior operator turns, the unresolved obligation inventory, the open repair reference |
| key | usually none | `FLOW_INDICATOR_API_KEY`, sent as a bearer token |
| latency measured | 0.3–0.5 s per record against a local `qwen36-35b-a3b-nvfp4` | provider-dependent, and every record is a round trip |
| suitable for `watch` | in `hybrid`, yes | the deadline is 200 ms by default; a remote round trip will mostly miss it |
| suitable for `replay` | yes | yes, at one request per eligible turn |

Session classification has no batching or fallback endpoint. A timed-out live
request is terminal; no retry consumes capacity reserved for new arrivals.

## Modes

Set `classifier.mode` in the configuration file.

| mode | live path | semantic path | sends anything |
|---|---|---|---|
| `none` | observations only, no classification | — | no |
| `heuristic` | markers, in source order | — | no |
| `openai-compatible` | markers, then the model, synchronously | strict, blocking | yes |
| `hybrid` | markers immediately, then a semantic projection update | bounded background worker | yes |
| `deferred` | markers immediately | bounded background worker retained for later replay | yes |

`heuristic` is the default and the live default.

**`openai-compatible` is strict.** Replay waits for the endpoint and projects
the validated result in source order. Use it for a finished session you want
read more closely. Measured against a local model it caught one of the two
corrections the heuristic path missed, left the healthy controls quiet, lost a
THRASH the heuristic path found, and opened a repair episode on a pasted
document. It is a different reading, not a strictly better one.

**`hybrid` is for `watch` when semantic results should update the reading.**
The marker tier projects each record immediately; eligible records also enter a
bounded worker queue. When a validated result arrives, the instrument appends
it, rebuilds the source-ordered projection from the results received so far,
and commits bounded delta parts with a `semantic_projection_updated` event.
Replay and delta serialization run in a background lane. Arrivals during a
rebuild invalidate its unpublished result; only the newest complete reading
is published. Source-only batches store changed records, not copied history.

The update selects results in source order, not completion order. Its source
events and named completions remain in the append-only log, so the live reading
can change without erasing the marker reading that preceded it.

Records already present when observation opens use markers and receive a
durable bootstrap-drop disposition. Only later arrivals enter the semantic
queues. Each harness supplies that boundary through the shared live interface;
no downstream rule distinguishes a file-backed source from a database.

**`deferred` preserves the former hybrid behavior.** It records completed,
failed and timed-out semantic work but leaves the live projection on the marker
path. Replay with retained completions to inspect that evidence later.

## What is sent

Only turns whose classification can still reach a metric:

- every operator turn;
- an agent turn **only while a repair episode is open**, because an agent
  record's semantic fields fold into an open correction cycle and nothing else;
- nothing else. Tool results and harness bookkeeping carry text that no metric
  reads, so sending them would buy a request and change no number.

A turn over `MaxTurnBytes` (32 KiB) is not sent and keeps its heuristic
classification; the log says why. Truncating would move every byte offset the
model returns.

The request body is the versioned prompt as the system message and this object
as the user message:

```json
{"turn": {"seq": 411, "speaker": "human", "text": "…", "byte_length": 3},
 "prior_turns": ["…", "…"],
 "unresolved_obligation_candidates": [{"key": "…", "kind": "scope", "text": "…"}],
 "active_repair": {"…": "…"}}
```

`prior_turns` is at most the last 4 operator turns — enough for referent and
restatement questions, not the whole rolling window. `temperature` is 0. No
mutable projector state is sent: no regime, no metric, no threshold. Requests,
prompts, API keys and raw model replies are never logged.

## What must come back

One JSON object, nothing after it, matching the schema in
`internal/classify/openai.go`: segments with byte offsets and labels, a pointer,
a correction, obligations, resolutions, repair fields, a confidence.

Validation rejects, rather than repairs:

| rejected | why |
|---|---|
| a second JSON value after the first | no event could name which interpretation was applied |
| unknown fields | the schema is the contract |
| segment offsets outside the turn, overlapping, out of order, or splitting a UTF-8 character | offsets decide every bucket share; bad ones inflate a numerator against an unchanged denominator |
| an unknown segment label, pointer type, obligation kind or resolution kind | the vocabulary is closed |
| confidence outside [0,1], or NaN | it is recorded as evidence and must be a number |
| a resolution naming a key the model was not shown, or stating no evidence | a resolution removes a requirement from the inventory, so it is held to the strictest check here |
| negative expansion counts | a count |
| a new obligation with an invented key or text absent from the turn | new identity must be reproducible from a source span |
| a nonempty obligation key absent from the outstanding candidates | a supplied key asserts a semantic repeat, never a new identity |

A rejected reply is a recorded failure and the turn keeps its marker
classification. **Malformed output is never repaired with a second call.**

Prompt version 5 removes obligation display IDs from context. Candidate and
repair fields have lowercase JSON names. New obligations return `key:""` and
exact source text; semantic repeats copy one outstanding `key`. Resolutions
copy that same field and state evidence. Correction targets and pointer types
share the closed vocabulary `node`, `alias`, `file`, `path`, `task`, `temporal`,
`quote`, `namespace`, `relation`, `operation`, `unknown`.

Offsets refer to decoded UTF-8 bytes, including trailing newlines. They must
fit `turn.byte_length` and land on character boundaries. JSON escape spelling
is not part of the turn's byte length.

Text the model left uncovered is tiled as `other` rather than dropped;
otherwise the denominator would shrink whenever the model skipped a sentence,
raising every share it did label.

Two answers are kept side by side rather than merged: the marker tier's record
that the agent *claimed* a repair, and the model's judgement of whether the
target was *verified* repaired. Marker-tier releases are kept only when the
model returned no resolutions — both tiers answer the same question, and the
model saw the whole inventory, so merging would apply one resolution twice under
two premises.

## Capabilities

Which resolutions a build can establish is reported, not assumed. The marker
tier establishes operator release and verified repair. The semantic tier is
asked for supersession and satisfaction as well. A build without one says so
rather than reporting a zero that was never measured.

An agent stating it did the thing is a claim, never `satisfied`.

## Configuration

```json
{"classifier": {
  "mode": "hybrid",
  "endpoint": "http://127.0.0.1:8000/v1/chat/completions",
  "model": "your-local-model",
  "live_deadline_ms": 200,
  "workers": 1,
  "max_queue": 32,
  "disable_thinking": false
}}
```

| key | default | meaning |
|---|---|---|
| `mode` | `heuristic` | `none`, `heuristic`, `openai-compatible`, `hybrid`, or `deferred` |
| `endpoint` | `""` | required by `openai-compatible`, `hybrid` and `deferred` |
| `model` | `""` | required by the same three |
| `live_deadline_ms` | 200 | per-job deadline in `hybrid` and `deferred`; a job past it is recorded `timed_out` |
| `workers` | 1 | concurrent semantic jobs in `hybrid` and `deferred`; minimum 1 |
| `max_queue` | 32 | queue depth in `hybrid` and `deferred`; minimum 1 |
| `disable_thinking` | false | send vLLM's `chat_template_kwargs.enable_thinking=false`; use when a reasoning model otherwise returns no `message.content` |
| `constrained_json` | false | request strict `response_format.json_schema` decoding with the turn's offset bound; enable only on an endpoint supporting that protocol |

The key is read from `FLOW_INDICATOR_API_KEY` and is never a flag. It is used
only as a bearer token on the configured endpoint, and never reaches a session
file, a log line or an error message.

An invalid value is an error. There is no silent fallback: `openai-compatible`
with no endpoint fails at startup rather than quietly classifying with markers.

## Cost and load control

The bounds exist so that a slow or absent model degrades the reading and never
the observation.

- **Both queues are bounded.** `max_queue` bounds the active queue and a
  catch-up queue of the same size. Overflow enters catch-up without delaying
  transcript observation. A job is lost only when both queues are full.
- **A timeout is terminal.** No automatic retry competes with new arrivals.
- **Every job has a deadline** in `hybrid` and `deferred`, and every request has the HTTP
  client's 60 s ceiling in both semantic modes.
- **Shutdown cancels in flight work.** Jobs still queued when `watch` ends are
  counted as canceled, not pending.
- **At most one request per eligible turn.**
  `flow-indicator corpus` prints turn counts before you point a metered endpoint
  at a corpus.

The default live view reports `validated/eligible` and a changed count when it
fits. The full view also shows failures, timeouts and drops.
`watch --model-details` adds active and catch-up work, latency and the latest
error. These measure the instrument, not the interaction:
**no regime rule reads them**, and they never enter a metric family.

## Coverage

These counts cover eligible operator records and agent records inside an open
repair cycle. Each disposition carries `operator_turn` for operator-only audits.

| counter | definition |
|---|---|
| `eligible` | records the shared semantic eligibility rule admits, including bootstrap, empty and oversized turns |
| `requested` | eligible records whose worker started a classifier request |
| `validated` | terminal completions whose result passed classifier validation; `completed` is the stored status and legacy counter name |
| `failed` | terminal classifier errors other than timeout or cancellation, including invalid responses |
| `timed_out` | terminal request deadline expiry |
| `canceled` | eligible queued or active records canceled at observer shutdown |
| `dropped` | eligible records never requested because they were historical, empty, oversized or both queues were full |
| `pending` | admitted records without a terminal outcome, including catch-up and active work |
| `applied` | validated results selected by a committed hybrid projection |
| `changed` | applied results whose classification fields differ from their original marker result, excluding confidence and provenance |

At an observation boundary, `eligible = validated + failed + timed_out +
canceled + dropped + pending`. Requested, applied and changed are overlapping
subsets, not extra outcomes. Queue admission, request start, drops and terminal
outcomes are append-only facts; a report after shutdown retains these counts.
Changed counts classifications, not rebuilds, metric movements or regime changes.

## Failure behaviour

Malformed output, an HTTP failure, a non-200 status, an empty choices array, a
timeout or a cancellation all record a semantic failure. In every case:

- the marker result stays in force for that turn;
- `unknown` is never changed to zero;
- a model response never directly selects a regime, threshold or metric.

The model supplies fields; the deterministic core decides what they mean.

## Provenance

Every completion names its source sequence and byte offset, the classifier's
name, version and hash, the response status, the input hash and the latency.

The classifier hash is `sha256(prompt version, endpoint, model, thinking mode,
constrained-decoding mode, prompt/schema hash)`
truncated to 16 hex characters. **The endpoint is part of the identity on
purpose**: two local runtimes can expose the same model name with different
weights or chat templates, and merging those outputs silently would make a
score meaningless. Change the endpoint and you have a different classifier.

`PromptVersion` versions the one prompt and the one context schema. There is one
of each, because prompt variation would make stored classifications
incomparable.

Model-reported confidence is not a calibrated probability. It is recorded as
what the model said about itself.

## Reproducibility

The deterministic core takes classifier output as input, so a session's semantic
reading can be projected again without calling any model:

```bash
flow-indicator replay --stream-id replayed-session \
  --semantic-session ~/.local/share/flow-indicator/sessions/live-session \
  source.jsonl
```

Replay selects exactly one `completed` result whose stream, source sequence and
input hash match the classifier input reconstructed in source order. Missing or
duplicate matches are refused **before** the output session is created. The
configured endpoint, model and prompt identity must match the retained
completion identity, and a semantic source session can never be the output
session — pass `--stream-id`, so the append-only evidence survives.

The session report grows a **semantic operations** section when deferred
completions exist: retained completions by outcome, latency percentiles, and
classifier identities. It is not a metric family. Dispositions preserve drops
and cancellations as well as completions. Active/catch-up queue separation and
the latest latency remain operational details.

## Calibration

Score the marker baseline and the semantic path as separate runs. Each report
names the classifier version and hash that produced it.

```bash
flow-indicator calibrate --labels labels/ --manifest corpus/manifest.json \
  --split dev --classifier heuristic
flow-indicator calibrate --config local-model.json --labels labels/ \
  --manifest corpus/manifest.json --split dev --classifier configured
```

Under `classifier.mode: "hybrid"` or `"deferred"`, configured calibration uses the strict
semantic classifier synchronously: the worker lane is a live-latency mechanism,
not a fourth interpretation of the corpus. Compare its retained completion
coverage and operational report separately from accuracy scores.

`annotate` is the other model-backed command and a different job: it fills in
label candidates for a corpus rather than classifying a session. It takes its
own `--endpoint` and `--model`, runs two passes per family under differing
instructions, and names the model in every file it writes. It sends up to 25
units per request. A failed batch is retried once; a reply truncated by the token
limit is split recursively, down to one unit. A model judgement is a candidate
an adjudicator accepts or rejects, never ground truth. See
[COMMANDS.md](COMMANDS.md#annotate).

## Choosing

| situation | mode |
|---|---|
| live pane, no model running | `heuristic` |
| live pane, local model updates wanted | `hybrid` |
| live pane, retain semantic results for later replay | `deferred` |
| finished session you want read closely | `openai-compatible` |
| transcripts you would not send anywhere | `heuristic` or `none` |
| scoring a rule change | `heuristic` and `configured` as two runs |

Do not point a remote endpoint at a transcript you would not send to that
endpoint. The default sends nothing.

Related: [PROFILES.md](PROFILES.md) for the marker lexicon both semantic modes
still run underneath, [INTEGRATIONS.md](INTEGRATIONS.md) for every other process
boundary, [METRICS.md](METRICS.md) for what the families do with a
classification.
