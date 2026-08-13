# Calibration

Every number this program prints is a claim about an operator's session. This
document is how those claims are tested, what the testing has found so far, and
which findings are still unmeasured.

The commands themselves are in [COMMANDS.md](COMMANDS.md#calibration). This is
the process around them.

## Why it exists

A rule that moves a metric in the predicted direction is not a rule that is
right. Both look identical from inside the program: the rule fires, the number
changes, the change matches the story that motivated it. The only thing that
separates them is a judgement made without the rule in view.

So the loop is built to make the rules falsifiable, not to make them look good:

- Labels are collected before the score, from units that carry no verdict. A
  labeler shown what the build decided agrees with the build, and the run then
  measures agreement rather than the rule.
- Every score names the artifact, corpus, ruleset and label set that produced
  it, by hash. A score that cannot name those is not evidence.
- The corpus is split by session and half of it is sealed. A rule tuned until
  the dev numbers improve has learned the dev corpus.

## The loop

```bash
flow-indicator corpus   --harness claude-code --scan ~/.claude/projects \
                        --build --out corpus/           # sample and split, once
flow-indicator label    --manifest corpus/manifest.json --family obligation \
                        --sample-per-mille 150 > units.jsonl
flow-indicator annotate --manifest corpus/manifest.json --out labels-model/ \
                        --family obligation --model <local-model>
                                                        # two passes, candidates only
flow-indicator calibrate --labels labels/ --manifest corpus/manifest.json
```

1. **Seal a corpus.** `corpus` samples sessions, assigns each to dev or holdout,
   and records each file's hash. The hash is checked on every later run: a
   corpus that changed underneath a score makes the score a number about
   nothing.
2. **Emit units.** A unit is one sentence, turn or cycle, depending on the
   family, with the context needed to judge it and no verdict attached.
3. **Judge them.** `annotate` runs a model over the units twice, in different
   orders and under differently worded instructions carrying the same rules.
   Both passes are written; neither is ground truth.
4. **Take the consensus.** Units the two passes put in the same class are where
   the codebook decided. Units they split on are where it did not, and those are
   a finding about the instructions, not noise to average away.
5. **Score.** `calibrate` aligns predictions to labels by record identifier and
   byte span — never by segment index, because segmentation is one of the things
   under test — and prints each score with the operator-facing consequence
   beside it.
6. **Change one thing, rescore.** Every phrase, parameter or rule change is
   scored alone before it is kept.

## The split

Session-level, never turn-level: turns inside a session share vocabulary and
subject matter, and a turn-level split leaks both across the boundary.

The holdout is sealed. `calibrate --split holdout` requires `--reason` and the
reason is recorded. Treat the count of openings as a budget that does not
refill; every opening after a change made in response to the last one is
measuring the same corpus twice.

Labeling the holdout does not open it. Scoring against it does. The sealed half
of the corpus here has been labeled and still has not been scored: its two
passes agreed at κ 0.50, under the 0.75 the dev labels reached, so the labels
are not yet strong enough to spend an opening on.

## What a label is

A candidate. Model output carries the model's name and the pass it came from,
and no path in this program promotes it to ground truth. What two passes of one
model agree on is evidence that the codebook decides a case; it is not evidence
that the answer is correct. Cohen's κ between the passes is printed with the
score for that reason.

A pass that assigns one class to every unit is called out where it happens. Its
κ against anything is zero or undefined, and it is a defect in the instructions
rather than a finding about the corpus.

## The obligation family's classes

| class | the sentence | scored as |
|---|---|---|
| `directive` | places a standing constraint: something a later turn could still break | the positive class |
| `task` | asks for a piece of work; doing it discharges it | negative, counted separately |
| `description` | asks for nothing — reports, explains, quotes, questions | negative |
| `unlabelable` | the class cannot be read: truncated mid-clause, a language nobody here reads, a reference the record does not carry | excluded from the score |

`task` is split out from `description` because the two are different findings.
The family staying quiet on a description is the rule working. The family
staying quiet on a task is the family declining to look, and that is where
almost all of the unmatched operator sentences sit. Keeping them in one class
hid the distinction inside a single recall number.

`unlabelable` means the evidence is absent, not that the call is hard and not
that the text is not a sentence. A verb-less fragment constrains nothing, which
is `description` — the answer is visible. Both errors are expensive: a pass that
reaches for `unlabelable` under difficulty disagrees with a pass that does not,
and one that reaches for it on pasted output removes that unit from scoring
along with any rule that fired on it.

## What is tunable

| layer | example | tunable |
|---|---|---|
| structural rules | subject before marker, span inside a fence, write outside the named scope | no, only on and off |
| lexicon | which words mark a correction, a stop, a scope, an acceptance | yes, through a profile |
| parameters | repeat-similarity threshold, window sizes | yes, declared with what justifies each value |

The lexicon is the dial that calibration is meant to turn. Structural rules are
not about a person or a project, and tuning them per corpus is how a classifier
learns a domain's vocabulary instead of the speech act.
[PROFILES.md](PROFILES.md) has the mechanism.

## Learnings

Measured against a private 31-session corpus, 17 dev and 14 holdout. The label
files and run records hold operator transcripts and are not distributed.

**Three of the four families cannot be scored yet.** Two-pass κ, over labeling
runs of successive codebooks:

| family | old codebook | current codebook | units |
|---|---|---|---|
| `obligation`, dev | 0.75 on decidable units | **0.75 over every unit** | 1,460 |
| `obligation`, sealed half | — | 0.76 on decidable units | 693 |
| `recovery` | 0.16 | 0.52 | 108 |
| `obligation_pair` | 0.01 | 0.08 | 108 |
| `pollution` | −0.14 | **0.17** | 108 |

Only `obligation` clears the bar. The failures are not rule problems: pair asks
a question its unit's evidence cannot answer, and recovery's boundary is
genuinely ambiguous at the rate it occurs. Redefine the unit before touching the
rules — a rule tuned against labels that disagree with themselves is fitted to
noise.

**Pollution's κ moved from below chance to positive by changing what the unit
shows, with no rule touched.** The unit displayed an operator turn and the cycle
*before* it, while the question asked what the agent did about the *previous*
turn. Question and evidence were about different turns, and two passes cannot
agree on an answer neither can see. Anchoring the unit on the turn whose
response is being judged — the following cycle's writes, failed calls, words,
and the operator's reaction — took it from −0.14 to 0.17 and removed every
unlabelable. Still too weak to score against, no longer measuring nothing.

**κ is unstable at small unit counts, so treat one measurement as one sample.**
The second run drew a different sample under the same codebook and moved
`recovery` from 0.16 to 0.61 on 105 units. Nothing about the instructions
changed. Only `pollution`, which reproduced at −0.14 and −0.15, is settled; the
rest need the same unit count before their numbers are compared.

**Report κ on decidable units and say so.** Counting the units either pass
called unlabelable drags κ down by a different amount per corpus — 0.44 against
0.75 on the same dev files — because the passes disagree about what is
decidable, not about the class. Both numbers are legitimate; mixing them across
runs is not. The way out of needing the distinction is to leave almost nothing
unlabelable, which is a codebook question.

**`unlabelable` must mean the class cannot be read, never "this is not a
sentence."** An instruction that sent verb-less fragments — pasted `ps` output,
separator lines, lone paths — to `unlabelable` sent 61% of dev units there, and
89% of a paste-heavy corpus. Two costs, one of them hidden:

- An unlabelable unit is dropped from scoring *before* any true or false
  positive is counted, so a rule firing on pasted output becomes invisible. 115
  of those 822 dev units carried a marker; four false positives were being
  swallowed.
- It buys nothing. Precision and recall contain no true negatives, so labeling
  those units `description` — which is what they are, since they constrain
  nothing — left the dev score identical at 0.925 and 0.327 while true negatives
  went from 267 to 1,176.

The rewrite was mechanical enough to apply to labels already collected: every
one of those units carried a note saying "no verb" or "fragment", so they were
remapped rather than relabeled, and κ over *every* dev unit came out at 0.749.

**A corpus can be disqualified by how its sessions were chosen.** A
second-harness corpus picked by operator-turn count selects the operators who
paste the most: 89% of its units were terminal output, leaving 8 directives in
1,037 units. Rank candidate sessions by typed prose — words, function words,
a letters-to-characters ratio — not by turn count.

**Precision was easy, recall was not.** The shipped baseline scored obligation
precision 0.885 at recall 0.167. Adding operator lexicon in two rounds reached
0.910 at 0.282 with false positives flat. The remaining misses are mostly plain
imperatives — "add a column header row" — which the marker layer matches but the
obligation family never probes, because that family measures a standing
requirement rather than a task instruction. No phrase list closes that. It is a
unit-definition question, and it is open.

**Score each phrase alone, and watch precision, not recall.** Of 124 candidate
phrases scored one at a time, most matched nothing in the labeled
corpus, several bought one true positive each and were dropped as not worth the
budget, and a handful bought recall while costing precision — one common word
took precision from 0.902 to 0.834 for four extra true positives. Marginal
scoring is what makes that visible; a batch of phrases added together hides
which one did the damage.

**Mining corpora runs out before recall does.** Yield falls sharply with corpus
size and rises with how close the corpus is to real agent steering. Harness
captures were about 3% of the turns available and produced most of the kept
phrases; two large consumer-chat exports, 54,000 operator turns between them,
produced two. Once every corpus on hand has been read, more phrases is no longer
the lever.

**Filter machine-generated turns before mining or labeling.** One capture's
"operator" side was 70% status blocks posted by a supervising program — a fleet
conductor writing into the agent's user channel. Mining those fits the lexicon
to a program's output; labeling them measures the codebook against text no
person wrote. They are detectable by their templates and should be dropped at
the session level.

**A profile overlay changes what the meter shows, not just what calibration
prints.** Adding one common phrase moved a fixture's control-burden reading from
1% to 3%. That is the overlay working as designed — more of the operator's
steering text is recognized — but it means a summary is only comparable to
another summary produced under the same rule hash.

## Failure modes

- Scoring against labels the rules under test produced, directly or by showing
  the labeler the build's verdict.
- Tuning until the dev split improves, then reporting the dev split.
- Editing the artifact after a long run started. The run then proved bytes that
  are not the bytes shipping; hash the artifact into the record so the record
  names its subject.
- Reporting a score without its rule hash. Scores under different rulesets are
  not comparable, and a report that does not name its hash cannot say which
  lexicon produced it.
- Treating a κ near zero as a weak signal. It means the instructions did not
  decide the case, and nothing downstream of it is interpretable.
