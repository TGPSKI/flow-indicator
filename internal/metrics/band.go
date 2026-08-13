package metrics

// Band is a coarse quality level for one measurement.
//
// A band is not a new measurement. It is a reading of one that already exists,
// coarse enough that crossing between bands is worth reporting and fine enough
// that most turns do not cross. Nothing decides a regime from a band: the
// regime rules read the values themselves.
type Band int

const (
	// BandUnknown is the band of a measurement that does not exist yet. It is
	// never reported as a transition: becoming measurable is not a change in
	// quality.
	BandUnknown Band = iota
	BandGood
	BandMid
	BandBad
)

func (b Band) String() string {
	switch b {
	case BandGood:
		return "good"
	case BandMid:
		return "mid"
	case BandBad:
		return "bad"
	default:
		return "unknown"
	}
}

// Boundaries for the bands that are not already fixed by a configured
// threshold. They are judgement calls, recorded in docs/METRICS.md, and they
// decide only what gets reported as a crossing.
const (
	ControlBurdenGood = 0.25
	ControlBurdenMid  = 0.50

	ForwardShareGood = 0.60
	ForwardShareMid  = 0.35

	DereferenceGood = 0.80
	DereferenceMid  = 0.50
)

// BandSerialization reads serialization inflation against the configured warn
// and high thresholds, so the band and the regime rules agree on where the
// lines are.
func BandSerialization(v Value, warn, high float64) Band {
	if !v.Known {
		return BandUnknown
	}
	switch {
	case high > 0 && v.Num >= high:
		return BandBad
	case warn > 0 && v.Num >= warn:
		return BandMid
	default:
		return BandGood
	}
}

// BandControlBurden reads the share of operator characters spent steering.
// More is worse.
func BandControlBurden(v Value) Band {
	return bandDescending(v, ControlBurdenGood, ControlBurdenMid)
}

// BandForwardShare reads the share of operator characters that advanced the
// work. More is better.
func BandForwardShare(v Value) Band {
	return bandAscending(v, ForwardShareGood, ForwardShareMid)
}

// BandDereference reads the dereference reliability proxy. More is better.
func BandDereference(v Value) Band {
	return bandAscending(v, DereferenceGood, DereferenceMid)
}

// BandRepairDepth reads the open repair depth against the configured thrash
// threshold. Any open episode is already past good.
func BandRepairDepth(depth, thrash int) Band {
	switch {
	case thrash > 0 && depth >= thrash:
		return BandBad
	case depth > 0:
		return BandMid
	default:
		return BandGood
	}
}

// bandAscending bands a value where a larger number is better.
func bandAscending(v Value, good, mid float64) Band {
	if !v.Known {
		return BandUnknown
	}
	switch {
	case v.Num >= good:
		return BandGood
	case v.Num >= mid:
		return BandMid
	default:
		return BandBad
	}
}

// bandDescending bands a value where a smaller number is better.
func bandDescending(v Value, good, mid float64) Band {
	if !v.Known {
		return BandUnknown
	}
	switch {
	case v.Num <= good:
		return BandGood
	case v.Num <= mid:
		return BandMid
	default:
		return BandBad
	}
}
