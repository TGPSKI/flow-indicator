package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// fallbackClassifier models a semantic tier that returns a marker result along
// with an error. The projector must retain the evidence rather than replacing
// it with an empty result merely because the optional tier failed.
type fallbackClassifier struct{ classify.Heuristic }

func (fallbackClassifier) Name() string    { return "fallback-test" }
func (fallbackClassifier) Version() string { return "1" }
func (fallbackClassifier) Hash() string    { return "fallback-test" }

func (f fallbackClassifier) Classify(ctx context.Context, in classify.Input) (classify.Result, error) {
	res, err := f.Heuristic.Classify(ctx, in)
	if err != nil {
		return res, err
	}
	return res, errors.New("semantic tier unavailable")
}

func newProjector() *Projector {
	return New(config.Default(), classify.Heuristic{}, "test")
}

func operator(text string, minute int) stream.Record {
	return stream.Record{
		SpeakerClass: stream.SpeakerHuman,
		Text:         text,
		Timestamp:    time.Date(2026, 3, 4, 13, minute, 0, 0, time.UTC),
		Metadata:     stream.Metadata{},
	}
}

func agent(text string, minute int) stream.Record {
	return stream.Record{
		SpeakerClass: stream.SpeakerAgent,
		Text:         text,
		Timestamp:    time.Date(2026, 3, 4, 13, minute, 0, 0, time.UTC),
		Metadata:     stream.Metadata{},
	}
}

// push feeds records and returns every event produced.
func push(p *Projector, records ...stream.Record) []event.Event {
	var out []event.Event
	for _, r := range records {
		out = append(out, p.Push(context.Background(), r)...)
	}
	return out
}

func kinds(events []event.Event) map[string]int {
	out := map[string]int{}
	for _, e := range events {
		out[e.Kind]++
	}
	return out
}

func TestEventsFollowPipelineOrder(t *testing.T) {
	p := newProjector()
	events := p.Push(context.Background(), operator("Add the retry test. Stop after CI.", 0))
	if len(events) < 3 {
		t.Fatalf("only %d events for a turn that carries an obligation", len(events))
	}
	if events[0].Kind != event.KindRecordObserved || events[0].Class != event.ClassObserved {
		t.Fatalf("first event is %s/%s, want an observed record", events[0].Kind, events[0].Class)
	}
	var lastClass int
	rank := map[event.EvidenceClass]int{event.ClassObserved: 0, event.ClassClassified: 1, event.ClassDerived: 2}
	for _, e := range events {
		if rank[e.Class] < lastClass {
			t.Fatalf("event %s of class %s came after a later class", e.Kind, e.Class)
		}
		lastClass = rank[e.Class]
	}
	if events[len(events)-1].Kind != event.KindMetricsComputed {
		t.Fatalf("last event is %s, want metrics", events[len(events)-1].Kind)
	}
}

func TestClassifierFailureKeepsSafeFallbackEvidence(t *testing.T) {
	p := projectorWith(fallbackClassifier{})
	events := push(p, operator("No. Revert internal/worker/retry.go.", 0))
	got := kinds(events)
	if got[event.KindClassifierFailed] != 1 {
		t.Fatalf("classifier failures = %d, want 1", got[event.KindClassifierFailed])
	}
	if got[event.KindCorrectionCandidate] != 1 {
		t.Fatal("the classifier failure erased the heuristic correction candidate")
	}
	if got[event.KindRepairOpened] != 1 {
		t.Fatal("the classifier failure erased the derived repair transition")
	}
	if got[event.KindSegmentsClassified] != 1 {
		t.Fatal("the classifier failure erased the heuristic segment evidence")
	}
}

func TestObligationRepeatIsExactAfterNormalization(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("Stop after CI passes.", 0),
		agent("Done.", 1),
		operator("stop after ci passes", 2),
	)
	got := kinds(events)
	if got[event.KindObligationIntroduced] != 1 {
		t.Errorf("introduced %d obligations, want 1", got[event.KindObligationIntroduced])
	}
	if got[event.KindObligationRepeated] != 1 {
		t.Errorf("repeated %d obligations, want 1", got[event.KindObligationRepeated])
	}
	if n := len(p.Obligations().Unresolved()); n != 1 {
		t.Errorf("unresolved candidates = %d, want 1: a repeat is not a new obligation", n)
	}
}

// Depth counts the corrections established as being about the same target. Each
// correction here names the same scope sentence, so each one belongs to the
// episode the first one opened.
func TestRepairDepthCountsCorrectionsOfRepair(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go.", 2),
		agent("Reverted it. I also added a validation step.", 3),
		operator("Wrong. The scope is internal/worker/retry.go.", 4),
		agent("Removed it. I opened issue #12.", 5),
		operator("That's not what I asked. The scope is internal/worker/retry.go.", 6),
	)
	repairs := p.Repairs()
	if len(repairs) != 1 {
		t.Fatalf("repair episodes = %d, want 1: corrections about the same target deepen one episode", len(repairs))
	}
	if repairs[0].Depth != 3 {
		t.Fatalf("repair depth = %d, want 3", repairs[0].Depth)
	}
	if repairs[0].Chars == 0 || repairs[0].Records == 0 {
		t.Errorf("episode carries no recovery cost: chars %d records %d", repairs[0].Chars, repairs[0].Records)
	}
}

func TestEpisodeDoesNotCloseBecauseAnAssistantSpoke(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert the workflow change.", 2),
		agent("Reverted it.", 3),
	)
	if p.Regime() != RegimeRecovery {
		t.Fatalf("regime = %s, want RECOVERY while the episode is open", p.Regime())
	}
	push(p, operator("Correct. Add the backoff test.", 4))
	repairs := p.Repairs()
	if repairs[0].Status != RepairProvisional {
		t.Fatalf("status = %s, want provisionally_closed after acceptance", repairs[0].Status)
	}
}

func TestDurableCloseNeedsTheRecurrenceWindow(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert .github/workflows/release.yml.", 2),
		agent("Reverted it.", 3),
		operator("Correct. Add the backoff test.", 4),
	)
	if got := p.Repairs()[0].Status; got != RepairProvisional {
		t.Fatalf("status = %s, want provisionally_closed before the window passes", got)
	}
	for i := 0; i < config.Default().Window.CorrectionDurabilityTurns; i++ {
		push(p, agent("Done.", 5+i), operator("Add the next test.", 6+i))
	}
	if got := p.Repairs()[0].Status; got != RepairDurable {
		t.Fatalf("status = %s, want durably_closed after the window passed with no recurrence", got)
	}
}

// Without an identifiable target there is no way to tell whether the same
// failure came back, so the episode ends as unknown rather than durably closed.
func TestDurableCloseIsUnknownWithoutATarget(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Undo that edit.", 2),
		agent("Reverted it.", 3),
		operator("Correct. Add the backoff test.", 4),
	)
	if key := p.Repairs()[0].TargetKey; key != "" {
		t.Skipf("correction carried a target key %q; this case needs one without", key)
	}
	for i := 0; i < config.Default().Window.CorrectionDurabilityTurns; i++ {
		push(p, agent("Done.", 5+i), operator("Add the next test.", 6+i))
	}
	if got := p.Repairs()[0].Status; got != RepairUnknown {
		t.Fatalf("status = %s, want unknown when target equivalence cannot be established", got)
	}
}

func TestResetClosesRepairAdvancesEpochAndBlanksObligations(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("Add the retry loop. Stop after CI.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert the workflow change.", 2),
		agent("Reverted it.", 3),
		operator("Context clear. New session.", 4),
	)
	got := kinds(events)
	if got[event.KindRepairReset] != 1 {
		t.Errorf("repair_reset events = %d, want 1", got[event.KindRepairReset])
	}
	if got[event.KindEpochAdvanced] != 1 {
		t.Errorf("epoch_advanced events = %d, want 1", got[event.KindEpochAdvanced])
	}
	if p.Epoch() != 1 {
		t.Errorf("epoch = %d, want 1", p.Epoch())
	}
	if p.Repairs()[0].Status != RepairReset {
		t.Errorf("repair status = %s, want reset: a reset is never a clean recovery", p.Repairs()[0].Status)
	}
	for _, ob := range p.Obligations().All() {
		if ob.Status != ObligationUnknown {
			t.Errorf("obligation %s status = %s, want unknown after a reset", ob.ID, ob.Status)
		}
	}
}

// The epoch's rolling baselines restart, but the turn carrying the reset is
// still measured against the epoch it closed.
func TestEpochResetRestartsRollingBaselines(t *testing.T) {
	p := newProjector()
	push(p, operator("Context clear. New session.", 0))
	push(p, agent("Waiting.", 1), operator("Stop after CI passes.", 2))
	if p.Snapshot().BaselineTurns != 0 {
		t.Errorf("baseline turns = %d, want 0 in a fresh epoch", p.Snapshot().BaselineTurns)
	}
	if p.Snapshot().Epoch != 1 {
		t.Errorf("snapshot epoch = %d, want 1", p.Snapshot().Epoch)
	}
}

// The source running out is not the operator giving up. An episode still open
// at the last byte has an unknown outcome, and nothing may call it abandoned.
func TestOpenEpisodeAtEndOfSourceIsUnknown(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert the workflow change.", 2),
	)
	events := p.Finish()
	if kinds(events)[event.KindRepairAbandoned] != 0 {
		t.Error("the end of the source was reported as abandonment")
	}
	if kinds(events)[event.KindRepairStatus] != 1 {
		t.Fatalf("open episode got no status at the end of the source: %v", kinds(events))
	}
	if got := p.Repairs()[0].Status; got != RepairUnknown {
		t.Errorf("repair status = %s, want %s", got, RepairUnknown)
	}
}

func TestSidechainHumanRecordsAreNotOperatorTurns(t *testing.T) {
	p := newProjector()
	rec := operator("No. That is wrong.", 0)
	rec.Metadata[stream.MetaSidechain] = "true"
	events := p.Push(context.Background(), rec)
	if kinds(events)[event.KindMetricsComputed] != 0 {
		t.Fatal("a sidechain record was measured as operator serialization")
	}
	if len(p.Repairs()) != 0 {
		t.Fatal("a sidechain record opened a repair episode")
	}
}

func TestRegimePrecedence(t *testing.T) {
	th := config.Default().Thresholds
	cases := []struct {
		name string
		in   RegimeInput
		want Regime
	}{
		{"reset beats everything", RegimeInput{Reset: true, RepairOpen: true, RepairDepth: 9, Thresholds: th}, RegimeReset},
		{"thrash by depth", RegimeInput{RepairOpen: true, RepairDepth: 3, Thresholds: th}, RegimeThrash},
		{"thrash by recovery chars", RegimeInput{RepairOpen: true, RepairDepth: 1, RepairChars: 400, Baseline: metrics.KnownValue(60), Thresholds: th}, RegimeThrash},
		{"recovery while an episode is open", RegimeInput{RepairOpen: true, RepairDepth: 1, Thresholds: th}, RegimeRecovery},
		{"drift on two corrections", RegimeInput{RecentCorrections: 2, Thresholds: th}, RegimeDrift},
		{"drift on two interrupts", RegimeInput{RecentInterrupts: 2, Thresholds: th}, RegimeDrift},
		{"drift on one interrupt and one correction", RegimeInput{RecentInterrupts: 1, RecentCorrections: 1, Thresholds: th}, RegimeDrift},
		{"one interrupt alone is not drift", RegimeInput{RecentInterrupts: 1, Thresholds: th}, RegimeFlow},
		{"drift on two dereference misses", RegimeInput{RecentDereferenceFailure: 2, Thresholds: th}, RegimeDrift},
		{"drift on inflation with a repeated obligation", RegimeInput{SerializationInflation: metrics.KnownValue(3), RecentObligationRepeats: 1, Thresholds: th}, RegimeDrift},
		{"flow otherwise", RegimeInput{Thresholds: th}, RegimeFlow},
	}
	for _, c := range cases {
		if got := Evaluate(c.in); got != c.want {
			t.Errorf("%s: regime = %s, want %s", c.name, got, c.want)
		}
	}
}

// Every rule is identified by the input that fires only it. The identifier is
// what downstream readers state as the reason, so an input that fires one rule
// must not be reported under another's name.
func TestEachRuleIsIdentifiedByTheInputThatFiresIt(t *testing.T) {
	th := config.Default().Thresholds
	cases := []struct {
		in   RegimeInput
		want metrics.RegimeRule
	}{
		{RegimeInput{Reset: true, Thresholds: th}, metrics.RuleReset},
		{RegimeInput{RepairOpen: true, RepairDepth: 3, Thresholds: th}, metrics.RuleThrashRepairDepth},
		{RegimeInput{RepairOpen: true, RepairDepth: 1, RepairChars: 400,
			Baseline: metrics.KnownValue(60), Thresholds: th}, metrics.RuleThrashRecoveryChars},
		{RegimeInput{RepairOpen: true, RepairDepth: 1,
			RepeatedUnresolvedObligations: th.RepeatedObligationsWarn,
			SerializationInflation:        metrics.KnownValue(th.SerializationWarn),
			ForwardShare:                  metrics.KnownValue(0.1),
			Thresholds:                    th}, metrics.RuleThrashObligationInflation},
		{RegimeInput{RepairOpen: true, RepairDepth: 1, Thresholds: th}, metrics.RuleRecoveryOpen},
		{RegimeInput{RecentDereferenceFailure: 2, Thresholds: th}, metrics.RuleDriftDereference},
		{RegimeInput{RecentCorrections: 2, Thresholds: th}, metrics.RuleDriftControlActions},
		{RegimeInput{RecentInterrupts: 2, Thresholds: th}, metrics.RuleDriftControlActions},
		{RegimeInput{SerializationInflation: metrics.KnownValue(th.SerializationWarn),
			RecentObligationRepeats: 1, Thresholds: th}, metrics.RuleDriftSerializationObligation},
		{RegimeInput{Thresholds: th}, metrics.RuleFlow},
	}
	seen := map[metrics.RegimeRule]bool{}
	for _, c := range cases {
		d := Decide(c.in)
		if d.Rule != c.want {
			t.Errorf("rule = %q, want %q", d.Rule, c.want)
		}
		if d.License == "" {
			t.Errorf("%s: no license", c.want)
		}
		seen[c.want] = true
	}
	for _, rule := range []metrics.RegimeRule{
		metrics.RuleFlow, metrics.RuleReset, metrics.RuleRecoveryOpen,
		metrics.RuleThrashRepairDepth, metrics.RuleThrashRecoveryChars,
		metrics.RuleThrashObligationInflation,
		metrics.RuleDriftDereference, metrics.RuleDriftControlActions,
		metrics.RuleDriftSerializationObligation,
	} {
		if !seen[rule] {
			t.Errorf("rule %q has no input that fires only it", rule)
		}
	}
}

// The rule the projector recorded on the snapshot is the rule that decided the
// turn. A renderer reads it instead of guessing from the values beside it.
func TestSnapshotCarriesTheRuleThatDecidedTheTurn(t *testing.T) {
	p := newProjector()
	push(p, operator("Add the retry loop.", 0))
	if got := p.Snapshot().RegimeRule; got != metrics.RuleFlow {
		t.Errorf("rule = %q, want %q", got, metrics.RuleFlow)
	}
	push(p,
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert .github/workflows/release.yml.", 2),
	)
	if got := p.Snapshot().RegimeRule; got != metrics.RuleRecoveryOpen {
		t.Errorf("rule = %q, want %q", got, metrics.RuleRecoveryOpen)
	}
	if p.Snapshot().RegimeLicense == "" {
		t.Error("the snapshot carries no license for the rule that fired")
	}
}

// An unknown metric must not satisfy a regime threshold.
func TestUnknownMetricsDoNotTripRegimeRules(t *testing.T) {
	th := config.Default().Thresholds
	in := RegimeInput{
		RepairOpen:                    true,
		RepairDepth:                   1,
		RepairChars:                   100000,
		Baseline:                      metrics.Unknown(),
		SerializationInflation:        metrics.Unknown(),
		ForwardShare:                  metrics.Unknown(),
		RepeatedUnresolvedObligations: 5,
		Thresholds:                    th,
	}
	if got := Evaluate(in); got != RegimeRecovery {
		t.Fatalf("regime = %s, want RECOVERY: unknown metrics must not satisfy a thrash rule", got)
	}
	in.RepairOpen = false
	in.RecentObligationRepeats = 1
	if got := Evaluate(in); got != RegimeFlow {
		t.Fatalf("regime = %s, want FLOW: unknown inflation must not satisfy a drift rule", got)
	}
}

func TestSnippetHonoursPrivacySettings(t *testing.T) {
	cfg := config.Default()
	cfg.Privacy.SnippetChars = 10
	p := New(cfg, classify.Heuristic{}, "test")
	if got := p.snippet("0123456789abcdef"); got != "0123456789" {
		t.Errorf("snippet = %q, want the first 10 characters", got)
	}
	cfg.Privacy.StoreSnippets = false
	p = New(cfg, classify.Heuristic{}, "test")
	if got := p.snippet("0123456789abcdef"); got != "" {
		t.Errorf("snippet = %q, want nothing stored", got)
	}
	cfg.Privacy.StoreText = true
	p = New(cfg, classify.Heuristic{}, "test")
	if got := p.snippet("0123456789abcdef"); got != "0123456789abcdef" {
		t.Errorf("snippet = %q, want the full text when store_text is set", got)
	}
}

// interrupt is the record a harness writes when the operator stops the agent
// mid-turn. The operator authored no text.
func interrupt(minute int) stream.Record {
	return stream.Record{
		SpeakerClass: stream.SpeakerSystem,
		Speaker:      "system",
		Text:         "[Request interrupted by user]",
		Timestamp:    time.Date(2026, 3, 4, 13, minute, 0, 0, time.UTC),
		Metadata:     stream.Metadata{stream.MetaInputMode: stream.InputInterrupt},
	}
}

// An interruption is the operator spending a turn on the agent's behaviour
// instead of on the work. It carries no text, so nothing downstream saw it
// before: WRECK #1 read FLOW through three interruptions and an explicit
// "stop" inside seven minutes.
func TestInterruptsCountTowardDrift(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry test.", 1), agent("Added.", 2),
		interrupt(3),
		operator("Try again.", 4), agent("Working.", 5),
		interrupt(6),
		operator("Now do the other one.", 7),
	)
	if got := p.Regime(); got != RegimeDrift {
		t.Fatalf("regime after two interruptions = %s, want %s", got, RegimeDrift)
	}
}

// One interruption is not drift. A single escape is ordinary steering, and the
// FLOW control session contains one.
func TestOneInterruptIsNotDrift(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry test.", 1), agent("Added.", 2),
		interrupt(3),
		operator("Now do the other one.", 4),
	)
	if got := p.Regime(); got != RegimeFlow {
		t.Fatalf("regime after one interruption = %s, want %s", got, RegimeFlow)
	}
}

// The interruption is recorded as observed evidence so a reader can find it.
func TestInterruptEmitsAnObservedEvent(t *testing.T) {
	p := newProjector()
	events := push(p, operator("Add it.", 1), agent("Added.", 2), interrupt(3))
	if kinds(events)[event.KindOperatorInterrupt] != 1 {
		t.Fatalf("interrupt events = %d, want 1", kinds(events)[event.KindOperatorInterrupt])
	}
}

// An episode is the thing in play only while the operator is still on it.
// Membership decides attribution: once the operator has moved to other work,
// what they type next belongs to that work, not to the correction.
//
// In 61cc320d an episode opened on "i meant to continue" after an accidental
// interrupt. The next turns were new task assignment the marker rules could
// not label at all, so nothing closed the episode and 1,089 characters of new
// work were charged to it, pushing it past five times the control baseline.
// The session the operator closed with "FUCKING AWESOMEEEEE. what a run" read
// THRASH from that point to the end.
func TestMovingToUnclassifiedOtherWorkStillClosesEpisode(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert .github/workflows/release.yml.", 2),
		agent("Reverted it.", 3),
	)
	if got := p.Repairs()[0].Status; got != RepairOpen {
		t.Fatalf("status = %s, want open before the operator moves on", got)
	}
	before := p.Repairs()[0].Chars
	// A turn the marker rules label as nothing at all, on a different subject.
	push(p, operator("finally, i'd like a broad meta-analysis of tonight across all sessions", 4))
	if got := p.Repairs()[0].Status; got != RepairProvisional {
		t.Fatalf("status = %s, want provisionally_closed once the operator moved on", got)
	}
	push(p, agent("Here it is.", 5), operator("man i have 2 more things after that one", 6))
	if got := p.Repairs()[0].Chars; got != before {
		t.Errorf("closed episode kept accruing: %d -> %d", before, got)
	}
}

// Constraining the repair is staying on it. An operator who narrows the scope
// of the work the agent just got wrong is still controlling that episode, and
// closing it there would stop measuring while the operator is visibly on it.
func TestConstraintKeepsRepairEpisodeOpen(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert .github/workflows/release.yml.", 2),
		agent("Reverted it.", 3),
		operator("only touch the worker package for this", 4),
	)
	if got := p.Repairs()[0].Status; got != RepairOpen {
		t.Errorf("status = %s, want open after a scope constraint on the repair", got)
	}
}

// A prohibition is the same kind of evidence as a scope constraint: the
// operator is bounding what the agent may do about the thing it got wrong.
func TestNegativeConstraintKeepsRepairEpisodeOpen(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert .github/workflows/release.yml.", 2),
		agent("Reverted it.", 3),
		operator("do not add another test while you are in there", 4),
	)
	if got := p.Repairs()[0].Status; got != RepairOpen {
		t.Errorf("status = %s, want open after a negative constraint on the repair", got)
	}
}

// Permission to keep going is not acceptance, and it is not moving on either.
func TestContinueStillDoesNotCloseEpisode(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert .github/workflows/release.yml.", 2),
		agent("Reverted it.", 3),
		operator("continue", 4),
	)
	if got := p.Repairs()[0].Status; got != RepairOpen {
		t.Errorf("status = %s, want open after a bare continuation", got)
	}
}

// Staying on the episode keeps it open, whether or not the classifier can
// label the turn. Unknown classification must not decide membership by itself.
func TestStayingOnTheEpisodeKeepsItOpen(t *testing.T) {
	const established = "Keep every change inside internal/worker/retry.go."
	for _, turn := range []string{
		"stop",
		established, // restating what was already established
	} {
		p := newProjector()
		push(p,
			operator("Add the retry loop. "+established, 0),
			agent("Added it and also updated the release workflow.", 1),
			operator("No. Revert .github/workflows/release.yml.", 2),
			agent("Reverted it.", 3),
			operator(turn, 4),
		)
		if got := p.Repairs()[0].Status; got != RepairOpen {
			t.Errorf("%q: status = %s, want open", turn, got)
		}
	}
}
