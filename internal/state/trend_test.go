package state

import (
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
)

// burden builds a snapshot carrying one banded metric, so a case states the
// band it means rather than the arithmetic that produces it.
func burden(v float64) metrics.Snapshot {
	return metrics.Snapshot{ControlBurden: metrics.KnownValue(v)}
}

const (
	burdenGood = 0.10 // <= 0.25
	burdenMid  = 0.40 // <= 0.50
	burdenBad  = 0.80 // >  0.50
)

// One turn in a worse band is a turn. The crossing is reported only once the
// new band has held for the configured durability.
func TestTrendNeedsDurability(t *testing.T) {
	cfg := config.Default()
	cfg.Window.TrendDurabilityTurns = 2
	tt := newTrendTracker()

	if got := tt.observe(burden(burdenGood), cfg); len(got) != 0 {
		t.Fatalf("first snapshot reported %v, want nothing", got)
	}
	if got := tt.observe(burden(burdenBad), cfg); len(got) != 0 {
		t.Errorf("one turn in a new band reported %v, want nothing yet", got)
	}
	got := tt.observe(burden(burdenBad), cfg)
	if len(got) != 1 {
		t.Fatalf("held band reported %v, want one crossing", got)
	}
	if got[0].From != metrics.BandGood || got[0].To != metrics.BandBad {
		t.Errorf("crossing was %v → %v, want good → bad", got[0].From, got[0].To)
	}
	if !got[0].Degrading() {
		t.Error("good → bad is not marked degrading")
	}
}

// A metric that flicks into another band and comes back has not crossed.
func TestTrendResetsOnReturn(t *testing.T) {
	cfg := config.Default()
	cfg.Window.TrendDurabilityTurns = 2
	tt := newTrendTracker()

	tt.observe(burden(burdenGood), cfg)
	tt.observe(burden(burdenBad), cfg)  // one turn away
	tt.observe(burden(burdenGood), cfg) // back before it was durable
	if got := tt.observe(burden(burdenBad), cfg); len(got) != 0 {
		t.Errorf("a round trip reported %v, want nothing", got)
	}
}

// The same crossing is reported once. Staying bad is not news every turn.
func TestTrendReportsACrossingOnce(t *testing.T) {
	cfg := config.Default()
	cfg.Window.TrendDurabilityTurns = 2
	tt := newTrendTracker()

	tt.observe(burden(burdenGood), cfg)
	tt.observe(burden(burdenBad), cfg)
	if got := tt.observe(burden(burdenBad), cfg); len(got) != 1 {
		t.Fatalf("want the crossing reported, got %v", got)
	}
	for range 4 {
		if got := tt.observe(burden(burdenBad), cfg); len(got) != 0 {
			t.Errorf("staying in the band reported %v again", got)
		}
	}
}

// Improving is a crossing too, and it is not marked degrading.
func TestTrendReportsImprovement(t *testing.T) {
	cfg := config.Default()
	cfg.Window.TrendDurabilityTurns = 2
	tt := newTrendTracker()

	tt.observe(burden(burdenBad), cfg)
	tt.observe(burden(burdenGood), cfg)
	got := tt.observe(burden(burdenGood), cfg)
	if len(got) != 1 {
		t.Fatalf("want one crossing, got %v", got)
	}
	if got[0].Degrading() {
		t.Error("bad → good is marked degrading")
	}
}

// An unmeasured metric holds its band. A gap in the evidence is not a
// recovery, and becoming measurable is not a crossing.
func TestTrendIgnoresUnknown(t *testing.T) {
	cfg := config.Default()
	cfg.Window.TrendDurabilityTurns = 2
	tt := newTrendTracker()

	if got := tt.observe(metrics.Snapshot{}, cfg); len(got) != 0 {
		t.Errorf("an unmeasured snapshot reported %v", got)
	}
	if got := tt.observe(burden(burdenBad), cfg); len(got) != 0 {
		t.Errorf("becoming measurable reported %v, want nothing", got)
	}

	// A gap between two turns in the same new band must not reset the run.
	tt2 := newTrendTracker()
	tt2.observe(burden(burdenGood), cfg)
	tt2.observe(burden(burdenBad), cfg)
	tt2.observe(metrics.Snapshot{}, cfg) // unmeasured turn
	if got := tt2.observe(burden(burdenBad), cfg); len(got) != 1 {
		t.Errorf("a gap reset the run: got %v, want the crossing", got)
	}
}

// The bands themselves must agree with the configured thresholds, so a
// crossing and a regime rule cannot disagree about where a line is.
func TestBandsFollowConfiguredThresholds(t *testing.T) {
	cfg := config.Default()
	warn, high := cfg.Thresholds.SerializationWarn, cfg.Thresholds.SerializationHigh

	for _, c := range []struct {
		v    float64
		want metrics.Band
	}{
		{warn - 0.1, metrics.BandGood},
		{warn, metrics.BandMid},
		{high - 0.1, metrics.BandMid},
		{high, metrics.BandBad},
	} {
		if got := metrics.BandSerialization(metrics.KnownValue(c.v), warn, high); got != c.want {
			t.Errorf("SI %.1f banded %v, want %v", c.v, got, c.want)
		}
	}
	if got := metrics.BandSerialization(metrics.Value{}, warn, high); got != metrics.BandUnknown {
		t.Errorf("unknown SI banded %v, want unknown", got)
	}
}
