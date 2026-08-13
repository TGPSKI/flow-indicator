package state

import (
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
)

// A trend is a metric that crossed between quality bands and stayed across.
//
// It is the temporal reading the per-turn numbers cannot give. One turn in a
// worse band is one turn; the same band held over several turns is a direction
// the session is moving in, and that is what an operator steers on.
//
// Nothing here measures anything new. Bands are read off snapshots already
// computed, and no regime rule consults a trend: the regime is decided by the
// values themselves, so a trend cannot smuggle a second opinion into the
// state machine.

// trendMetric is one banded metric and the label it reports under.
type trendMetric struct {
	id   string
	band func(metrics.Snapshot, config.Config) metrics.Band
}

// trendMetrics are the measurements with a band worth crossing. A metric is
// here only when its quality is a matter of degree; counts that are only ever
// "more" are left out, because every increment would read as a decline.
var trendMetrics = []trendMetric{
	{"serialization_inflation", func(s metrics.Snapshot, c config.Config) metrics.Band {
		return metrics.BandSerialization(s.SerializationInfl,
			c.Thresholds.SerializationWarn, c.Thresholds.SerializationHigh)
	}},
	{"control_plane_burden", func(s metrics.Snapshot, _ config.Config) metrics.Band {
		return metrics.BandControlBurden(s.ControlBurden)
	}},
	{"forward_work_share", func(s metrics.Snapshot, _ config.Config) metrics.Band {
		return metrics.BandForwardShare(s.ForwardShare)
	}},
	{"dereference", func(s metrics.Snapshot, _ config.Config) metrics.Band {
		return metrics.BandDereference(s.Dereference)
	}},
	{"repair_depth", func(s metrics.Snapshot, c config.Config) metrics.Band {
		return metrics.BandRepairDepth(s.RepairDepth, c.Thresholds.ThrashRepairDepth)
	}},
}

// trendState is what one metric's crossing needs to be decided: the band that
// has been reported, and how long a different one has held since.
type trendState struct {
	confirmed metrics.Band
	candidate metrics.Band
	held      int
}

// trendTracker holds one state per banded metric.
type trendTracker struct {
	states map[string]*trendState
}

func newTrendTracker() *trendTracker {
	return &trendTracker{states: make(map[string]*trendState, len(trendMetrics))}
}

// trend is one reported crossing.
type trend struct {
	Metric string
	From   metrics.Band
	To     metrics.Band
	Turns  int
}

// Degrading reports whether the crossing moved towards the bad end.
func (t trend) Degrading() bool { return t.To > t.From }

// observe folds one operator turn's snapshot in and returns the crossings that
// became durable on this turn.
//
// A metric becoming measurable for the first time is not a crossing: there was
// no earlier band for it to have left. Nor is a return to the confirmed band,
// which resets the candidate rather than reporting a round trip.
func (tt *trendTracker) observe(s metrics.Snapshot, cfg config.Config) []trend {
	var out []trend
	for _, m := range trendMetrics {
		band := m.band(s, cfg)
		st, ok := tt.states[m.id]
		if !ok {
			st = &trendState{}
			tt.states[m.id] = st
		}

		// An unmeasured metric holds whatever it had. Absence is not a move
		// towards good or bad, and letting it reset the run would make a gap
		// in the evidence look like a recovery.
		if band == metrics.BandUnknown {
			continue
		}
		if st.confirmed == metrics.BandUnknown {
			st.confirmed = band
			continue
		}
		if band == st.confirmed {
			st.candidate, st.held = metrics.BandUnknown, 0
			continue
		}
		if band != st.candidate {
			st.candidate, st.held = band, 1
		} else {
			st.held++
		}
		if st.held >= cfg.Window.TrendDurabilityTurns {
			out = append(out, trend{Metric: m.id, From: st.confirmed, To: band, Turns: st.held})
			st.confirmed = band
			st.candidate, st.held = metrics.BandUnknown, 0
		}
	}
	return out
}
