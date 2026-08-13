package metrics

// BaselineChars is the median size of recent eligible control turns: turns that
// neither corrected the agent nor fell inside an open repair episode. They are
// not known to have worked; they are the turns not visibly spent on repair. It
// is unknown until minBaseline of them exist, because an invented baseline would
// make every inflation figure a guess.
func BaselineChars(recent []int, minBaseline int) Value {
	if len(recent) < minBaseline {
		return Unknown()
	}
	return median(recent)
}

// SerializationInflation is current turn size over the control baseline.
func SerializationInflation(currentChars int, baseline Value) Value {
	if !baseline.Known || baseline.Num == 0 {
		return Unknown()
	}
	return KnownValue(float64(currentChars) / baseline.Num)
}
