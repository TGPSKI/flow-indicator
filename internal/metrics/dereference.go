package metrics

// DereferenceReliability is success over success plus failure. Unknown
// outcomes are excluded from both sides; with no resolved outcome the metric is
// unknown, never 100%.
//
// This is a proxy. It reports whether compact references appear to have
// resolved, judged from what the operator did next.
func DereferenceReliability(success, failure int) Value {
	return ratio(success, success+failure, Unknown())
}
