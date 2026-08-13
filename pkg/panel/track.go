package panel

import "time"

// Sense is the direction a value went, in the caller's terms rather than
// arithmetic ones: which way a number moved is arithmetic, whether that was
// better or worse is a fact about the thing being measured, and only the
// caller holds it.
type Sense int

const (
	// SenseNone is no movement worth marking.
	SenseNone Sense = iota
	// SenseNeutral is a value that moved with no direction worth colouring.
	SenseNeutral
	SenseBetter
	SenseWorse
)

// Directions declare what a climbing number means. An identifier in neither
// set moves without a direction and is marked SenseNeutral.
type Directions struct {
	RisingWorse  []string
	RisingBetter []string
}

// Tracker remembers what the last set of values held, so one that moved can be
// marked for a moment afterwards.
//
// The renderer is a pure function of the values passed to it, so this memory
// lives with whichever caller owns the loop rather than inside the layout.
type Tracker struct {
	hold          time.Duration
	worse, better map[string]bool
	prev          map[string]float64
	moved         map[string]Sense
	at            map[string]time.Time
}

// NewTracker returns a tracker holding each mark for the given duration.
//
// The first set of values it sees marks nothing: there is no earlier value for
// them to have moved from, and lighting the whole table up on the first update
// would say every value had just changed.
func NewTracker(hold time.Duration, d Directions) *Tracker {
	t := &Tracker{
		hold:   hold,
		worse:  make(map[string]bool, len(d.RisingWorse)),
		better: make(map[string]bool, len(d.RisingBetter)),
		moved:  make(map[string]Sense),
		at:     make(map[string]time.Time),
	}
	for _, id := range d.RisingWorse {
		t.worse[id] = true
	}
	for _, id := range d.RisingBetter {
		t.better[id] = true
	}
	return t
}

// Observe records a set of values and marks whatever changed since the last
// set.
//
// An identifier absent from vals is not a change: a measurement that is
// unknown this time should be left out rather than passed as zero, so becoming
// known later reads as a first value and not as a move from nothing.
func (t *Tracker) Observe(vals map[string]float64, now time.Time) {
	if t.prev == nil {
		t.prev = vals
		return
	}
	for id, v := range vals {
		old, had := t.prev[id]
		if !had || old == v {
			continue
		}
		t.moved[id] = t.direction(id, v > old)
		t.at[id] = now
	}
	t.prev = vals
}

// Active returns the marks still inside the hold window.
func (t *Tracker) Active(now time.Time) map[string]Sense {
	if t == nil || len(t.moved) == 0 {
		return nil
	}
	out := make(map[string]Sense, len(t.moved))
	for id, m := range t.moved {
		if now.Sub(t.at[id]) < t.hold {
			out[id] = m
		}
	}
	return out
}

// direction maps a rise or a fall onto what it meant.
func (t *Tracker) direction(id string, rose bool) Sense {
	switch {
	case t.worse[id]:
		if rose {
			return SenseWorse
		}
		return SenseBetter
	case t.better[id]:
		if rose {
			return SenseBetter
		}
		return SenseWorse
	default:
		return SenseNeutral
	}
}

// Decay grades a line by how long ago what it reports happened. A fact that
// just landed is worth the eye; the same fact twenty minutes later is context.
// Emphasis decays rather than the line vanishing, so the row keeps its place
// and its height.
type Decay struct{ Fresh, Recent time.Duration }

// Emphasis is the attribute a line of the given age carries. Colour is the
// only thing that changes: the words are the same at every age.
func (d Decay) Emphasis(age time.Duration, hue string) string {
	switch {
	case age < d.Fresh:
		return hue
	case age < d.Recent:
		return Dim + hue
	default:
		return Dim
	}
}
