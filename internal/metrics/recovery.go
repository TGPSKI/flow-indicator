package metrics

import "time"

// RepairMagnification is the operator characters spent recovering over the
// characters of the pointer that triggered the episode. It is unknown when the
// trigger did not begin with a pointer.
func RepairMagnification(recoveryChars, pointerChars int) Value {
	if pointerChars <= 0 {
		return Unknown()
	}
	return KnownValue(float64(recoveryChars) / float64(pointerChars))
}

// RecoveryDuration is the wall-clock span of an episode. It is unknown when
// either endpoint has no timestamp.
func RecoveryDuration(start, end time.Time) Value {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return Unknown()
	}
	return KnownValue(end.Sub(start).Seconds())
}
