# Profiles

A profile is the operator-variable half of the marker classifier: the words a
person reaches for when they correct, stop, constrain, accept and refer. It is
the only part of the classified families that is meant to be tuned, and it is
layered — a baseline ships in the binary, and an operator overlay names what is
different about that operator.

`internal/profile` holds the mechanism; `internal/profile/baseline.json` holds
the shipped lexicon.

## Where an overlay lives

`$XDG_CONFIG_HOME/flow-indicator/profile.json`, beside the configuration file.
No file there means the shipped baseline, which is what most operators run.

```bash
flow-indicator replay --adapter generic session.jsonl            # baseline + default overlay
flow-indicator replay --profile team.json --adapter generic session.jsonl
flow-indicator watch --config test.json --last                   # overlay beside test.json
```

`--profile` is accepted by every command that classifies: `watch`, `replay`,
`replay-set`, `calibrate`, and it rides along on `report` and `inspect` where it
changes nothing. The overlay is resolved from the directory of the configuration
file in use, so naming a configuration file names the overlay that goes with it
and a test run cannot silently pick up the operator's own lexicon.

An overlay named with `--profile` that does not exist is an error. The default
one not existing is not.

Every tier reads the same ruleset: the marker classifier is the whole of
`heuristic` mode and the fast half of `openai-compatible` and `hybrid`, so an
overlay reaches all three. `cmd/flow-indicator/profile_test.go` holds it down by
replaying `fixtures/recent-thrash` with and without an overlay and asserting the
two rule hashes differ. Any run prints the hash in force — `calibrate` on its
`rules` line, and every stored classified event carries it — so read it there
rather than from a number written down here.

## What is a profile and what is not

| kind of rule | lives in | tunable |
|---|---|---|
| whether a sentence has a subject before its marker | `internal/classify/structure.go` | no, only on/off |
| whether the sentence sits inside a fence or block quote | same | no, only on/off |
| whether the agent re-edited a path it had just written | `internal/classify/heuristic.go` | no |
| whether a write landed outside what the correction named | same | no |
| which words mark a correction, a stop, a release, a pointer | `profile.Markers` | yes |
| the constants a rule reads | `profile.Parameters` | yes |
| whether a structural test runs at all | `profile.Discriminators` | yes |

Structural rules are universal: none of them is about a person or a project.
They stay in code and carry no dial. The lexicon is not universal — it varies by
person, team, language and domain — so it carries a version, a fingerprint and
an overlay mechanism.

`profile.Language` names what the lexicon is written in. Every marker rule in
this program is language-specific; the baseline declares `"en"`.

## Shape

```json
{
  "name": "my-team",
  "version": "1",
  "description": "how we actually talk",
  "language": "en",
  "markers": {
    "correction_tier1": {"phrases": ["nah", "back up"], "anchored": ["hold on"]},
    "release":          {"phrases": ["belay that"]}
  },
  "parameters":     {"bulk_paste_lines": 60},
  "discriminators": {"code_density": false}
}
```

`name` is required. **Unknown keys are an error**: a misspelled marker group
would otherwise be accepted and silently do nothing, which is the worst outcome
available — the operator believes they changed a rule. A marker group whose
pattern does not compile is an error at parse time, not at first match.

### Marker groups

Three ways to state one, in increasing order of power and decreasing order of
safety.

| field | matching |
|---|---|
| `phrases` | anywhere in the text, on word boundaries, case-insensitive |
| `anchored` | only at the start of the text — `"no"` opening a turn is a rejection, `"no"` mid-sentence is a word |
| `pattern` | a raw regular expression, unioned with the phrases; for the few groups that need position or character-class logic |
| `case_sensitive` | keeps the group case-sensitive; wanted only where upper case is the signal, as in identifier shapes |
| `replace` | overlay only: stand in place of the baseline group instead of adding to it |

Write `phrases`. `pattern` exists because a few baseline groups genuinely need
it, and a bad one is a rule that fires on everything.

An empty group compiles to nil and matches nothing. That is deliberate: a rule
whose marker group an operator emptied stops firing, rather than firing on
everything by accident.

## Merge semantics

`Merge(baseline, overlay)` returns a new profile; neither input is modified.

| part | rule |
|---|---|
| `markers` | union by default — the overlay's phrases are added to the baseline's, deduplicated case-insensitively, baseline order first |
| `markers` with `"replace": true` | the overlay's group stands alone |
| a group the baseline does not have | taken as written |
| `pattern` | unioned as `(?:base)\|(?:overlay)` |
| `case_sensitive` | stays on only if both layers ask for it |
| `parameters` | override per key: a number has no union |
| `discriminators` | override per key |
| `name` | becomes `baseline+<overlay name>` |
| `language` | the overlay's, if it names one |

Adding is the default because an operator naming their own way of correcting
rarely wants the common ways forgotten.

## The baseline groups

31 groups, all pattern-stated. What each feeds:

| group | read by |
|---|---|
| `correction_tier1` | a correction on its own evidence; also the `correction` segment label |
| `correction_tier2` | a correction only when the turn also repeats an unresolved obligation |
| `release` | withdrawal of a requirement — the one resolution the default tier can establish |
| `stop`, `scope`, `negative`, `positive` | obligation kinds, in that precedence |
| `reset` | a handoff or context reset; `handoff_after_failure` when a repair is open |
| `acceptance`, `continuation` | first-sentence tests that the operator is not correcting |
| `meta`, `new_task_line`, `referent`, `reconstruction`, `temporal`, `evidence`, `forward` | the remaining segment labels |
| `pointer_task`, `pointer_file`, `pointer_namespace`, `pointer_operation`, `pointer_quote`, `pointer_alias` | what a correction points at, in that order |
| `repair_verb` | the agent's *claim* to have repaired, recorded only while an episode is open and kept separate from a verified write |
| `expansion_task`, `expansion_validation`, `expansion_constraint`, `expansion_scope` | scope expansion inside a repair; `expansion_scope` is ignored when the record carried actions, because the write set already answered exactly |
| `second_person` | the structural "is this addressed to the agent" test |
| `code_span`, `path_token` | the code-density measure |

Two behaviours no overlay changes. Neither tier of correction marker counts
inside a bulk paste, and neither does `stop` or a release: a pasted document is
content, not an address to the agent. And `restated prior state` is exact
repetition of an earlier operator sentence after normalization — similarity is
not repetition, and no marker is involved.

## Parameters

Five, each declared in `classify.FreeParameters()` with what justifies its
value. `fitted` means the value rests on no demonstrated separation between two
populations; the program is trying to drive that count to zero.

| parameter | default | fitted | justification |
|---|---|---|---|
| `bulk_paste_lines` | 40 | no | typed directives run to tens of lines, pasted specifications to hundreds; no threshold inside that gap changes which turns are affected |
| `release_shared_tokens` | 2 | no | one shared token is a coincidence at these lengths: "never mind" and "never touch the release workflow" share "never" |
| `near_repeat_threshold` | 0.8 | yes | bounds a candidate kind no rule acts on |
| `release_coverage` | 0.5 | yes | set high on the argument that a release naming no candidate this clearly should resolve nothing |
| `min_obligation_chars` | 8 | yes | bounds what is long enough to carry a requirement |

Raising `bulk_paste_lines` is the one an operator plausibly wants: it is the
line count above which a turn is treated as delivered material rather than as an
address to the agent.

## Discriminators

Switches over the structural tests. Whether a test runs is an operator decision
even though how it works is not.

| discriminator | default | effect when on |
|---|---|---|
| `contained` | true | a marker sentence inside a code fence or block quote describes, so it is not an obligation |
| `subject_before_marker` | true | a sentence with a non-function word before its marker describes, unless it also addresses the agent |
| `second_person` | true | addressing the agent overrides the subject test |
| `code_density` | false | measured and reported either way; it does not participate in the decision, because a density rule needs a threshold and no labeled population justifies one |

Turning `subject_before_marker` off makes nothing describe. With every
discriminator off, every marker-carrying sentence is kept — the pre-structural
behaviour, which is what "off" should mean.

The known cost of the shipped rule: a directive stated in the third person —
"the installer must not touch /opt" — reads as a description. That is what the
labeled scoring is for.

## Provenance

Every classification carries the classifier's name, version and rule hash. The
hash is `sha256(HeuristicVersion \0 profile.Fingerprint())`, truncated to 16 hex
characters, and the fingerprint covers every pattern, parameter and
discriminator with its keys sorted. Consequences:

- two builds that disagree anywhere produce different hashes, even if nobody
  bumped the version constant;
- a session classified under an overlay is never mistaken for one classified
  under the baseline;
- a calibration score names the ruleset it scored. Scores under different
  fingerprints are not comparable, and the corpus split is fixed independently
  so an overlay cannot be tuned against the holdout by accident.

The tuning budget — the number of marker groups in force and the free parameters
with their justifications — is a property of the rules in force, not of the
source tree, so an overlay that adds groups shows up in it. Both counts are
expected to fall: fewer patterns, with structure carrying the load.

## Writing one

1. Collect turns the meter got wrong. A correction it missed is a phrase the
   lexicon does not have; a false correction is a phrase that should be narrowed
   or a structural test that should be on.
2. Add phrases, not patterns. `phrases` and `anchored` cover almost everything,
   and a regex that overmatches produces a calm session or a permanent THRASH,
   both silently.
3. Keep `replace` for the case where the baseline group is actively wrong for
   your language or domain — it discards evidence the baseline was catching.
4. Score it before trusting it. `corpus` → `label` → `calibrate`
   ([COMMANDS.md](COMMANDS.md#calibration)) is the only path that says whether
   the overlay improved anything. Run it twice against the same labels —
   `--profile` naming the overlay, then without — and compare; the two runs
   print different rule hashes, which is how the comparison stays honest.

Related: [METRICS.md](METRICS.md) for what the families do with these
classifications, [INFERENCE.md](INFERENCE.md) for the semantic tier that
reaches corrections carrying no marker at all.
