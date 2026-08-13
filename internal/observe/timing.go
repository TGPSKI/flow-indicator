package observe

import (
	"time"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

type lastSeen struct {
	record time.Time
	human  time.Time
}

// gaps returns seconds since the previous record and since the previous
// operator turn. Either is nil when a timestamp is missing or moves backwards:
// a stream with unusable timestamps yields unknown timing, not invented timing.
func (o *Observer) gaps(r stream.Record) (prev, prevHuman *float64) {
	if !r.Timestamp.IsZero() {
		prev = elapsed(o.last.record, r.Timestamp)
		prevHuman = elapsed(o.last.human, r.Timestamp)
	}
	if !r.Timestamp.IsZero() {
		o.last.record = r.Timestamp
		if r.IsOperatorTurn() {
			o.last.human = r.Timestamp
		}
	}
	return prev, prevHuman
}

func elapsed(from, to time.Time) *float64 {
	if from.IsZero() || to.Before(from) {
		return nil
	}
	secs := to.Sub(from).Seconds()
	return &secs
}
