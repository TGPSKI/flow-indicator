package classify

import (
	"slices"
	"sort"
)

// Capability names a fact a classifier is able to establish.
//
// The view needs to tell "measured, and the answer is unknown" from "this build
// cannot measure this". Both render as an absent value, and they are different
// facts about the instrument: the first is a reading, the second is a missing
// instrument. A classifier declares what it can reach, and the declaration
// travels with the measurements so a reader of a stored session can tell the
// two apart without knowing which mode produced it.
//
// A capability is a claim about reachability, never about the answer. Declaring
// CapVerifiedRepair says the classifier can return a verdict on repair, not that
// any particular turn got one.
type Capability string

const (
	// CapVerifiedRepair is the ability to establish that the target the operator
	// named was repaired. The agent asserting it is a claim, not verification,
	// so no marker rule reaches this.
	CapVerifiedRepair Capability = "verified_repair"

	// CapObligationRelease is the ability to establish that the operator
	// withdrew a requirement they stated earlier. Release language is literal
	// text, so a marker classifier reaches it.
	CapObligationRelease Capability = "obligation_release"

	// CapObligationSupersession is the ability to establish that a later
	// requirement replaced an earlier one.
	//
	// This looks like a rule over the obligation surface and is not one. Token
	// overlap cannot separate a replacement from an independent second
	// requirement: "only edit internal/worker" against "only edit internal/api"
	// and "do not touch the release workflow" against "do not touch the config"
	// have the same shape and the same overlap, and only the first pair is a
	// replacement. What separates them is whether the two objects are
	// alternatives within one dimension, which is a semantic judgement. A
	// similarity threshold would drop a live requirement on the second pair, so
	// the marker tier does not have this.
	CapObligationSupersession Capability = "obligation_supersession"

	// CapObligationSatisfaction is the ability to establish that the agent's
	// work satisfied a requirement. This is the same question as verified
	// repair, asked of an obligation, and no marker rule reaches it either.
	CapObligationSatisfaction Capability = "obligation_satisfaction"
)

// Capabilities is a set of capabilities, in declaration order.
type Capabilities []Capability

// Can reports whether the set contains c. A nil set can nothing.
func (cs Capabilities) Can(c Capability) bool { return slices.Contains(cs, c) }

// Strings returns the capability names, sorted, for storage beside a
// measurement.
func (cs Capabilities) Strings() []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, string(c))
	}
	sort.Strings(out)
	return out
}
