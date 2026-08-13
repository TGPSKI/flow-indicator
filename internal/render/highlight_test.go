package render

import (
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/metrics"
)

// The first snapshot has nothing to have moved from. Marking it would light
// the whole table on the operator's first turn and say every metric changed.
func TestHighlightsFirstSnapshotMarksNothing(t *testing.T) {
	h := NewHighlights()
	now := time.Now()
	h.Observe(metrics.Snapshot{UserChars: 100, ControlBurden: metrics.KnownValue(0.5)}, now)
	if got := h.Active(now); len(got) != 0 {
		t.Errorf("first snapshot marked %v, want nothing", got)
	}
}

// Direction is in cost terms, not arithmetic ones.
func TestHighlightsDirectionIsCostNotArithmetic(t *testing.T) {
	h := NewHighlights()
	now := time.Now()
	h.Observe(metrics.Snapshot{
		ControlBurden:         metrics.KnownValue(0.10),
		ForwardShare:          metrics.KnownValue(0.80),
		UnresolvedObligations: 1,
		PointerSuccess:        3,
		UserChars:             100,
	}, now)
	h.Observe(metrics.Snapshot{
		ControlBurden:         metrics.KnownValue(0.30), // steering cost up: worse
		ForwardShare:          metrics.KnownValue(0.50), // forward work down: worse
		UnresolvedObligations: 0,                        // fewer left open: better
		PointerSuccess:        5,                        // more resolved: better
		UserChars:             120,                      // neither good nor bad
	}, now)

	got := h.Active(now)
	for id, want := range map[string]Move{
		"cpb":        MoveWorse,
		"forward":    MoveWorse,
		"unresolved": MoveBetter,
		"resolved":   MoveBetter,
		"now":        MoveNeutral,
	} {
		if got[id] != want {
			t.Errorf("%s moved %v, want %v", id, got[id], want)
		}
	}
}

// A value that did not move is not marked.
func TestHighlightsOnlyMarksWhatChanged(t *testing.T) {
	h := NewHighlights()
	now := time.Now()
	s := metrics.Snapshot{UserChars: 100, RepairDepth: 2}
	h.Observe(s, now)
	s.UserChars = 140
	h.Observe(s, now)

	got := h.Active(now)
	if _, marked := got["depth"]; marked {
		t.Error("an unchanged value was marked")
	}
	if got["now"] == MoveNone {
		t.Error("a changed value was not marked")
	}
}

// Marks expire, so the table does not stay lit between turns.
func TestHighlightsExpire(t *testing.T) {
	h := NewHighlights()
	start := time.Now()
	h.Observe(metrics.Snapshot{UserChars: 100}, start)
	h.Observe(metrics.Snapshot{UserChars: 200}, start)

	if len(h.Active(start.Add(highlightFor-time.Millisecond))) == 0 {
		t.Error("mark expired before the window closed")
	}
	if got := h.Active(start.Add(highlightFor)); len(got) != 0 {
		t.Errorf("mark survived the window: %v", got)
	}
}

// An unknown becoming known is a first value, not a change from zero.
func TestHighlightsUnknownToKnownIsNotAMove(t *testing.T) {
	h := NewHighlights()
	now := time.Now()
	h.Observe(metrics.Snapshot{SerializationInfl: metrics.Value{}}, now)
	h.Observe(metrics.Snapshot{SerializationInfl: metrics.KnownValue(1.4)}, now)
	if got := h.Active(now); got["si"] != MoveNone {
		t.Errorf("first known value marked as %v, want no mark", got["si"])
	}
}
