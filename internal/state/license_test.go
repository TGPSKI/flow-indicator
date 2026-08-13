package state

import (
	"encoding/json"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/event"
)

// Transition license. Each test here names an implication the projector is not
// allowed to make, and the premise it requires instead. A transition whose
// premise is weaker than its conclusion is the defect; provenance reachability
// does not catch it, because an unlicensed transition is reachable too.
//
//	unknown target                  != target equivalence
//	active repair                   != this correction belongs to it
//	later correction                != failure of every pending reference
//	unresolved candidate            != requirement known to be in force
//	observer stop                   != source EOF
//	agent repair claim              != verified repair

// A correction with no established target is not evidence that it is about the
// open episode. This is WRECK's "i did not say push." arriving while an episode
// opened two hundred records earlier is still open.
func TestUnknownTargetDoesNotDeepenActiveRepair(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go.", 2),
		agent("Reverted it.", 3),
	)
	first := p.Repairs()[0]
	if first.TargetKey == "" {
		t.Fatalf("this case needs an episode with an established target; got none")
	}
	depthBefore := first.Depth

	events = append(events, push(p, operator("i did not say push.", 4))...)

	if got := kinds(events)[event.KindRepairDeepened]; got != 0 {
		t.Errorf("repair_deepened events = %d; an episode was deepened by a correction with no target", got)
	}
	if first.Depth != depthBefore {
		t.Errorf("episode depth = %d, want %d: presence of an open episode is not target identity",
			first.Depth, depthBefore)
	}
	if first.Status != RepairUnknown {
		t.Errorf("episode status = %s, want %s: nothing established how it ended", first.Status, RepairUnknown)
	}
	if n := len(p.Repairs()); n != 2 {
		t.Fatalf("repair episodes = %d, want 2: the new correction gets its own identity", n)
	}
	if second := p.Repairs()[1]; second.TargetKey != "" || second.TargetType != "unknown" {
		t.Errorf("second episode target = %s/%q, want unknown: an unknown target stays unknown",
			second.TargetType, second.TargetKey)
	}
}

// Two established targets that differ are two things to correct, whichever one
// happens to be open.
func TestDifferentTargetDoesNotDeepenActiveRepair(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go.", 2),
		agent("Reverted it.", 3),
		operator("Wrong. The scope is docs/CONFIG.md.", 4),
	)
	if got := kinds(events)[event.KindRepairDeepened]; got != 0 {
		t.Errorf("repair_deepened events = %d; an episode was deepened by a correction about something else", got)
	}
	repairs := p.Repairs()
	if len(repairs) != 2 {
		t.Fatalf("repair episodes = %d, want 2", len(repairs))
	}
	if repairs[0].Depth != 1 {
		t.Errorf("first episode depth = %d, want 1", repairs[0].Depth)
	}
	if repairs[0].TargetKey == repairs[1].TargetKey {
		t.Fatalf("both episodes carry target key %q; this case needs two different ones", repairs[0].TargetKey)
	}
}

// The narrowed rule must not make legitimate deepening dead.
func TestSameTargetCanDeepenActiveRepair(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go.", 2),
		agent("Reverted it.", 3),
		operator("Wrong. The scope is internal/worker/retry.go.", 4),
	)
	if got := kinds(events)[event.KindRepairDeepened]; got != 1 {
		t.Fatalf("repair_deepened events = %d, want 1: two equal established target keys license deepening", got)
	}
	repairs := p.Repairs()
	if len(repairs) != 1 {
		t.Fatalf("repair episodes = %d, want 1", len(repairs))
	}
	if repairs[0].Depth != 2 {
		t.Errorf("episode depth = %d, want 2", repairs[0].Depth)
	}
}

// One correction is one operator judgement. It is not a verdict on every
// compact reference that happened to be outstanding when it arrived.
func TestCorrectionDoesNotFailAllPendingPointers(t *testing.T) {
	p := newProjector()
	// Both references are still pending when the correction arrives: neither
	// intervening turn accepted anything or moved work forward.
	events := push(p,
		operator("Update retry.go now.", 0),
		agent("Updated it.", 1),
		operator("Interesting.", 2),
		agent("Anything else?", 3),
		operator("Interesting about docs/CONFIG.md.", 4),
		agent("Anything else?", 5),
		operator("No. That is wrong.", 6),
	)
	failures := outcomes(events)[OutcomeFailure]
	if failures != 1 {
		t.Fatalf("pointer failures = %d, want 1: one correction settled more than one reference", failures)
	}
	if p.pointerFailure != 1 {
		t.Errorf("pointer failure counter = %d, want 1", p.pointerFailure)
	}
	// The older reference was not settled by that correction. It ran out of
	// window instead, which is a different fact and says so.
	if got := outcomes(events)[OutcomeUnknown]; got != 1 {
		t.Errorf("pointer unknowns = %d, want 1: the older reference should have expired, not failed", got)
	}
}

// A reference the operator never came back to has no outcome. Silence is not
// success and it is not failure.
func TestPointerExpiryIsUnknown(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("Update retry.go now.", 0),
		agent("Updated it.", 1),
		operator("Interesting.", 2),
		agent("Anything else?", 3),
		operator("Interesting.", 4),
		agent("Anything else?", 5),
		operator("Interesting.", 6),
	)
	got := outcomes(events)
	if got[OutcomeUnknown] != 1 {
		t.Fatalf("pointer unknowns = %d, want 1: %v", got[OutcomeUnknown], got)
	}
	if got[OutcomeSuccess]+got[OutcomeFailure] != 0 {
		t.Errorf("a reference with no evidence either way was resolved: %v", got)
	}
	if p.pointerNoAnswer != 1 {
		t.Errorf("pointer unknown counter = %d, want 1", p.pointerNoAnswer)
	}
}

// Locality cuts both ways: the operator's own first response to what the agent
// did with a reference still settles it.
func TestLocalPointerCorrectionFailsOnePointer(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("Update retry.go now.", 0),
		agent("Updated it.", 1),
		operator("No. That is wrong.", 2),
	)
	if got := outcomes(events)[OutcomeFailure]; got != 1 {
		t.Fatalf("pointer failures = %d, want 1", got)
	}
}

// An operator turn that the agent has not answered yet evaluates nothing. The
// reference keeps its window rather than spending an opportunity that never
// existed.
func TestPointerNeedsAnAgentResponseBeforeAnOutcome(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("Update retry.go now.", 0),
		operator("Good.", 1),
	)
	if got := outcomes(events); len(got) != 0 {
		t.Fatalf("a reference resolved before the agent acted on it: %v", got)
	}
	if len(p.pendingPointers) != 1 {
		t.Fatalf("pending references = %d, want 1", len(p.pendingPointers))
	}
	if p.pendingPointers[0].remaining != p.cfg.Window.DereferenceOutcomeTurns {
		t.Errorf("outcome window spent on a turn that evaluated nothing: %d of %d left",
			p.pendingPointers[0].remaining, p.cfg.Window.DereferenceOutcomeTurns)
	}
}

// The inventory counts candidates introduced and not resolved. It is not a
// count of requirements known to remain in force, and leaving it takes evidence
// the turn actually carried.
//
// The turns below carry none: no release marker, no correction, nothing about
// whether the work was done. Every candidate has to still be counted. A rule
// that let any of these turns resolve something would be reading absence of
// evidence as evidence, which is the defect this whole vocabulary exists to
// catch.
func TestObligationInventoryDoesNotClaimContinuedForceWithoutEvidence(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Never touch the release workflow.", 0),
		agent("Understood.", 1),
		operator("Only edit internal/worker.", 2),
		agent("Understood. I did all of that and it works.", 3),
		operator("Add the retry loop.", 4),
	)
	all := p.Obligations().All()
	if len(all) == 0 {
		t.Fatal("no obligation candidates were introduced")
	}
	for _, ob := range all {
		switch ob.Status {
		case ObligationSatisfied, ObligationReleased, ObligationSuperseded, ObligationExpired:
			t.Errorf("obligation %s reached %s; nothing in these turns establishes it", ob.ID, ob.Status)
		case ObligationUnresolved, ObligationViolated, ObligationUnknown:
		default:
			t.Errorf("obligation %s has unrecognized status %q", ob.ID, ob.Status)
		}
	}
	if got, want := p.Snapshot().UnresolvedObligations, len(p.Obligations().Unresolved()); got != want {
		t.Errorf("reported inventory = %d, unresolved candidates = %d", got, want)
	}
	// The status word itself must not claim force.
	for _, ob := range p.Obligations().Unresolved() {
		if ob.Status == "active" {
			t.Errorf("obligation %s is labelled active; the instrument cannot establish that", ob.ID)
		}
	}
}

// Stopping the observer produces one observed fact and one derived inventory.
// Mixing them would file projected state as something read out of the source.
func TestObservationStopSeparatesObservedAndDerivedFacts(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Update retry.go now.", 0),
		agent("Updated it and also changed the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go.", 2),
	)
	events := p.StopObservation(4096, "test")
	if len(events) != 2 {
		t.Fatalf("stopping produced %d events, want 2", len(events))
	}

	observed, derived := events[0], events[1]
	if observed.Kind != event.KindObservationStopped || observed.Class != event.ClassObserved {
		t.Fatalf("first event is %s/%s, want %s/observed", observed.Kind, observed.Class, event.KindObservationStopped)
	}
	if derived.Kind != event.KindStateAtObservationStop || derived.Class != event.ClassDerived {
		t.Fatalf("second event is %s/%s, want %s/derived", derived.Kind, derived.Class, event.KindStateAtObservationStop)
	}

	// The observed event carries only what the source established.
	var obs map[string]any
	if err := json.Unmarshal(observed.Payload, &obs); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"open_repair", "pending_pointers", "repairs_awaiting_durability",
		"unresolved_obligation_candidates", "epoch",
	} {
		if _, ok := obs[field]; ok {
			t.Errorf("the observed stop event carries projected state in %q", field)
		}
	}
	for _, field := range []string{"reason", "source_offset", "last_record"} {
		if _, ok := obs[field]; !ok {
			t.Errorf("the observed stop event omits the observed fact %q", field)
		}
	}

	// The derived inventory names what was left open, and settles none of it.
	var inv struct {
		OpenRepair      string `json:"open_repair"`
		PendingPointers int    `json:"pending_pointers"`
	}
	if err := json.Unmarshal(derived.Payload, &inv); err != nil {
		t.Fatal(err)
	}
	if inv.OpenRepair == "" {
		t.Error("the inventory does not name the episode left open")
	}

	k := kinds(events)
	for _, forbidden := range []string{
		event.KindRepairAbandoned, event.KindRepairStatus, event.KindRepairReset,
		event.KindPointerResolved, event.KindEpochAdvanced, event.KindObligationExpired,
	} {
		if k[forbidden] != 0 {
			t.Errorf("stopping the observer emitted %s", forbidden)
		}
	}
	if got := p.Repairs()[0].Status; got != RepairOpen {
		t.Errorf("repair status = %s after the observer stopped, want %s", got, RepairOpen)
	}
	if p.Repairs()[0].Closed != nil {
		t.Error("the observer stopping wrote a closing timestamp onto an open episode")
	}
}

// An episode that is open must not carry a timestamp whose meaning is that it
// ended. Reopening drops the one written at the provisional close.
func TestReopenedRepairHasTruthfulTimestampState(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop to internal/worker/retry.go.", 0),
		agent("Added it. I also updated the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go. Revert the rest.", 2),
		agent("Reverted it.", 3),
	)
	r := p.Repairs()[0]
	if r.Status != RepairOpen {
		t.Fatalf("episode status = %s, want %s", r.Status, RepairOpen)
	}
	if r.Closed != nil {
		t.Errorf("an open episode carries a closing timestamp of %s", r.Closed)
	}

	push(p, operator("Good. Run the suite.", 4))
	if r.Status != RepairProvisional {
		t.Fatalf("episode status = %s, want %s", r.Status, RepairProvisional)
	}
	if r.Closed == nil {
		t.Fatal("a provisionally closed episode carries no closing timestamp")
	}

	push(p,
		agent("Suite is green.", 5),
		operator("No. The scope is internal/worker/retry.go. You touched the workflow again.", 6),
	)
	if r.Status != RepairOpen {
		t.Fatalf("episode status = %s after recurrence, want %s", r.Status, RepairOpen)
	}
	if r.Closed != nil {
		t.Errorf("a reopened episode still carries closed=%s; status open and a closing timestamp cannot both hold", r.Closed)
	}
	if !r.LastActivity.After(r.Start) {
		t.Errorf("last activity %s is not after start %s", r.LastActivity, r.Start)
	}
}

// Every derived repair transition stores the premise that licensed it, so a
// reader can audit the conclusion without re-deriving the stream.
func TestRepairTransitionsCarryTheirLicense(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("Add the retry loop to internal/worker/retry.go.", 0),
		agent("Added it. I also updated the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go. Revert the rest.", 2),
		agent("Reverted it.", 3),
		operator("Good. Run the suite.", 4),
		agent("Suite is green.", 5),
		operator("No. The scope is internal/worker/retry.go. You touched the workflow again.", 6),
		agent("Reverted it again.", 7),
		operator("Context clear. New session.", 8),
	)
	want := map[string]bool{
		event.KindRepairOpened:           true,
		event.KindRepairProvisionalClose: true,
		event.KindRepairRecurred:         true,
		event.KindRepairReset:            true,
	}
	seen := map[string]bool{}
	for _, e := range events {
		if !want[e.Kind] {
			continue
		}
		var p struct {
			License string `json:"license"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.License == "" {
			t.Errorf("%s stores no license: nothing records what permitted this transition", e.Kind)
		}
		seen[e.Kind] = true
	}
	for kind := range want {
		if !seen[kind] {
			t.Errorf("%s was never emitted; this case does not cover it", kind)
		}
	}
}

// No derived transition may reach the log without the premise that permitted
// it. A transition with nothing stored is one nobody can audit.
func TestEveryDerivedTransitionStoresAPremise(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("Add the retry loop. The scope is internal/worker/retry.go.", 0),
		agent("Added it. I also updated the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go.", 2),
		agent("Reverted it.", 3),
		operator("Good. Run the suite.", 4),
		agent("Suite is green.", 5),
		operator("Update docs/CONFIG.md now.", 6),
		agent("Updated it.", 7),
	)
	// Long enough for the durability window to close the episode.
	for i := range p.cfg.Window.CorrectionDurabilityTurns + 1 {
		events = append(events, push(p, agent("Done.", 8+i), operator("Add the next test.", 9+i))...)
	}
	events = append(events, push(p,
		agent("Added it.", 40),
		operator("Context clear. New session.", 41),
	)...)
	events = append(events, p.Finish()...)

	seen := map[string]bool{}
	for _, e := range events {
		if e.Class != event.ClassDerived || e.Kind == event.KindMetricsComputed {
			continue
		}
		var payload struct {
			License  string `json:"license"`
			Evidence string `json:"evidence"`
			Reason   string `json:"reason"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("%s: %v", e.Kind, err)
		}
		if payload.License == "" && payload.Evidence == "" && payload.Reason == "" {
			t.Errorf("%s stores no premise; nothing records what licensed this transition", e.Kind)
		}
		seen[e.Kind] = true
	}
	// The stream has to reach the transitions for the check to mean anything.
	for _, kind := range []string{
		event.KindObligationIntroduced, event.KindObligationRepeated,
		event.KindRepairOpened, event.KindRepairProvisionalClose, event.KindRepairDurableClose,
		event.KindPointerResolved, event.KindRegimeChanged, event.KindEpochAdvanced,
	} {
		if !seen[kind] {
			t.Errorf("%s was never emitted; this case does not cover it", kind)
		}
	}
}

// outcomes counts pointer resolutions by outcome.
func outcomes(events []event.Event) map[string]int {
	out := map[string]int{}
	for _, e := range events {
		if e.Kind != event.KindPointerResolved {
			continue
		}
		var p struct {
			Outcome string `json:"outcome"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			continue
		}
		out[p.Outcome]++
	}
	return out
}
