package state

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/event"
)

// assertingClassifier asserts fixed resolutions about whatever the projector is
// holding, under a capability set the test chooses. It exists to exercise the
// guards between a classified resolution and the inventory, which is the only
// path that removes something the operator asked for.
//
// It asserts on every turn, agent turns included. A classifier that behaved
// this way would be wrong, and the point is that the projector does not depend
// on classifiers being right.
type assertingClassifier struct {
	classify.Heuristic
	caps  classify.Capabilities
	kind  string
	claim string
}

func (a assertingClassifier) Capabilities() classify.Capabilities { return a.caps }

func (a assertingClassifier) Classify(ctx context.Context, in classify.Input) (classify.Result, error) {
	res, err := a.Heuristic.Classify(ctx, in)
	if err != nil {
		return res, err
	}
	res.Resolutions = nil
	for _, ob := range in.UnresolvedObligations {
		res.Resolutions = append(res.Resolutions, classify.ObligationResolution{
			Key: ob.Key, Kind: a.kind, Evidence: a.claim,
		})
	}
	return res, nil
}

func projectorWith(cls classify.Classifier) *Projector {
	return New(config.Default(), cls, "test")
}

// The inventory has to be able to go down. Before resolution existed it counted
// what the operator asked for and never removed anything, so the number stopped
// carrying information after the first few turns.
func TestOperatorReleaseRemovesCandidateFromInventory(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Only edit internal/worker.", 0),
		agent("Understood.", 1),
	)
	before := len(p.Obligations().Unresolved())
	if before == 0 {
		t.Fatal("no candidate was introduced")
	}

	events := push(p, operator("Disregard the only edit internal/worker rule.", 2))

	if after := len(p.Obligations().Unresolved()); after != before-1 {
		t.Fatalf("inventory went %d to %d; the release removed nothing", before, after)
	}
	if n := kinds(events)[event.KindObligationReleased]; n != 1 {
		t.Fatalf("the release produced %d obligation_released events, want 1", n)
	}
	if got := p.Snapshot().ReleasedObligations; got != 1 {
		t.Errorf("released count = %d, want 1", got)
	}
	if got, want := p.Snapshot().UnresolvedObligations, len(p.Obligations().Unresolved()); got != want {
		t.Errorf("reported inventory = %d, unresolved candidates = %d", got, want)
	}

	// A transition that drops a requirement has to say what permitted it.
	for _, e := range events {
		if e.Kind != event.KindObligationReleased {
			continue
		}
		var payload struct {
			License string `json:"license"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.License == "" {
			t.Error("the release stores no premise; nothing records what licensed it")
		}
	}
}

// A requirement the operator states again after withdrawing it comes back under
// the identity it had before. A second candidate with a fresh identity would
// report a first statement where the session shows a re-assertion.
func TestRestatingAReleasedRequirementRevivesIt(t *testing.T) {
	p := newProjector()
	push(p,
		operator("Only edit internal/worker.", 0),
		agent("Understood.", 1),
		operator("Disregard the only edit internal/worker rule.", 2),
		agent("Understood.", 3),
	)
	all := p.Obligations().All()
	if len(all) != 1 {
		t.Fatalf("the surface holds %d candidates, want 1", len(all))
	}
	ob := all[0]
	if ob.Status != ObligationReleased {
		t.Fatalf("candidate status = %s after the release, want %s", ob.Status, ObligationReleased)
	}
	id, repeats := ob.ID, ob.RepeatCount

	events := push(p, operator("Only edit internal/worker.", 4))

	if n := len(p.Obligations().All()); n != 1 {
		t.Fatalf("restating the requirement created %d candidates; the round trip lost its identity", n)
	}
	if ob.Status != ObligationUnresolved {
		t.Errorf("revived candidate status = %s, want %s", ob.Status, ObligationUnresolved)
	}
	if ob.ID != id {
		t.Errorf("revived candidate id = %s, was %s", ob.ID, id)
	}
	if ob.RepeatCount != repeats+1 {
		t.Errorf("revived candidate repeat count = %d, want %d", ob.RepeatCount, repeats+1)
	}
	if got := p.Snapshot().ReleasedObligations; got != 0 {
		t.Errorf("released count = %d after the requirement came back, want 0", got)
	}

	var from string
	for _, e := range events {
		if e.Kind != event.KindObligationRevived {
			continue
		}
		var payload struct {
			From    string `json:"from_status"`
			License string `json:"license"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		from = payload.From
		if payload.License == "" {
			t.Error("the revival stores no premise")
		}
	}
	if from != ObligationReleased {
		t.Errorf("the revival records it came back from %q, want %q", from, ObligationReleased)
	}
}

// Satisfaction is never reachable from the agent's own account of its work.
// This is the same rule as ClaimedRepaired against TargetRepaired, and the
// guard is structural: the operator path is the only one that resolves.
func TestAgentTurnNeverResolvesAnObligation(t *testing.T) {
	cls := assertingClassifier{
		caps:  classify.Capabilities{classify.CapObligationSatisfaction},
		kind:  classify.ResolutionSatisfied,
		claim: "the agent said it did the thing",
	}
	p := projectorWith(cls)
	push(p, operator("Always run make ci.", 0))
	before := len(p.Obligations().Unresolved())
	if before == 0 {
		t.Fatal("no candidate was introduced")
	}

	events := push(p, agent("Done. I ran make ci and it passed.", 1))

	if after := len(p.Obligations().Unresolved()); after != before {
		t.Errorf("an agent turn moved the inventory from %d to %d", before, after)
	}
	if n := kinds(events)[event.KindObligationSatisfied]; n != 0 {
		t.Errorf("an agent turn produced %d obligation_satisfied events", n)
	}
	for _, ob := range p.Obligations().All() {
		if ob.Status == ObligationSatisfied {
			t.Errorf("obligation %s reached satisfied on the agent's own claim", ob.ID)
		}
	}
}

// A classifier does not get to assert a fact it cannot establish. Without this
// guard, extending the marker tier by accident would start emptying the
// inventory on evidence nobody declared.
func TestResolutionIsRefusedWithoutTheCapability(t *testing.T) {
	cls := assertingClassifier{
		caps:  classify.Capabilities{classify.CapObligationRelease},
		kind:  classify.ResolutionSatisfied,
		claim: "asserted without declaring the capability",
	}
	p := projectorWith(cls)
	push(p, operator("Always run make ci.", 0), agent("Understood.", 1))

	events := push(p, operator("Add the retry loop.", 2))

	if n := kinds(events)[event.KindObligationSatisfied]; n != 0 {
		t.Errorf("a classifier that declares no satisfaction capability produced %d obligation_satisfied events", n)
	}
	for _, ob := range p.Obligations().All() {
		if resolved(ob.Status) {
			t.Errorf("obligation %s reached %s under a classifier that cannot establish it", ob.ID, ob.Status)
		}
	}
}

// A resolution with no premise is refused outright rather than stored with an
// empty license. The license is what a reader audits when a requirement stops
// being counted, and there is nothing to audit here.
func TestResolutionWithoutEvidenceIsRefused(t *testing.T) {
	cls := assertingClassifier{
		caps:  classify.Capabilities{classify.CapObligationRelease},
		kind:  classify.ResolutionReleased,
		claim: "",
	}
	p := projectorWith(cls)
	push(p, operator("Always run make ci.", 0), agent("Understood.", 1))

	events := push(p, operator("Add the retry loop.", 2))

	if n := kinds(events)[event.KindObligationReleased]; n != 0 {
		t.Errorf("a resolution with no evidence produced %d obligation_released events", n)
	}
	if len(p.Obligations().Unresolved()) == 0 {
		t.Error("a resolution with no evidence emptied the inventory")
	}
}

// The satisfied status is reachable, under a classifier that declares it can
// establish satisfaction and on the operator's side of the exchange.
func TestSatisfactionIsReachableUnderASemanticTier(t *testing.T) {
	cls := assertingClassifier{
		caps:  classify.Capabilities{classify.CapObligationSatisfaction},
		kind:  classify.ResolutionSatisfied,
		claim: "the operator confirmed the suite ran green on their own machine",
	}
	p := projectorWith(cls)
	push(p, operator("Always run make ci.", 0), agent("Done.", 1))

	events := push(p, operator("Confirmed, the suite is green here too.", 2))

	if n := kinds(events)[event.KindObligationSatisfied]; n != 1 {
		t.Fatalf("got %d obligation_satisfied events, want 1", n)
	}
	var satisfied int
	for _, ob := range p.Obligations().All() {
		if ob.Status == ObligationSatisfied {
			satisfied++
			if ob.ResolvedTurn == 0 {
				t.Errorf("obligation %s was satisfied without recording the turn that did it", ob.ID)
			}
		}
	}
	if satisfied != 1 {
		t.Errorf("%d candidates reached satisfied, want 1", satisfied)
	}
	if got := p.Snapshot().SatisfiedObligation; got != 1 {
		t.Errorf("satisfied count = %d, want 1", got)
	}
}

// Every snapshot carries what the classifier behind it could establish, so a
// stored session read back later can tell an unmeasurable fact from an unknown
// measurement.
func TestSnapshotRecordsClassifierCapabilities(t *testing.T) {
	p := newProjector()
	push(p, operator("Add the retry loop.", 0))

	got := p.Snapshot().Capabilities
	want := classify.Heuristic{}.Capabilities().Strings()
	if !slices.Equal(got, want) {
		t.Errorf("snapshot capabilities = %v, want the configured classifier's declaration %v", got, want)
	}
	if len(got) == 0 {
		t.Error("the snapshot recorded no capabilities, so a reader cannot tell an unmeasurable fact from an unknown one")
	}
}
