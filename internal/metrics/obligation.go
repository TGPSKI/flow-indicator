package metrics

// ObligationRepeatRatio is repeated unresolved candidates over unresolved
// candidates. With no candidate there is nothing to repeat, so the ratio is 0
// rather than unknown.
func ObligationRepeatRatio(repeated, unresolved int) Value {
	if unresolved == 0 {
		return KnownValue(0)
	}
	return KnownValue(float64(repeated) / float64(unresolved))
}

// MeanRepeatCount is the average number of times an unresolved candidate has
// been repeated.
func MeanRepeatCount(repeats []int) Value {
	if len(repeats) == 0 {
		return Unknown()
	}
	var sum int
	for _, r := range repeats {
		sum += r
	}
	return KnownValue(float64(sum) / float64(len(repeats)))
}

// MedianCandidateAge is the median age, in operator turns, of unresolved
// obligation candidates. It measures how long the inventory has been carried,
// not how long a requirement has been in force.
func MedianCandidateAge(ages []int) Value { return median(ages) }
