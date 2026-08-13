package render

import (
	"time"

	"github.com/TGPSKI/flow-indicator/internal/metrics"
	"github.com/TGPSKI/flow-indicator/pkg/panel"
)

// Highlights remembers what the last snapshot held, so a value that moved can
// be marked for a moment afterwards.
//
// It is the meter's half of the tracking: which cell each measurement lands
// in, and what a climbing number costs the operator. The mechanism underneath
// is panel.Tracker, which knows neither.
type Highlights struct{ *panel.Tracker }

// risingIsWorse and risingIsBetter name what a climbing number means for the
// cost of steering. A metric in neither set moved without a direction worth
// colouring, and is marked in weight alone.
var moveDirections = panel.Directions{
	RisingWorse: []string{
		"si", "cpb", "unresolved", "new", "repeated",
		"missed", "depth", "repair_chars", "repair_records", "expansions",
	},
	RisingBetter: []string{"forward", "drp", "resolved"},
}

// NewHighlights returns a tracker with no history. The first snapshot it sees
// marks nothing: there is no earlier value for it to have moved from, and
// lighting the whole table up on the first operator turn would say every
// metric had just changed.
func NewHighlights() *Highlights {
	return &Highlights{panel.NewTracker(highlightFor, moveDirections)}
}

// Observe records a snapshot and marks whatever changed since the last one.
func (h *Highlights) Observe(s metrics.Snapshot, now time.Time) {
	h.Tracker.Observe(snapshotValues(s), now)
}

// snapshotValues is the numeric state of one snapshot, keyed by the cell
// identifiers the table uses. An unknown measurement is absent rather than
// zero, so becoming known reads as a first value and not as a change from
// nothing.
func snapshotValues(s metrics.Snapshot) map[string]float64 {
	out := map[string]float64{
		"now":            float64(s.UserChars),
		"unresolved":     float64(s.UnresolvedObligations),
		"new":            float64(s.NewObligations),
		"repeated":       float64(s.RepeatedObligations),
		"missed":         float64(s.PointerFailure),
		"resolved":       float64(s.PointerSuccess),
		"depth":          float64(s.RepairDepth),
		"repair_chars":   float64(s.RepairChars),
		"repair_records": float64(s.RepairRecords),
		"expansions":     float64(s.Expansions),
	}
	for id, v := range map[string]metrics.Value{
		"si":       s.SerializationInfl,
		"baseline": s.BaselineChars,
		"cpb":      s.ControlBurden,
		"forward":  s.ForwardShare,
		"drp":      s.Dereference,
	} {
		if v.Known {
			out[id] = v.Num
		}
	}
	return out
}
