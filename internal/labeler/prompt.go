package labeler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TGPSKI/flow-indicator/internal/calibrate"
	"github.com/TGPSKI/flow-indicator/internal/labels"
)

// The instructions below are the codebook, stated for a model.
//
// They describe the three families in general terms — speech acts and actions —
// and name no project, no person and no vocabulary belonging to one operator.
// That is deliberate. What is being characterized is what a directive is, what a
// correction is, and what a polluted repair is; a rule shaped around one
// operator's habits would score well here and transfer nowhere.
//
// Each family has two framings carrying the same rules in different words. The
// two passes disagree exactly where the rules do not decide, which is what makes
// their agreement evidence about the codebook.

const obligationRules = `A STANDING CONSTRAINT is a requirement that outlives the action it accompanies:
after the agent has done the immediate work, the constraint is still in force and
a later turn could violate it.

directive   = the sentence places a standing constraint on the agent
task        = it asks for a piece of work and nothing outlives the doing of it
description = it neither constrains nor asks: it reports, explains, or asks a
              question
unlabelable = there is no readable sentence to judge

Decide the speech act, not the vocabulary. Words like "never", "must", "always"
and "only" appear in both classes and settle nothing on their own.

A fragment with no verb — a bare path, an identifier, a heading, a column of
pasted output — constrains nothing, so it is description. It is not
unlabelable: you can see it places no requirement.

unlabelable is for a sentence whose class cannot be read: cut off mid-clause, in
a language you do not read, or pointing at something the record does not carry.
Difficulty is not undecidability; a hard call still gets a class.

These are task, and they are the common mistakes:
- A one-shot request. "add the retry loop", "write the installer", "also add the
  makefile support". Doing it discharges it; nothing survives to be violated.
- A request carrying a detail that only shapes this piece of work: "put the
  header row in bold", "call it config.yaml". The detail dies with the task.

These are description:
- A statement about how some system behaves. "the runner never retries",
  "it does not verify the checksum". The subject is a program, not the agent.
- Material the operator delivered rather than said: quoted text, a pasted
  specification, anything inside a code fence or a block quote. The operator is
  showing it, not requiring it.
- A question.

Long turns carry both kinds, and the line count is a hint, not an answer. Ask
who the sentence is addressed to:

- A rule for the agent reading it, however long the document: "the source files
  supplied by the operator must never be modified", "do not sign". Directive.
- A document about other people or other teams, reported rather than imposed:
  "engineers must prepare their codebase", "Wadhwani reminded the team that
  experimentation is encouraged", "keep solutions simple". Description — the
  operator pasted a meeting note or a policy page. They are showing you what it
  says, not telling you to obey it.

Named third parties, past-tense reporting and audiences other than the agent are
the tell.

These are directive:
- A prohibition or a permission that stands until lifted: "never touch the
  release workflow", "only edit files under internal/".
- A property the work must keep having: "make sure the config survives a
  reinstall", "keep the public API unchanged".
- A process rule: "run the tests before every commit", "ask before deleting".
- A constraint stated in the third person about something the agent is building:
  "the installer must not touch /opt". The subject is a program, but the
  requirement is on the agent's work, so this is directive.

A sentence can request and constrain at once — "put it in the user directory and
make sure I don't lose my config". If any clause states a standing constraint,
the sentence is directive, not task.

For a directive also give kind: stop, scope, negative, or positive.
  stop     = halt, wait, do nothing further
  scope    = bounds what may be touched
  negative = must not do something
  positive = must do or preserve something`

const obligationFraming2 = `You are deciding, for each sentence, whether it adds a rule to a list of rules the
agent must keep obeying for the rest of the session.

Ask two questions in order. Is the agent being given something to do? Is there
still something here it could break after that work is finished?

directive   = adds a standing rule, whether or not it also asks for work
task        = asks for work, adds no standing rule
description = asks for nothing: it reports, explains, quotes, or questions
unlabelable = there is nothing here that can be read as a sentence

Pasted material — a lone file path, a heading, an identifier, a line of terminal
output — adds no rule, so it is description. Do not call it unlabelable: the
answer is visible.

Reach for unlabelable only when the sentence itself defeats reading: truncated
mid-clause, a language you do not read, or a reference the record does not
carry. A close call is still a call.

These ask for work and leave no rule behind — task:
- "build X", "fix Y", "also do Z" — finished when done
- a detail that shapes only this piece of work: "name it config.yaml"

These ask for nothing — description:
- describing what some program does or fails to do
- quoted, pasted or fenced material the operator is showing rather than asserting
- questions

A long turn can be a specification the operator wrote for you or a document they
pasted about something else. The line count does not decide it; the addressee
does. "Do not modify the source files" inside a 900-line spec is a rule you must
keep. "The team must prepare for agentic development" inside pasted meeting
notes is a report about other people — description. Watch for named third
parties and past-tense reporting.

Things that add a standing rule:
- prohibitions and permissions that hold until withdrawn
- properties the work must continue to have
- process rules about how the agent should operate
- a requirement about a program the agent is building, even in the third person

Modal words are not the test. "never", "must" and "only" occur in all three
classes. If a sentence both asks for work and imposes a lasting rule, it is
directive.

For a directive also give kind: stop, scope, negative, or positive.`

const recoveryRules = `You are judging whether an operator's turn is them CORRECTING work the agent has
already done.

opens     = this turn corrects the preceding cycle, and no correction was already running
deepens   = a correction was already running and this turn pushes on the same thing
unrelated = the turn moves work forward, accepts, asks a question, or starts something else
unlabelable = the record does not establish whether it corrects

You are given what the agent did before the turn: the files it wrote in the
preceding cycle, the files it wrote in the cycle before that, whether any tool
call failed, and an excerpt of what it said.

A correction does not need correction words. "put it in the user directory
instead of /opt" carries none and is plainly one: the agent had just written an
installer targeting /opt and the operator is redirecting it. Judge the act.

Strong evidence of a correction:
- the turn rejects, redirects, or narrows what the previous cycle produced
- it names something the previous cycle wrote and asks for it to be different
- the operator interrupted the cycle

Not a correction:
- "continue", "go ahead", "keep going" — permission to proceed, saying nothing
  about whether the previous work was right
- praise or acceptance
- a new, unrelated request
- a question about the work

A turn that accepts one thing and corrects another is opens: the correction is
the part that cost the operator something.

THE TEST, and apply it before anything else:

  Suppose the operator had said nothing at all after that cycle. Would the work
  the agent produced have stood as acceptable?

  If yes — it was fine as far as it went, and this turn asks for MORE — the turn
  is unrelated. Building further on good work is not repair.

  If no — what was produced was wrong, aimed at the wrong target, missed a
  requirement it had been given, or has to be redone — the turn is opens.

Adding the next feature is unrelated. Undoing or redirecting the last one is
opens. A turn that says "now also do X" is unrelated even when X is large; a
turn that says "that should have been Y" is opens even when Y is one word.
`

const recoveryFraming2 = `Every operator turn either responds to what the agent just produced, or moves on
from it. Decide which, for each turn.

opens     = the turn responds to the previous cycle by asking for it to be different
deepens   = the turn is still working on something an earlier turn already objected to
unrelated = the turn moves on: new work, a question, acceptance, or permission to continue
unlabelable = the evidence does not settle it

The evidence you are given is what the agent did in the cycle before the turn:
paths written, paths written in the cycle before that, failed calls, and an
excerpt of what it said.

Turns that ask for it to be different, all of which are opens:
- rejecting or undoing what was produced
- redirecting it somewhere else, or to a different shape
- narrowing it, or adding a constraint it failed to respect
- restating a requirement the cycle did not meet
- reporting that the result is wrong, broken, or not what was wanted

Turns that move on, all of which are unrelated:
- "continue", "go ahead", "keep going" — permission, and no verdict on the work
- praise, thanks, or acceptance
- a fresh request on a different subject
- a question about how something works

The wording carries no weight here. An operator who writes "put it under the
user directory instead" has corrected the agent as surely as one who writes
"no, that's wrong" — the first names no fault and is still a correction. Judge
what the turn asks for against what the cycle produced.

Where a turn does both — accepts one thing and objects to another — answer opens.

THE TEST, and apply it before anything else:

  Suppose the operator had said nothing at all after that cycle. Would the work
  the agent produced have stood as acceptable?

  If yes — it was fine as far as it went, and this turn asks for MORE — the turn
  is unrelated. Building further on good work is not repair.

  If no — what was produced was wrong, aimed at the wrong target, missed a
  requirement it had been given, or has to be redone — the turn is opens.

Adding the next feature is unrelated. Undoing or redirecting the last one is
opens. A turn that says "now also do X" is unrelated even when X is large; a
turn that says "that should have been Y" is opens even when Y is one word.
`

const pollutionRules = `You are judging what the agent's response to an operator correction actually did.

The turn shown is the operator's, and the evidence is what the agent did NEXT:
the paths it wrote in response to this turn, failed calls, what it said, and
what the operator said afterwards. The question and the evidence are about the
same turn.

clean       = the response fixed what the operator asked about, and did nothing else
polluted    = it fixed what was asked, and also did work nobody asked for
failed      = it did not fix what the operator asked about
unknown     = the record does not establish whether the target was fixed
unlabelable = the unit cannot be judged at all

Rules that decide most cases:
- If the turn shown is not a correction — it asks for new work, accepts, or asks
  a question — there is nothing to have been polluted: answer unknown.
- If the correction named no particular target, answer unknown. Do not treat
  whatever the agent happened to touch as the target.
- The operator's next turn is evidence about the response, not about itself. A
  repeat of the same complaint says the fix did not land.
- The agent saying it fixed something is a claim, not evidence. Judge what it
  did. Files written are evidence; "I reverted it" is not.
- Extra work means paths outside what the correction was about, or new tasks,
  tests, or rules the operator did not ask for.`

const pollutionFraming2 = `Assess one response cycle for scope creep.

You see an operator turn, then what the agent did about it: paths written,
failed calls, an excerpt of its words, and whatever the operator said next.

First ask whether the turn shown is a correction at all. If it is not, the
answer is unknown — there was no target, so nothing could be polluted.

If it was a correction, ask two questions in order:
1. Did the agent fix the thing that was complained about? If not: failed. If the
   record does not say: unknown.
2. Did it also do things nobody asked for — other files, extra tests, new rules,
   follow-up tasks? If yes: polluted. If no: clean.

Never take the agent's word for question 1. What it wrote is evidence; what it
claimed is not. If the correction pointed at nothing specific, the answer is
unknown rather than a guess.`

const pairRules = `You are judging what an operator's turn does to requirements they stated earlier
in the same session.

repeat      = it states a requirement they already gave, in any wording
supersedes  = it replaces an earlier requirement with a different one on the same dimension
releases    = it withdraws an earlier requirement
unrelated   = it does none of these
unlabelable = the record does not establish which

A repeat does not need the same words. "do not touch the release workflow" and
"never touch release workflow" are one requirement stated twice.

supersedes needs the two things to be alternatives on one dimension: "only edit
module A" then "only edit module B" replaces. "do not touch A" then "do not touch
B" does not — both still stand. If you cannot tell a replacement from a second
independent requirement, answer unlabelable.

You are shown the operator's previous turn as context. You are not shown the
whole session, so judge only what that context supports; when it does not
support any of the three, the answer is unrelated.`

const pairFraming2 = `Decide whether this turn re-treads ground the operator already covered.

repeat      = the operator is saying a requirement over again, however worded
supersedes  = they are swapping one requirement for another of the same kind
releases    = they are calling a requirement off
unrelated   = none of these
unlabelable = cannot tell from what is shown

Rewording counts as repeating: the question is whether the same demand is being
made twice, not whether the same words appear twice.

Swapping requires the two to be alternatives on a single axis. Two separate
prohibitions are not a swap; both remain in force. When a case could be either a
swap or a second independent requirement, answer unlabelable.

Only the previous turn is given as context. If it does not support one of the
three, answer unrelated.`

// rulesFor returns the instruction body for a family and framing.
func rulesFor(family string, framing int) (string, error) {
	pick := func(a, b string) string {
		if framing == 0 {
			return a
		}
		return b
	}
	switch family {
	case labels.FamilyObligation:
		return pick(obligationRules, obligationFraming2), nil
	case labels.FamilyRecovery:
		return pick(recoveryRules, recoveryFraming2), nil
	case labels.FamilyPollution:
		return pick(pollutionRules, pollutionFraming2), nil
	case labels.FamilyObligationPair:
		return pick(pairRules, pairFraming2), nil
	default:
		return "", fmt.Errorf("labeler: no instructions for family %q", family)
	}
}

// systemFor is the role instruction. It states the output contract and the one
// rule that matters more than any classification: an undecidable case is
// reported as undecidable.
func systemFor(_ string, _ Pass) string {
	return "You are annotating a corpus of operator/agent coding transcripts for a measurement tool. " +
		"You classify; you do not summarize, advise, or comment on the work being done. " +
		"Reply with strict JSON only, no prose and no code fence. " +
		"When the record does not contain the evidence the question needs, say so with 'unlabelable' rather than " +
		"choosing the likeliest class — a wrong label is worse than a missing one, because a missing one is visible. " +
		"Undecidable means the evidence is absent, not that the call is close; a case you find difficult still gets a class."
}

// pastedTurnLines is where a turn stops being typed and starts being pasted.
// It matches the classifier's bulk_paste_lines default, so the labeler and the
// rule are reading the same fact about the same turn.
const pastedTurnLines = 40

// renderPrompt builds the user message for one batch.
func renderPrompt(family string, pass Pass, units []calibrate.Unit) (string, error) {
	rules, err := rulesFor(family, pass.Framing)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(rules)
	b.WriteString("\n\nReturn exactly this shape, one entry per numbered item, in order:\n")
	b.WriteString(`{"labels":[{"i":1,"label":"...","kind":"...","why":"a short reason, under 12 words"}]}`)
	b.WriteString("\n\nOmit \"kind\" unless the family asks for it. Every item must appear exactly once.\n\n")
	fmt.Fprintf(&b, "There are %d items.\n\n", len(units))

	for i, u := range units {
		fmt.Fprintf(&b, "--- %d ---\n", i+1)
		// Pollution reads forward, so its evidence follows the turn it is about.
		// Every other family reads back, and its evidence comes first.
		if u.Context != nil && family != labels.FamilyObligation && family != labels.FamilyPollution {
			writeContext(&b, family, u.Context)
		}
		// The same fact the classifier reads as bulk paste. Without it a
		// labeler sees one imperative sentence and no sign that the operator
		// pasted a policy document around it.
		if u.TurnLines > pastedTurnLines {
			fmt.Fprintf(&b, "THIS SENTENCE COMES FROM A PASTED BLOCK OF %d LINES\n", u.TurnLines)
		}
		fmt.Fprintf(&b, "OPERATOR TURN: %s\n", strings.TrimSpace(u.Text))
		if u.Context != nil && family == labels.FamilyPollution {
			writeContext(&b, family, u.Context)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// writeResponseContext renders what the agent did about the turn just shown.
// It is the pollution family's whole evidence, and it looks forward: the cycle
// that answered this turn, and the operator's reaction to that cycle.
func writeResponseContext(b *strings.Builder, c *calibrate.TurnContext) {
	if len(c.ResponseWrites) > 0 {
		fmt.Fprintf(b, "AGENT THEN WROTE: %s\n", strings.Join(c.ResponseWrites, ", "))
	} else {
		b.WriteString("AGENT WROTE NOTHING IN RESPONSE\n")
	}
	if c.ResponseFailedCalls > 0 {
		fmt.Fprintf(b, "TOOL CALLS THAT FAILED: %d\n", c.ResponseFailedCalls)
	}
	if c.ResponseInterrupted {
		b.WriteString("THE OPERATOR INTERRUPTED THAT RESPONSE\n")
	}
	if c.ResponseAgentSaid != "" {
		fmt.Fprintf(b, "AGENT SAID: %s\n", strings.TrimSpace(c.ResponseAgentSaid))
	}
	if c.NextOperatorText != "" {
		fmt.Fprintf(b, "OPERATOR SAID NEXT: %s\n", strings.TrimSpace(c.NextOperatorText))
	} else {
		b.WriteString("THE SESSION ENDED WITHOUT ANOTHER OPERATOR TURN\n")
	}
	if len(c.CycleWrites) > 0 {
		fmt.Fprintf(b, "FOR REFERENCE, THE AGENT HAD WRITTEN BEFORE THIS TURN: %s\n",
			strings.Join(c.CycleWrites, ", "))
	}
}

// writeContext renders the observed evidence a family needs. Only observed
// facts appear: paths written, calls that failed, words said. Nothing the
// classifier concluded is shown, because a labeler shown the build's answer
// agrees with the build.
func writeContext(b *strings.Builder, family string, c *calibrate.TurnContext) {
	if family == labels.FamilyPollution {
		writeResponseContext(b, c)
		return
	}
	if c.PriorOperatorText != "" {
		fmt.Fprintf(b, "PREVIOUS OPERATOR TURN: %s\n", strings.TrimSpace(c.PriorOperatorText))
	}
	if len(c.CycleWrites) > 0 {
		fmt.Fprintf(b, "AGENT WROTE SINCE THEN: %s\n", strings.Join(c.CycleWrites, ", "))
	}
	if family == labels.FamilyRecovery && len(c.EarlierWrites) > 0 {
		fmt.Fprintf(b, "AGENT HAD WRITTEN BEFORE THAT: %s\n", strings.Join(c.EarlierWrites, ", "))
	}
	if c.CycleFailedCalls > 0 {
		fmt.Fprintf(b, "TOOL CALLS THAT FAILED: %d\n", c.CycleFailedCalls)
	}
	if c.CycleInterrupted {
		b.WriteString("THE OPERATOR INTERRUPTED THE AGENT MID-CYCLE\n")
	}
	if c.AgentSaid != "" {
		fmt.Fprintf(b, "AGENT SAID: %s\n", strings.TrimSpace(c.AgentSaid))
	}
}

// PromptHash fingerprints the instructions a run used, so a label set can be
// attributed to the wording that produced it.
func PromptHash(family string) string {
	var parts []string
	for _, p := range Passes {
		r, err := rulesFor(family, p.Framing)
		if err != nil {
			continue
		}
		parts = append(parts, p.Name, r)
	}
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:16]
}
