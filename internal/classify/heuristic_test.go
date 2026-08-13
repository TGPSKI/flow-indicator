package classify

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func operator(text string) stream.Record {
	return stream.Record{SpeakerClass: stream.SpeakerHuman, Text: text, Metadata: stream.Metadata{}}
}

func agent(text string) stream.Record {
	return stream.Record{SpeakerClass: stream.SpeakerAgent, Text: text, Metadata: stream.Metadata{}}
}

func classify(t *testing.T, in Input) Result {
	t.Helper()
	res, err := Heuristic{}.Classify(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCorrectionTier1Markers(t *testing.T) {
	corrections := []string{
		"No. I said stop after CI passes.",
		"Wrong again. I did not ask for a new validation step.",
		"That's not what I asked. You just created an issue after I told you to stop.",
	}
	for _, text := range corrections {
		res := classify(t, Input{Turn: operator(text)})
		if !res.Correction.IsCorrection {
			t.Errorf("no correction candidate for %q", text)
		}
	}
}

// A first-time constraint is a constraint. Treating every "do not" as a
// correction would open a repair episode on ordinary steering.
func TestFirstTimeConstraintIsNotACorrection(t *testing.T) {
	res := classify(t, Input{Turn: operator("Do not touch the release workflow.")})
	if res.Correction.IsCorrection {
		t.Fatal("first-time negative constraint classified as a correction")
	}
	if len(res.Obligations) != 1 || res.Obligations[0].Kind != ObligationScope {
		t.Fatalf("obligations = %+v, want one scope obligation", res.Obligations)
	}
}

// Tier-two restatement language becomes correction evidence only when it
// repeats an obligation that is already active.
func TestRestatementEscalatesOnlyWithAnActiveRepeat(t *testing.T) {
	text := "Stop after CI passes. I already told you this."
	plain := classify(t, Input{Turn: operator(text)})
	if plain.Correction.IsCorrection {
		t.Fatal("tier-two marker alone opened a correction")
	}
	active := []ObligationRef{{Key: "stop after ci passes", Kind: ObligationStop}}
	repeat := classify(t, Input{Turn: operator(text), UnresolvedObligations: active})
	if !repeat.Correction.IsCorrection {
		t.Fatal("tier-two marker with an active repeat did not open a correction")
	}
}

func TestResetAndStopAreDistinct(t *testing.T) {
	stopOnly := classify(t, Input{Turn: operator("Stop after CI passes and wait for instructions.")})
	if stopOnly.Reset {
		t.Error("a stop condition was read as a session reset")
	}
	if !stopOnly.Stop {
		t.Error("stop marker missed")
	}
	reset := classify(t, Input{Turn: operator("Context clear. New session.")})
	if !reset.Reset {
		t.Error("reset marker missed")
	}
}

func TestSegmentsTileTheText(t *testing.T) {
	text := "Good. Add the backoff test. Do not touch the release workflow."
	res := classify(t, Input{Turn: operator(text)})
	if len(res.Segments) < 3 {
		t.Fatalf("segments = %d, want one per sentence", len(res.Segments))
	}
	if res.Segments[0].Start != 0 {
		t.Errorf("first segment starts at %d, want 0", res.Segments[0].Start)
	}
	last := res.Segments[len(res.Segments)-1]
	if last.End != len(text) {
		t.Errorf("last segment ends at %d, want %d", last.End, len(text))
	}
	for i := 1; i < len(res.Segments); i++ {
		if res.Segments[i].Start != res.Segments[i-1].End {
			t.Fatalf("segments %d and %d leave a gap", i-1, i)
		}
	}
	if got := res.CharsIn(BucketForward); got == 0 {
		t.Error("no forward chars for an imperative task sentence")
	}
	if got := res.CharsIn(BucketControl); got == 0 {
		t.Error("no control chars for a constraint sentence")
	}
}

func TestRestatedSentenceIsRestateNotControl(t *testing.T) {
	prior := []string{"Stop after CI passes."}
	res := classify(t, Input{Turn: operator("Stop after CI passes."), PriorOperatorText: prior})
	if got := res.CharsIn(BucketRestate); got == 0 {
		t.Fatalf("repeated sentence produced no restate chars: %+v", res.Segments)
	}
}

func TestUnlabelledTextIsOtherNotForward(t *testing.T) {
	res := classify(t, Input{Turn: operator("Hmm.")})
	if res.CharsIn(BucketForward) != 0 {
		t.Fatal("unlabelled text counted as forward work")
	}
	if res.CharsIn(BucketOther) == 0 {
		t.Fatal("unlabelled text not counted as other")
	}
}

func TestPointerTypes(t *testing.T) {
	cases := []struct {
		text string
		want PointerType
	}{
		{"Finish task 8.", PointerTask},
		{"Revert internal/worker/retry.go.", PointerFile},
		{"Don't touch M3a.", PointerNamespace},
		{"Stop after CI.", PointerOperation},
		{"That tripwire is the one I meant.", PointerAlias},
		{"Write something new.", PointerUnknown},
	}
	for _, c := range cases {
		got := classify(t, Input{Turn: operator(c.text)}).Pointer
		if got.Type != c.want {
			t.Errorf("pointer for %q = %q, want %q", c.text, got.Type, c.want)
		}
		if c.want != PointerUnknown && got.Chars == 0 {
			t.Errorf("pointer for %q has no chars", c.text)
		}
	}
}

func TestNearRepeatIsCandidateOnly(t *testing.T) {
	active := []ObligationRef{{Key: "do not touch the release workflow", Kind: ObligationNegative}}
	res := classify(t, Input{
		Turn:                  operator("Do not touch the release workflow file."),
		UnresolvedObligations: active,
	})
	if len(res.NearRepeats) == 0 {
		t.Fatal("no near-repeat candidate for a reworded obligation")
	}
	if res.NearRepeats[0].Jaccard < nearRepeatThreshold {
		t.Fatalf("near repeat below threshold: %v", res.NearRepeats[0].Jaccard)
	}
}

func TestAgentExpansionSignals(t *testing.T) {
	open := &RepairRef{ID: "r1", Depth: 1, Status: "open"}
	res := classify(t, Input{
		Turn:         agent("Reverted .github/workflows/release.yml. I also added a validation step to retry_test.go."),
		ActiveRepair: open,
	})
	if !res.Repair.ClaimedRepaired {
		t.Fatal("repair claim missed")
	}
	// The agent said it reverted the file. Nothing here verifies that it did.
	if res.Repair.TargetRepaired != nil {
		t.Fatalf("agent wording was promoted to verified repair: %v", *res.Repair.TargetRepaired)
	}
	if res.Repair.Expansions() == 0 {
		t.Fatal("expansion missed")
	}
}

// Outside an open episode an agent saying "reverted" answers no correction.
func TestRepairClaimNeedsAnOpenEpisode(t *testing.T) {
	res := classify(t, Input{Turn: agent("Reverted the file.")})
	if res.Repair.TargetRepaired != nil {
		t.Fatal("repair claim recorded with no open episode")
	}
}

func TestClassifyIsDeterministic(t *testing.T) {
	in := Input{
		Turn:                  operator("No. I said stop after CI passes. Revert internal/worker/retry.go."),
		PriorOperatorText:     []string{"Stop after CI passes."},
		UnresolvedObligations: []ObligationRef{{Key: "stop after ci passes", Kind: ObligationStop}},
	}
	if !reflect.DeepEqual(classify(t, in), classify(t, in)) {
		t.Fatal("two classifications of the same input differ")
	}
}

func TestProvenanceIsRecorded(t *testing.T) {
	res := classify(t, Input{Turn: operator("Add the test.")})
	p := res.Provenance
	if p.Classifier != "heuristic" || p.Version != HeuristicVersion || p.Hash == "" {
		t.Fatalf("provenance incomplete: %+v", p)
	}
}

// A marker inside a pasted document is the document's vocabulary, not the
// operator objecting to what the agent just did. WRECK #1 opened a repair
// episode on its first substantive turn because one literal "wrong" appeared
// somewhere in a 585-line analysis the operator pasted in, and the session
// then read RECOVERY for two hours of ordinary steering.
func TestBulkPasteMarkersAreNotCorrectionEvidence(t *testing.T) {
	body := strings.Repeat("a line of pasted analysis\n", bulkPasteLines+1)
	res := classify(t, Input{Turn: operator(body + "this got the wrong answer\nstop. do the thing\n")})
	if res.Correction.IsCorrection {
		t.Error("a tier-one marker inside a bulk paste was read as a correction")
	}
	if res.Stop {
		t.Error("a stop marker inside a bulk paste was read as a stop")
	}
}

// The gate is about scale, not about the words. The same sentences typed as a
// directive stay correction evidence.
func TestSameMarkersInADirectiveStayEvidence(t *testing.T) {
	res := classify(t, Input{Turn: operator("this got the wrong answer\nstop. do the thing\n")})
	if !res.Correction.IsCorrection {
		t.Error("a tier-one marker in a directive-scale turn was not read as a correction")
	}
	if !res.Stop {
		t.Error("a stop marker in a directive-scale turn was not read as a stop")
	}
}

// Obligations and references are about content the operator did deliver, so
// the bulk gate must not blank them.
func TestBulkPasteStillYieldsObligations(t *testing.T) {
	body := strings.Repeat("a line of pasted analysis\n", bulkPasteLines+1)
	res := classify(t, Input{Turn: operator(body + "never touch internal/worker/retry.go\n")})
	if len(res.Obligations) == 0 {
		t.Error("a bulk paste produced no obligation candidates")
	}
}
