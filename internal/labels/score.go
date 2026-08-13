package labels

import (
	"fmt"
	"sort"
)

// Confusion is the count of every (labeled, predicted) combination, plus the
// units one side was silent about.
//
// Silence is kept separate from a negative prediction. A build that says nothing
// about a unit has not predicted the negative class, and folding the two would
// let a rule improve its score by declining to answer.
type Confusion struct {
	// Cells is keyed by labeled class, then predicted class.
	Cells map[string]map[string]int
	// MissedByBuild counts labeled units the build predicted nothing about.
	MissedByBuild map[string]int
	// Unlabeled counts predicted units nobody judged.
	Unlabeled map[string]int
	// UnlabelableUnits counts labeled units the codebook could not decide. They
	// are excluded from precision and recall and reported beside them: they
	// bound what the instrument could have achieved.
	UnlabelableUnits int
}

// Score is one family's result at one iteration.
type Score struct {
	Family    string    `json:"family"`
	Confusion Confusion `json:"-"`

	TruePositives  int `json:"true_positives"`
	FalsePositives int `json:"false_positives"`
	FalseNegatives int `json:"false_negatives"`
	TrueNegatives  int `json:"true_negatives"`

	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`

	// Labeled is how many units carried a decidable label. Unlabelable is how
	// many did not.
	Labeled     int `json:"labeled_units"`
	Unlabelable int `json:"unlabelable_units"`
	// PredictedPos and LabeledPos are both counted over the scored units. They
	// share a denominator, so their ratio is a rate rather than a comparison
	// between the corpus and the label set.
	PredictedPos int `json:"predicted_positive"`
	LabeledPos   int `json:"labeled_positive"`

	// Consequence is what the score means for someone reading the meter, in
	// words and in the units they read. A score that improves while this does
	// not is a score that is measuring the harness.
	Consequence string `json:"operator_facing_consequence"`
}

// Rate is a proportion that may have no denominator. Unknown is not zero: a
// precision over no predictions is undefined, and printing 0 would report a
// rule as maximally wrong when it made no claim at all.
func rate(num, den int) float64 {
	if den == 0 {
		return -1
	}
	return float64(num) / float64(den)
}

// FormatRate renders a rate, with the undefined case named rather than printed
// as a number.
func FormatRate(r float64) string {
	if r < 0 {
		return "undefined"
	}
	return fmt.Sprintf("%.3f", r)
}

// Evaluate scores one family's aligned pairs against the positive class.
func Evaluate(family string, pairs []Pair) Score {
	s := Score{
		Family: family,
		Confusion: Confusion{
			Cells:         map[string]map[string]int{},
			MissedByBuild: map[string]int{},
			Unlabeled:     map[string]int{},
		},
	}
	pos := positives[family]

	for _, p := range pairs {
		switch {
		case p.Label != nil && p.Prediction != nil:
			if p.Label.Label == Unlabelable {
				s.Unlabelable++
				s.Confusion.UnlabelableUnits++
				continue
			}
			s.Labeled++
			cell(s.Confusion.Cells, p.Label.Label, p.Prediction.Value)
			lp, pp := pos[p.Label.Label], pos[p.Prediction.Value]
			switch {
			case lp && pp:
				s.TruePositives++
			case !lp && pp:
				s.FalsePositives++
			case lp && !pp:
				s.FalseNegatives++
			default:
				s.TrueNegatives++
			}

		case p.Label != nil:
			if p.Label.Label == Unlabelable {
				s.Unlabelable++
				s.Confusion.UnlabelableUnits++
				continue
			}
			s.Labeled++
			s.Confusion.MissedByBuild[p.Label.Label]++
			// The build was silent. For a positive label that is a miss; for a
			// negative one it is the right answer arrived at by saying nothing,
			// which is what a rule that does not over-fire looks like.
			if pos[p.Label.Label] {
				s.FalseNegatives++
			} else {
				s.TrueNegatives++
			}

		case p.Prediction != nil:
			s.Confusion.Unlabeled[p.Prediction.Value]++
			// A claim about a unit nobody judged is not scored. Counting it as
			// wrong would punish a build for reaching past the labeled set, and
			// counting it as right would reward it for the same thing.
		}
	}

	// Both counts are taken over the scored units only — those carrying a
	// decidable label. A ratio between every prediction the build made and the
	// positives among the handful of units somebody judged is not a ratio: it
	// divides the size of the corpus by the size of the label set and reads the
	// answer as an error rate.
	s.PredictedPos = s.TruePositives + s.FalsePositives
	s.LabeledPos = s.TruePositives + s.FalseNegatives

	s.Precision = rate(s.TruePositives, s.TruePositives+s.FalsePositives)
	s.Recall = rate(s.TruePositives, s.TruePositives+s.FalseNegatives)
	if s.Precision > 0 && s.Recall > 0 {
		s.F1 = 2 * s.Precision * s.Recall / (s.Precision + s.Recall)
	} else {
		s.F1 = rate(0, 0)
	}
	s.Consequence = consequenceOf(s)
	return s
}

// consequenceOf states the score in the units an operator reads the meter in.
//
// Precision and recall are properties of a rule. What the operator sees is an
// inventory that is too large, or episodes that never appeared. The two move
// together when the instrument is improving and come apart when the score is
// measuring the harness, which is why both are reported or neither is.
func consequenceOf(s Score) string {
	switch s.Family {
	case FamilyObligation:
		if s.LabeledPos == 0 {
			return "no sentence in the labeled set states a requirement, so the inventory has no denominator"
		}
		ratio := float64(s.PredictedPos) / float64(s.LabeledPos)
		// The family was built to stop over-reporting, so the phrasing assumed
		// over-reporting. It runs both ways, and a rule that misses most of what
		// it should catch is not improved by being described as inflated.
		if ratio < 1 {
			return fmt.Sprintf("across the labeled sentences the inventory holds %.0f%% of what the operator asked for: "+
				"%d candidates stand where %d requirements were labeled, and %d of the misses carried no marker at all",
				ratio*100, s.PredictedPos, s.LabeledPos, s.Confusion.MissedByBuild[Directive])
		}
		return fmt.Sprintf("across the labeled sentences the inventory is inflated %.2fx: %d candidates stand where %d requirements were labeled",
			ratio, s.PredictedPos, s.LabeledPos)
	case FamilyRecovery:
		if s.LabeledPos == 0 {
			return "no labeled turn opens or deepens an episode, so there is no episode to have been seen"
		}
		return fmt.Sprintf("%d repair episodes in %d were seen; %d turns opened an episode that no operator did",
			s.TruePositives, s.LabeledPos, s.FalsePositives)
	case FamilyPollution:
		decided := s.TruePositives + s.FalsePositives
		if s.Labeled == 0 {
			return "no cycle carries a decidable label"
		}
		return fmt.Sprintf("%d cycles in %d reached a verdict, and %d of those verdicts were wrong",
			decided, s.Labeled, s.FalsePositives)
	case FamilyObligationPair:
		return fmt.Sprintf("%d repeats reported against %d labeled; a loose repeat inflates repeated_obligations, which feeds a regime rule",
			s.PredictedPos, s.LabeledPos)
	default:
		return ""
	}
}

func cell(m map[string]map[string]int, labeled, predicted string) {
	if m[labeled] == nil {
		m[labeled] = map[string]int{}
	}
	m[labeled][predicted]++
}

// Classes lists every class appearing in a confusion matrix, sorted, so the
// matrix prints in a stable order.
func (c Confusion) Classes() []string {
	seen := map[string]struct{}{}
	for l, row := range c.Cells {
		seen[l] = struct{}{}
		for p := range row {
			seen[p] = struct{}{}
		}
	}
	for l := range c.MissedByBuild {
		seen[l] = struct{}{}
	}
	for p := range c.Unlabeled {
		seen[p] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Agreement is Cohen's kappa between two labeling passes, with the counts it
// was computed from.
type Agreement struct {
	Family   string  `json:"family"`
	LabelerA string  `json:"labeler_a"`
	LabelerB string  `json:"labeler_b"`
	Units    int     `json:"units_both_labeled"`
	Observed float64 `json:"observed_agreement"`
	Expected float64 `json:"expected_agreement"`
	Kappa    float64 `json:"cohens_kappa"`
}

// Kappa computes Cohen's kappa between two passes over the same family.
//
// Low agreement means the codebook is underspecified, and the fix is the
// codebook. It is reported rather than acted on here: this package counts, and
// what to do about a number is a decision for a person.
//
// Units only one pass labeled are excluded. Kappa is agreement between two
// judgements of the same thing, and a unit one labeler never saw is not a
// disagreement.
func Kappa(ls []Label, family, a, b string) Agreement {
	byUnit := func(labeler string) map[string]string {
		out := map[string]string{}
		for _, l := range ls {
			if l.Family == family && l.Labeler == labeler {
				out[unitKey(l)] = l.Label
			}
		}
		return out
	}
	la, lb := byUnit(a), byUnit(b)

	ag := Agreement{Family: family, LabelerA: a, LabelerB: b}
	countA, countB := map[string]int{}, map[string]int{}
	var same int
	for k, va := range la {
		vb, ok := lb[k]
		if !ok {
			continue
		}
		ag.Units++
		countA[va]++
		countB[vb]++
		if va == vb {
			same++
		}
	}
	if ag.Units == 0 {
		ag.Observed, ag.Expected, ag.Kappa = rate(0, 0), rate(0, 0), rate(0, 0)
		return ag
	}
	ag.Observed = float64(same) / float64(ag.Units)
	var expected float64
	for class, na := range countA {
		expected += (float64(na) / float64(ag.Units)) * (float64(countB[class]) / float64(ag.Units))
	}
	ag.Expected = expected
	if expected >= 1 {
		// Both passes used one class throughout. Kappa is undefined there, and
		// reporting 0 would call perfect agreement no better than chance.
		ag.Kappa = rate(0, 0)
		return ag
	}
	ag.Kappa = (ag.Observed - expected) / (1 - expected)
	return ag
}

func unitKey(l Label) string {
	return fmt.Sprintf("%s\x00%d\x00%d", l.Record, l.Start, l.End)
}
