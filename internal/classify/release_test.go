package classify

import (
	"context"
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// outstanding builds the inventory the projector would hand the classifier.
func outstanding(keys ...string) []ObligationRef {
	refs := make([]ObligationRef, 0, len(keys))
	for _, k := range keys {
		refs = append(refs, ObligationRef{ID: k, Key: stream.Normalize(k), Kind: ObligationScope, Text: k})
	}
	return refs
}

// classifyTurn runs the heuristic over one operator turn against an inventory.
func classifyTurn(t *testing.T, text string, refs []ObligationRef) Result {
	t.Helper()
	res, err := Heuristic{}.Classify(context.Background(), Input{
		Turn:                  stream.Record{SpeakerClass: stream.SpeakerHuman, Text: text},
		UnresolvedObligations: refs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A release names its candidate by restating it. The marker alone says a
// requirement was dropped and never says which.
func TestReleaseBindsToTheCandidateTheOperatorRestated(t *testing.T) {
	res := classifyTurn(t,
		"Disregard the only edit internal/worker rule.",
		outstanding("only edit internal/worker"))

	if len(res.Resolutions) != 1 {
		t.Fatalf("got %d resolutions, want 1: %+v", len(res.Resolutions), res.Resolutions)
	}
	r := res.Resolutions[0]
	if r.Key != "only edit internal/worker" {
		t.Errorf("resolution names %q", r.Key)
	}
	if r.Kind != ResolutionReleased {
		t.Errorf("resolution kind = %q, want %q", r.Kind, ResolutionReleased)
	}
	if !strings.Contains(r.Evidence, "disregard") {
		t.Errorf("evidence does not quote the marker it rests on: %q", r.Evidence)
	}
}

// A bare marker resolves nothing. "never mind" shares the word "never" with
// "never touch the release workflow" and says nothing whatever about it, so a
// rule that let an incidental token bind would drop a live requirement.
func TestBareReleaseMarkerResolvesNothing(t *testing.T) {
	for _, text := range []string{
		"Never mind.",
		"Never mind, let us move on.",
		"Scratch that.",
	} {
		res := classifyTurn(t, text, outstanding("never touch the release workflow"))
		if len(res.Resolutions) != 0 {
			t.Errorf("%q resolved %+v; it names no candidate", text, res.Resolutions)
		}
	}
}

// A release that fits two outstanding requirements equally names neither.
// Resolving the arbitrary one would remove a requirement still in force.
func TestAmbiguousReleaseResolvesNothing(t *testing.T) {
	res := classifyTurn(t,
		"Disregard the always run make rule.",
		outstanding("always run make ci", "always run make lint"))

	if len(res.Resolutions) != 0 {
		t.Errorf("an ambiguous release resolved %+v", res.Resolutions)
	}
}

// A release marker inside delivered material is the document's vocabulary, not
// the operator withdrawing anything. This is the guard that the correction
// markers already carry, for the same reason.
func TestBulkPasteSuppressesRelease(t *testing.T) {
	body := strings.Repeat("Some pasted analysis line.\n", bulkPasteLines+5)
	res := classifyTurn(t,
		body+"Disregard the only edit internal/worker rule.",
		outstanding("only edit internal/worker"))

	if len(res.Resolutions) != 0 {
		t.Errorf("a pasted document released %+v", res.Resolutions)
	}
}

// A sentence that withdraws a requirement does not state one. Without this the
// inventory grows on the turn meant to shrink it, because the sentence carries
// the words the obligation rules match on.
func TestReleaseSentenceIntroducesNoObligation(t *testing.T) {
	res := classifyTurn(t,
		"Disregard the only edit internal/worker scope rule.",
		outstanding("only edit internal/worker"))

	if len(res.Obligations) != 0 {
		t.Errorf("the release sentence introduced %+v", res.Obligations)
	}
}

// The default tier reaches release evidence and verified repair, and stops
// there.
//
// Release language is literal text. Verified repair is reachable because it
// stopped being a question about agent prose: a write to the path the operator
// named establishes it, and the source records that structurally.
//
// Satisfaction and supersession are still out of reach. Both ask whether one
// requirement stands in a particular relation to another or to the work, and a
// requirement is not a path.
func TestDefaultTierDeclaresOnlyWhatItReaches(t *testing.T) {
	caps := Heuristic{}.Capabilities()
	for _, c := range []Capability{CapObligationRelease, CapVerifiedRepair} {
		if !caps.Can(c) {
			t.Errorf("the default tier does not declare %s, which its own rules establish", c)
		}
	}
	for _, c := range []Capability{CapObligationSatisfaction, CapObligationSupersession} {
		if caps.Can(c) {
			t.Errorf("the default tier declares %s; no rule in it establishes that", c)
		}
	}
	if len(None{}.Capabilities()) != 0 {
		t.Error("a classifier that interprets nothing declares a capability")
	}
}

// Verified repair is decided by what the agent did, never by what it said. An
// agent that announces the repair without writing the target establishes
// nothing, and an agent that writes the target establishes it in silence.
func TestVerifiedRepairReadsActionsNotProse(t *testing.T) {
	active := &RepairRef{ID: "rep-1", TargetKey: "internal/worker.go", TargetPaths: []string{"internal/worker.go"}}

	claimOnly := agentTurn(t, "Reverted it. The target is back to what it was.", nil, active)
	if claimOnly.Repair.TargetRepaired != nil {
		t.Error("the agent's account of its own work established verified repair")
	}
	if !claimOnly.Repair.ClaimedRepaired {
		t.Error("the claim was not recorded as a claim")
	}

	silentWrite := agentTurn(t, "", []stream.Action{
		{Verb: stream.VerbWrite, Targets: []string{"internal/worker.go"}},
	}, active)
	if silentWrite.Repair.TargetRepaired == nil || !*silentWrite.Repair.TargetRepaired {
		t.Error("a write to the path the correction named did not establish repair")
	}
	if silentWrite.Repair.NewScope != 0 {
		t.Error("a write confined to the named target counted as expansion")
	}

	strayWrite := agentTurn(t, "", []stream.Action{
		{Verb: stream.VerbWrite, Targets: []string{"internal/worker.go"}},
		{Verb: stream.VerbWrite, Targets: []string{"docs/README.md"}},
	}, active)
	if strayWrite.Repair.NewScope != 1 {
		t.Error("a write outside every path the correction named was not counted as expansion")
	}
}

// A correction that named no path leaves repair unestablished. Reading the
// write set as the target would let the rule fire on a target it had to guess.
func TestNoNamedPathLeavesRepairUnestablished(t *testing.T) {
	res := agentTurn(t, "", []stream.Action{
		{Verb: stream.VerbWrite, Targets: []string{"internal/worker.go"}},
	}, &RepairRef{ID: "rep-1", TargetKey: "that thing"})

	if res.Repair.TargetRepaired != nil {
		t.Error("repair was established against a target the correction never named")
	}
	if res.Repair.NewScope != 0 {
		t.Error("expansion was counted with nothing to be outside of")
	}
}

// agentTurn classifies one agent record carrying text and actions.
func agentTurn(t *testing.T, text string, actions []stream.Action, active *RepairRef) Result {
	t.Helper()
	res, err := Heuristic{}.Classify(context.Background(), Input{
		Turn: stream.Record{
			SpeakerClass: stream.SpeakerAgent,
			Text:         text,
			Actions:      actions,
		},
		ActiveRepair: active,
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	return res
}
