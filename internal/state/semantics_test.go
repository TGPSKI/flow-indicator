package state

import (
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
)

// Second-pass semantics. Each test here names a claim the projector is not
// allowed to make and the smaller claim it makes instead.

// "continue" gives the agent leave to keep going. It says nothing about whether
// the correction was met, so it cannot close a repair episode.
func TestContinuationDoesNotCloseARepair(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert the workflow change.", 2),
		agent("Reverted it.", 3),
		operator("continue", 4),
	)
	r := p.Repairs()[0]
	if r.Status != RepairOpen {
		t.Errorf("repair status = %s after a bare continuation, want %s", r.Status, RepairOpen)
	}
}

// Acceptance closes it, and so does forward work.
func TestAcceptanceClosesARepair(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert the workflow change.", 2),
		agent("Reverted it.", 3),
		operator("Good. Add the backoff test.", 4),
	)
	if got := p.Repairs()[0].Status; got != RepairProvisional {
		t.Errorf("repair status = %s after acceptance, want %s", got, RepairProvisional)
	}
}

// An episode nobody closed runs out of window. That bounds the measurement; it
// does not establish that the operator abandoned anything.
func TestElapsedDurabilityWindowIsUnknownNotAbandoned(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert the workflow change.", 2),
		agent("Reverted it.", 3),
	)
	var events []event.Event
	for i := range p.cfg.Window.CorrectionDurabilityTurns + 1 {
		events = append(events, push(p, operator("continue", 4+i))...)
	}
	if kinds(events)[event.KindRepairAbandoned] != 0 {
		t.Error("an elapsed window was reported as abandonment")
	}
	if got := p.Repairs()[0].Status; got != RepairUnknown {
		t.Errorf("repair status = %s, want %s", got, RepairUnknown)
	}
	// The bound is mechanical: the episode stops accruing so the regime cannot
	// latch on it.
	if p.active != nil {
		t.Error("the episode is still open and still accruing recovery characters")
	}
	if p.Regime() == RegimeThrash {
		t.Error("the regime latched at THRASH on an episode with no evidence behind it")
	}
}

// The same target coming back is the same episode at greater depth, not a
// second episode. Two records would say the operator corrected two things.
func TestRepairRecurrenceStaysOneEpisode(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop to internal/worker/retry.go.", 0),
		agent("Added it. I also updated the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go. Revert the rest.", 2),
		agent("Reverted it.", 3),
		operator("Good. Run the suite.", 4),
		agent("Suite is green.", 5),
		operator("No. The scope is internal/worker/retry.go. You touched the workflow again.", 6),
	)
	if n := len(p.Repairs()); n != 1 {
		t.Fatalf("repair episodes = %d, want 1", n)
	}
	r := p.Repairs()[0]
	if r.ID != "rep-1" {
		t.Errorf("repair id = %s, want rep-1", r.ID)
	}
	if r.Depth != 2 {
		t.Errorf("max repair depth = %d, want 2", r.Depth)
	}
	if !r.Recurred {
		t.Error("the episode is not marked as recurred")
	}
}

// A turn measured against a baseline that already contains it measures nothing.
func TestCurrentTurnIsNotInItsOwnBaseline(t *testing.T) {
	p := newProjector()
	minimum := p.cfg.Thresholds.MinimumControlBaseline
	for i := range minimum {
		push(p, operator("Run the suite and paste the summary line.", i))
	}
	// The n-th turn sees n-1 prior turns. One short of the minimum, so the
	// baseline is still unknown: a turn that counted itself would have reached
	// the minimum here and reported a number.
	s := p.Snapshot()
	if s.BaselineTurns != minimum-1 {
		t.Errorf("baseline turns = %d after %d turns, want %d prior turns", s.BaselineTurns, minimum, minimum-1)
	}
	if s.BaselineChars.Known {
		t.Errorf("baseline = %v from %d prior turns, under the minimum of %d",
			s.BaselineChars, s.BaselineTurns, minimum)
	}

	push(p, operator("Run the suite and paste the summary line.", minimum))
	base := p.Snapshot().BaselineChars
	if !base.Known {
		t.Fatalf("baseline still unknown with %d prior turns", p.Snapshot().BaselineTurns)
	}

	push(p, operator(longTurn(), minimum+1))
	s = p.Snapshot()
	if !s.SerializationInfl.Known {
		t.Fatal("serialization inflation is unknown with a full baseline")
	}
	want := float64(s.UserChars) / base.Num
	if s.SerializationInfl.Num != want {
		t.Errorf("serialization inflation = %v, want %v: the turn moved its own denominator",
			s.SerializationInfl.Num, want)
	}
}

func TestBaselineIsBoundedByTheRollingWindow(t *testing.T) {
	p := newProjector()
	for i := range p.cfg.Window.RollingTurns + 10 {
		push(p, operator("Run the suite and paste the summary line.", i))
	}
	if got := p.Snapshot().BaselineTurns; got > p.cfg.Window.RollingTurns {
		t.Errorf("baseline turns = %d, over the %d-turn window", got, p.cfg.Window.RollingTurns)
	}
}

func TestBaselineIsUnknownBeforeTheMinimum(t *testing.T) {
	p := newProjector()
	push(p, operator("Run the suite.", 0))
	if p.Snapshot().BaselineChars.Known {
		t.Error("a baseline was reported from one turn")
	}
	if p.Snapshot().SerializationInfl.Known {
		t.Error("inflation was reported without a baseline")
	}
}

// The whole response to a correction counts, not just the first record.
func TestPollutionAggregatesTheWholeResponseCycle(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert the workflow change.", 2),
		agent("Reverted it.", 3),
		agent("I also added a validation step to retry_test.go.", 4),
		agent("I went ahead and opened a follow-up issue.", 5),
		operator("Good. Run the suite.", 6),
	)
	r := p.Repairs()[0]
	if r.Expansions < 2 {
		t.Errorf("expansions = %d; the cycle after the first assistant record was not read", r.Expansions)
	}
	if !r.RepairClaimed {
		t.Error("the agent's repair claim was not recorded")
	}
}

// "Removed it." is the agent describing its own work.
func TestAgentClaimIsNotVerifiedRepair(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert the workflow change.", 2),
		agent("Reverted it.", 3),
		operator("Good. Run the suite.", 4),
	)
	r := p.Repairs()[0]
	if r.Pollution == metrics.PollutionClean {
		t.Error("pollution was called clean on the agent's wording alone")
	}
	if r.Pollution != metrics.PollutionUnknown {
		t.Errorf("pollution status = %q, want %q", r.Pollution, metrics.PollutionUnknown)
	}
	if !r.RepairClaimed {
		t.Error("the claim itself was not preserved")
	}
}

// A correction about one thing says nothing about an unrelated rule the
// operator happened to restate in the same breath.
func TestObligationViolationNeedsLinkage(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("Never touch the release workflow.", 0),
		agent("Understood.", 1),
		operator("Add the retry loop to internal/worker/retry.go.", 2),
		agent("Added it.", 3),
		operator("No. I meant internal/worker/backoff.go. Never touch the release workflow.", 4),
	)
	k := kinds(events)
	if k[event.KindObligationRepeated] == 0 {
		t.Fatal("the repeat itself was not recorded")
	}
	if k[event.KindObligationViolated] != 0 {
		t.Error("an unrelated correction was read as a violation of the repeated obligation")
	}
	for _, ob := range p.Obligations().All() {
		if ob.Status == ObligationViolated {
			t.Errorf("obligation %s was marked violated without linkage", ob.ID)
		}
	}
}

// Stopping the observer is not the interaction ending.
func TestStopObservationDrawsNoConclusions(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Add the retry loop to internal/worker/retry.go.", 0),
		agent("Added it and also updated the release workflow.", 1),
		operator("No. Revert the workflow change.", 2),
	)
	events := p.StopObservation(4096, "test")
	k := kinds(events)
	for _, forbidden := range []string{
		event.KindRepairAbandoned, event.KindRepairStatus, event.KindRepairReset,
		event.KindPointerResolved, event.KindEpochAdvanced, event.KindObligationExpired,
	} {
		if k[forbidden] != 0 {
			t.Errorf("stopping the observer emitted %s", forbidden)
		}
	}
	if k[event.KindObservationStopped] != 1 {
		t.Fatalf("no observation_stopped event: %v", k)
	}
	if got := p.Repairs()[0].Status; got != RepairOpen {
		t.Errorf("repair status = %s after the observer stopped, want %s", got, RepairOpen)
	}
}

func longTurn() string {
	const unit = "Run the suite and paste the summary line. "
	out := ""
	for range 12 {
		out += unit
	}
	return out
}

// Linkage does exist when the correction's identified target is the obligation
// itself. The narrowed rule must not make the violation path dead.
func TestObligationViolationFiresWithLinkage(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("Stop after CI passes.", 0),
		agent("Understood.", 1),
		operator("Add the retry loop.", 2),
		agent("Added it, ran the release workflow, and opened a PR.", 3),
		operator("No. Stop after CI passes.", 4),
	)
	if kinds(events)[event.KindObligationViolated] != 1 {
		t.Fatalf("a correction whose target is the obligation did not mark it violated: %v", kinds(events))
	}
	var violated int
	for _, ob := range p.Obligations().All() {
		if ob.Status == ObligationViolated {
			violated++
		}
	}
	if violated != 1 {
		t.Errorf("violated obligations = %d, want 1", violated)
	}
}
