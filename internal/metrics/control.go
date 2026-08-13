package metrics

// Buckets are the labelled character totals of a window of operator turns.
type Buckets struct {
	Forward  int
	Control  int
	Recovery int
	Restate  int
	Other    int
}

// Total is every classified operator character in the window, including the
// unlabelled remainder.
func (b Buckets) Total() int {
	return b.Forward + b.Control + b.Recovery + b.Restate + b.Other
}

// ForwardWorkShare is forward chars over all operator chars. Unlabelled text
// stays in the denominator: it is not forward work.
func (b Buckets) ForwardWorkShare() Value {
	return ratio(b.Forward, b.Total(), Unknown())
}

// ControlPlaneBurden is the share of operator characters spent steering,
// recovering, or restating what was already established.
func (b Buckets) ControlPlaneBurden() Value {
	return ratio(b.Control+b.Recovery+b.Restate, b.Total(), Unknown())
}

// RestateBurden is the share spent restating prior state.
func (b Buckets) RestateBurden() Value {
	return ratio(b.Restate, b.Total(), Unknown())
}
