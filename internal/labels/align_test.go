package labels

import (
	"os"
	"path/filepath"
	"testing"
)

// verdicts reduces an alignment to what each labeled unit was scored against, so
// two alignments can be compared without depending on pair order.
func verdicts(pairs []Pair) map[string]string {
	out := map[string]string{}
	for _, p := range pairs {
		if p.Label == nil {
			continue
		}
		key := p.Label.Text
		if p.Prediction == nil {
			out[key] = "(build silent)"
			continue
		}
		out[key] = p.Prediction.Value
	}
	return out
}

// Labels are keyed to a record and a byte span, never to a segment index,
// because segmentation is one of the things calibration changes. A rule that
// splits a turn's sentences differently must still be judged against the label a
// person wrote on that text.
//
// This is the golden case: the same turn segmented two ways, the same labels,
// and the same verdict for every labeled unit.
func TestAlignmentSurvivesASegmentationChange(t *testing.T) {
	const record = "rec-1"

	// The turn: "never touch the release workflow. it never retries. only edit
	// internal/worker."
	//            0123456789...
	ls := []Label{
		{Record: record, Family: FamilyObligation, Start: 0, End: 32, Label: Directive, Labeler: "a", Text: "never touch the release workflow."},
		{Record: record, Family: FamilyObligation, Start: 33, End: 52, Label: Description, Labeler: "a", Text: "it never retries."},
		{Record: record, Family: FamilyObligation, Start: 53, End: 82, Label: Directive, Labeler: "a", Text: "only edit internal/worker."},
	}

	// One segmentation splits on sentence-final punctuation.
	bySentence := []Prediction{
		{Record: record, Family: FamilyObligation, Start: 0, End: 32, Value: Directive},
		{Record: record, Family: FamilyObligation, Start: 33, End: 52, Value: Description},
		{Record: record, Family: FamilyObligation, Start: 53, End: 82, Value: Directive},
	}

	// Another trims trailing whitespace differently and lands the boundaries a
	// few bytes off. Same three units, different spans.
	shifted := []Prediction{
		{Record: record, Family: FamilyObligation, Start: 0, End: 31, Value: Directive},
		{Record: record, Family: FamilyObligation, Start: 34, End: 50, Value: Description},
		{Record: record, Family: FamilyObligation, Start: 54, End: 81, Value: Directive},
	}

	want := verdicts(Align(ls, bySentence, FamilyObligation))
	got := verdicts(Align(ls, shifted, FamilyObligation))

	if len(want) != 3 {
		t.Fatalf("the reference alignment covered %d labels, want 3", len(want))
	}
	for text, w := range want {
		if got[text] != w {
			t.Errorf("segmentation change moved the verdict for %q: %q -> %q", text, w, got[text])
		}
	}
}

// A label and a prediction that share no byte are not the same unit. Matching
// them would let a rule earn credit for a sentence it never looked at.
func TestDisjointSpansNeverMatch(t *testing.T) {
	ls := []Label{{Record: "r", Family: FamilyObligation, Start: 0, End: 10, Label: Directive, Labeler: "a"}}
	ps := []Prediction{{Record: "r", Family: FamilyObligation, Start: 20, End: 30, Value: Directive}}

	pairs := Align(ls, ps, FamilyObligation)
	for _, p := range pairs {
		if p.Label != nil && p.Prediction != nil {
			t.Error("a label matched a prediction it shares no byte with")
		}
	}
	if len(pairs) != 2 {
		t.Errorf("%d pairs, want the label and the prediction reported separately", len(pairs))
	}
}

// A whole-record unit — a turn, a cycle — carries a zero-length span and matches
// one prediction on that record.
func TestWholeRecordUnitsMatchOnTheRecord(t *testing.T) {
	ls := []Label{{Record: "r", Family: FamilyRecovery, Label: Opens, Labeler: "a"}}
	ps := []Prediction{{Record: "r", Family: FamilyRecovery, Value: Opens}}

	pairs := Align(ls, ps, FamilyRecovery)
	if len(pairs) != 1 || pairs[0].Label == nil || pairs[0].Prediction == nil {
		t.Fatalf("a whole-record label did not match its prediction: %+v", pairs)
	}
	if s := Evaluate(FamilyRecovery, pairs); s.TruePositives != 1 {
		t.Errorf("a matched positive scored %d true positives", s.TruePositives)
	}
}

// A build that says nothing about a labeled positive has missed it. Silence is
// not a negative prediction, and folding the two would let a rule improve its
// score by declining to answer.
func TestSilenceOnAPositiveIsAMiss(t *testing.T) {
	ls := []Label{
		{Record: "r1", Family: FamilyRecovery, Label: Opens, Labeler: "a"},
		{Record: "r2", Family: FamilyRecovery, Label: Unrelated, Labeler: "a"},
	}
	s := Evaluate(FamilyRecovery, Align(ls, nil, FamilyRecovery))

	if s.FalseNegatives != 1 {
		t.Errorf("false negatives = %d, want 1: the build was silent on a labeled open", s.FalseNegatives)
	}
	if s.TrueNegatives != 1 {
		t.Errorf("true negatives = %d, want 1: silence on a labeled non-event is the right answer", s.TrueNegatives)
	}
	if s.Recall != 0 {
		t.Errorf("recall = %v, want 0", s.Recall)
	}
}

// A unit the codebook could not decide is excluded from precision and recall and
// reported beside them. Forcing it into a class would hide a fact about the
// evidence inside a score.
func TestUnlabelableUnitsLeaveTheDenominator(t *testing.T) {
	ls := []Label{
		{Record: "r1", Family: FamilyObligation, Label: Directive, Labeler: "a"},
		{Record: "r2", Family: FamilyObligation, Label: Unlabelable, Labeler: "a", Note: "the turn is a paste with no addressee"},
	}
	ps := []Prediction{
		{Record: "r1", Family: FamilyObligation, Value: Directive},
		{Record: "r2", Family: FamilyObligation, Value: Directive},
	}
	s := Evaluate(FamilyObligation, Align(ls, ps, FamilyObligation))

	if s.Labeled != 1 {
		t.Errorf("labeled units = %d, want 1", s.Labeled)
	}
	if s.Unlabelable != 1 {
		t.Errorf("unlabelable units = %d, want 1", s.Unlabelable)
	}
	if s.FalsePositives != 0 {
		t.Error("an unlabelable unit was scored as a false positive")
	}
}

// A precision over no predictions is undefined. Printing zero would report a
// rule as maximally wrong when it made no claim at all.
func TestNoPredictionsLeavePrecisionUndefined(t *testing.T) {
	ls := []Label{{Record: "r", Family: FamilyObligation, Label: Description, Labeler: "a"}}
	s := Evaluate(FamilyObligation, Align(ls, nil, FamilyObligation))

	if FormatRate(s.Precision) != "undefined" {
		t.Errorf("precision over no predictions = %s", FormatRate(s.Precision))
	}
}

// A label marked unlabelable with no reason is refused at load. The reason is
// the finding: it says what about the record defeated the codebook.
func TestUnlabelableWithoutAReasonIsRefused(t *testing.T) {
	dir := t.TempDir()
	line := `{"session":"S","record":"r","family":"obligation","label":"unlabelable","labeler":"a"}`
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("a label marked unlabelable with no reason loaded cleanly")
	}
}

// The label set names itself. A calibration run that cannot name the labels it
// scored against is not evidence, so the hash has to move when the labels do.
func TestLabelSetHashesItsContents(t *testing.T) {
	write := func(body string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		set, err := Load(dir)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		return set.SHA256
	}
	a := `{"session":"S","record":"r","family":"obligation","label":"directive","labeler":"a"}` + "\n"
	b := `{"session":"S","record":"r","family":"obligation","label":"description","labeler":"a"}` + "\n"

	first, second, other := write(a), write(a), write(b)
	if first == other {
		t.Error("two different label sets share a hash")
	}
	if first != second {
		t.Error("the same label set hashed to two values")
	}
}
