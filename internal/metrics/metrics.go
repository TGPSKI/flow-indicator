// Package metrics computes the six derived families.
//
// Every function here is arithmetic over counts. Nothing in this package reads
// a source record, and nothing decides state. Unknown is a value, not zero: a
// metric with no evidence behind it says so.
package metrics

import (
	"encoding/json"
	"sort"
	"strconv"
)

// Value is a metric that may be unknown. It marshals to null when unknown, so
// a stored metric never claims a number it did not have.
type Value struct {
	Known bool
	Num   float64
}

// Known returns a known value.
func KnownValue(f float64) Value { return Value{Known: true, Num: f} }

// Unknown returns the unknown value.
func Unknown() Value { return Value{} }

// AtLeast reports whether the value is known and meets the threshold. An
// unknown metric never satisfies a threshold.
func (v Value) AtLeast(threshold float64) bool { return v.Known && v.Num >= threshold }

// AtMost reports whether the value is known and is at or below the threshold.
func (v Value) AtMost(threshold float64) bool { return v.Known && v.Num <= threshold }

// String renders the value for display.
func (v Value) String() string {
	if !v.Known {
		return "unknown"
	}
	return strconv.FormatFloat(v.Num, 'f', -1, 64)
}

func (v Value) MarshalJSON() ([]byte, error) {
	if !v.Known {
		return []byte("null"), nil
	}
	return json.Marshal(v.Num)
}

func (v *Value) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*v = Unknown()
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*v = KnownValue(f)
	return nil
}

// ratio divides with an explicit zero-denominator answer.
func ratio(num, den int, zeroDenominator Value) Value {
	if den == 0 {
		return zeroDenominator
	}
	return KnownValue(float64(num) / float64(den))
}

// median returns the median of xs, or unknown for an empty input.
func median(xs []int) Value {
	if len(xs) == 0 {
		return Unknown()
	}
	sorted := append([]int(nil), xs...)
	sort.Ints(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return KnownValue(float64(sorted[mid]))
	}
	return KnownValue(float64(sorted[mid-1]+sorted[mid]) / 2)
}

// Snapshot is the derived state of one operator turn: the payload of a
// metrics_computed event and one row of timeline.csv.
type Snapshot struct {
	// Seq is the ordinal of the record this snapshot was computed from, across
	// every record in the stream. It counts agent and tool records too, so it
	// is not a count of operator turns: use TurnIndex for that.
	Seq uint64 `json:"seq"`
	// TurnIndex is the ordinal of the operator turn, counting operator turns
	// only. It is the number a reader means by "which turn are we on".
	TurnIndex int    `json:"turn_index"`
	Epoch     uint64 `json:"epoch"`
	Regime    string `json:"regime"`

	// RegimeRule is the comparison that decided the regime, and RegimeLicense
	// is that comparison in words. A reader states the rule that ran; nothing
	// downstream re-derives one from the values below.
	RegimeRule    RegimeRule `json:"regime_rule"`
	RegimeLicense string     `json:"regime_license,omitempty"`

	// Family 1, transmission load.
	UserChars         int   `json:"user_chars"`
	BaselineChars     Value `json:"baseline_chars"`
	BaselineTurns     int   `json:"baseline_turns"`
	SerializationInfl Value `json:"serialization_inflation"`
	QuotedFraction    Value `json:"quoted_fraction"`
	RepeatFraction    Value `json:"repeat_fraction"`

	// Family 2, control-plane burden.
	ForwardChars  int   `json:"forward_chars"`
	ControlChars  int   `json:"control_chars"`
	RecoveryChars int   `json:"recovery_chars"`
	RestateChars  int   `json:"restate_chars"`
	OtherChars    int   `json:"other_chars"`
	ForwardShare  Value `json:"forward_work_share"`
	ControlBurden Value `json:"control_plane_burden"`
	RestateShare  Value `json:"restate_burden"`

	// Family 3, obligation surface. The inventory counts candidates that were
	// introduced and not resolved since. It is not a count of requirements known
	// to remain in force: nothing establishes continued force, only that no
	// evidence has removed them.
	//
	// Which resolutions are reachable depends on the classifier, so the counts
	// below are read against Capabilities. A zero under a classifier that cannot
	// establish the kind is not a session in which it never happened.
	UnresolvedObligations int   `json:"unresolved_obligation_candidates"`
	NewObligations        int   `json:"new_obligations"`
	RepeatedObligations   int   `json:"repeated_obligations"`
	ViolatedObligations   int   `json:"violated_obligations"`
	SatisfiedObligation   int   `json:"satisfied_obligations"`
	ReleasedObligations   int   `json:"released_obligations"`
	SupersededObligations int   `json:"superseded_obligations"`
	MeanRepeatCount       Value `json:"mean_repeat_count"`
	MedianCandidateAge    Value `json:"median_candidate_age_turns"`
	RepeatRatio           Value `json:"obligation_repeat_ratio"`

	// Family 4, dereference reliability proxy.
	PointerSuccess int   `json:"pointer_success"`
	PointerFailure int   `json:"pointer_failure"`
	PointerUnknown int   `json:"pointer_unknown"`
	Dereference    Value `json:"dereference_reliability_proxy"`

	// Family 5, recovery telemetry.
	RepairID      string `json:"repair_id,omitempty"`
	RepairDepth   int    `json:"repair_depth"`
	RepairChars   int    `json:"repair_chars"`
	RepairRecords int    `json:"repair_records"`
	RepairSeconds Value  `json:"repair_seconds"`
	RepairMagnify Value  `json:"repair_magnification"`
	RepairStatus  string `json:"repair_status,omitempty"`

	// Family 6, repair pollution.
	Expansions      int    `json:"repair_expansion_count"`
	PollutionStatus string `json:"repair_pollution_status,omitempty"`

	// Capabilities names the facts the configured classifier can establish. It
	// travels with every snapshot because it is what separates "measured, and
	// the answer is unknown" from "this build cannot measure this", and a stored
	// session read back later has no other way to tell them apart.
	Capabilities []string `json:"classifier_capabilities,omitempty"`

	// Drift window. The events inside the current window, so a reader sees the
	// counts the drift rules compared rather than re-deriving them.
	// DriftWindowTurns is the width the counts are taken over.
	RecentCorrections       int `json:"recent_corrections"`
	RecentInterrupts        int `json:"recent_interrupts"`
	RecentDereferenceMisses int `json:"recent_dereference_misses"`
	DriftWindowTurns        int `json:"drift_window_turns"`
}
