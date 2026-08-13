package labels

import "sort"

// Prediction is one thing a build claimed about one unit of one record.
//
// It carries the same coordinates a label does — record identifier and byte span
// — so the two line up without either knowing how the other was produced.
type Prediction struct {
	Session string
	Record  string
	Family  string
	Start   int
	End     int
	// Value is the class predicted, drawn from the same vocabulary the labels
	// use. A build that predicts nothing about a unit emits no prediction for it,
	// which is different from predicting the negative class.
	Value string
	Text  string
	// Detail carries per-discriminator verdicts, so each can be scored alone
	// before any are combined. The keys are the discriminator names; the values
	// are what that discriminator alone would have concluded.
	Detail map[string]string
}

// WholeRecord reports that the prediction covers the record rather than a span.
func (p Prediction) WholeRecord() bool { return p.End <= p.Start }

// Pair is one aligned label and prediction. Either side may be absent: a
// prediction with no label is something the build claimed about a unit nobody
// judged, and a label with no prediction is a unit the build was silent about.
type Pair struct {
	Label      *Label
	Prediction *Prediction
}

// Align matches predictions to labels within a family.
//
// Matching is by record identifier, then by maximal byte overlap. Overlap rather
// than equality is the whole point: segmentation is one of the things
// calibration changes, so a rule that splits a sentence differently must still
// be judged against the label a person wrote on that text. A label and a
// prediction that share no byte are not the same unit and are never matched.
//
// Whole-record units — a turn, a cycle — carry a zero-length span and match any
// prediction on the same record, at most one each.
//
// Matching is greedy by descending overlap, which is stable: ties are broken by
// the earlier start, then by the label's position in the file, so the same
// inputs always produce the same pairing.
func Align(ls []Label, ps []Prediction, family string) []Pair {
	type candidate struct {
		li, pi  int
		overlap int
		start   int
	}
	var labelIdx []int
	for i, l := range ls {
		if l.Family == family {
			labelIdx = append(labelIdx, i)
		}
	}
	var predIdx []int
	for i, p := range ps {
		if p.Family == family {
			predIdx = append(predIdx, i)
		}
	}

	var cands []candidate
	for _, li := range labelIdx {
		for _, pi := range predIdx {
			l, p := ls[li], ps[pi]
			if l.Record != p.Record {
				continue
			}
			o := overlapOf(l, p)
			if o <= 0 {
				continue
			}
			cands = append(cands, candidate{li: li, pi: pi, overlap: o, start: p.Start})
		}
	}
	sort.SliceStable(cands, func(a, b int) bool {
		if cands[a].overlap != cands[b].overlap {
			return cands[a].overlap > cands[b].overlap
		}
		if cands[a].start != cands[b].start {
			return cands[a].start < cands[b].start
		}
		return cands[a].li < cands[b].li
	})

	usedL := make(map[int]bool)
	usedP := make(map[int]bool)
	var out []Pair
	for _, c := range cands {
		if usedL[c.li] || usedP[c.pi] {
			continue
		}
		usedL[c.li], usedP[c.pi] = true, true
		l, p := ls[c.li], ps[c.pi]
		out = append(out, Pair{Label: &l, Prediction: &p})
	}
	for _, li := range labelIdx {
		if !usedL[li] {
			l := ls[li]
			out = append(out, Pair{Label: &l})
		}
	}
	for _, pi := range predIdx {
		if !usedP[pi] {
			p := ps[pi]
			out = append(out, Pair{Prediction: &p})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return sortKey(out[a]) < sortKey(out[b]) })
	return out
}

// overlapOf is the number of bytes a label and a prediction share. A
// whole-record unit on either side matches the whole record, which is reported
// as one byte of overlap so it ranks below any real span agreement.
func overlapOf(l Label, p Prediction) int {
	if l.WholeRecord() || p.WholeRecord() {
		return 1
	}
	lo, hi := max(l.Start, p.Start), min(l.End, p.End)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

// sortKey orders pairs deterministically for reporting.
func sortKey(p Pair) string {
	switch {
	case p.Label != nil:
		return p.Label.Record + "\x00" + pad(p.Label.Start)
	case p.Prediction != nil:
		return p.Prediction.Record + "\x00" + pad(p.Prediction.Start)
	default:
		return ""
	}
}

func pad(n int) string {
	const width = 10
	s := ""
	for v := n; v > 0; v /= 10 {
		s = string(rune('0'+v%10)) + s
	}
	for len(s) < width {
		s = "0" + s
	}
	return s
}
