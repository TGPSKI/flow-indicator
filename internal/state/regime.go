package state

import (
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
)

// Regime is the interaction state of the stream.
type Regime string

const (
	RegimeFlow     Regime = "FLOW"
	RegimeDrift    Regime = "DRIFT"
	RegimeRecovery Regime = "RECOVERY"
	RegimeThrash   Regime = "THRASH"
	RegimeReset    Regime = "RESET"
)

// Rule constants that the contract fixes rather than exposing as config.
const (
	// thrashRecoveryMultiple: recovery characters at this multiple of the
	// control baseline is thrash on its own.
	thrashRecoveryMultiple = 5
	// thrashForwardShare: the forward-work share at or below which the third
	// thrash rule can fire.
	thrashForwardShare = 0.40
	// driftWindowTurns: the width, in eligible operator turns, of the drift
	// window.
	driftWindowTurns = 8
	// driftEventCount: how many failures or corrections inside that window
	// constitute drift.
	driftEventCount = 2
)

// RegimeInput is everything the regime rules read. Unknown metrics arrive as
// unknown values and never satisfy a threshold.
type RegimeInput struct {
	Reset bool

	RepairOpen  bool
	RepairDepth int
	RepairChars int

	Baseline                      metrics.Value
	SerializationInflation        metrics.Value
	ForwardShare                  metrics.Value
	RepeatedUnresolvedObligations int

	RecentCorrections        int
	RecentInterrupts         int
	RecentDereferenceFailure int
	RecentObligationRepeats  int

	Thresholds config.Thresholds
}

// Decision is one turn's regime and the comparison that decided it.
//
// The rule identifies which comparison fired; the license is that comparison in
// words. Both travel with the snapshot so that every reader — the report, the
// event log, the side-pane meter — states the rule that ran rather than
// reconstructing a plausible one from the metric values.
type Decision struct {
	Regime  Regime
	Rule    metrics.RegimeRule
	License string
}

// Evaluate applies the regime rules in fixed precedence: RESET, THRASH,
// RECOVERY, DRIFT, FLOW. There is no composite score.
func Evaluate(in RegimeInput) Regime { return Decide(in).Regime }

// Decide applies the regime rules once and returns the decision.
func Decide(in RegimeInput) Decision {
	switch {
	case in.Reset:
		return Decision{RegimeReset, metrics.RuleReset,
			"the turn carried a session-reset marker"}
	case in.RepairOpen:
		if d, ok := thrashing(in); ok {
			return d
		}
		return Decision{RegimeRecovery, metrics.RuleRecoveryOpen,
			"a repair episode is open and no thrash rule fired"}
	default:
		if d, ok := drifting(in); ok {
			return d
		}
		return Decision{RegimeFlow, metrics.RuleFlow,
			"no reset, no open episode, and no drift rule fired"}
	}
}

// thrashing returns the thrash decision, or reports that no thrash rule fired.
// An unknown metric satisfies no rule.
func thrashing(in RegimeInput) (Decision, bool) {
	if in.RepairDepth >= in.Thresholds.ThrashRepairDepth {
		return Decision{RegimeThrash, metrics.RuleThrashRepairDepth,
			"an episode is open at depth at or over thresholds.thrash_repair_depth"}, true
	}
	if in.Baseline.Known && float64(in.RepairChars) >= thrashRecoveryMultiple*in.Baseline.Num {
		return Decision{RegimeThrash, metrics.RuleThrashRecoveryChars,
			"an episode is open whose recovery characters are at or over five times the control baseline"}, true
	}
	if in.RepeatedUnresolvedObligations >= in.Thresholds.RepeatedObligationsWarn &&
		in.SerializationInflation.AtLeast(in.Thresholds.SerializationWarn) &&
		in.ForwardShare.AtMost(thrashForwardShare) {
		return Decision{RegimeThrash, metrics.RuleThrashObligationInflation,
			"an episode is open with repeated unresolved candidates, high inflation and a low forward-work share"}, true
	}
	return Decision{}, false
}

// drifting returns the drift decision, or reports that no drift rule fired.
func drifting(in RegimeInput) (Decision, bool) {
	if in.RecentDereferenceFailure >= driftEventCount {
		return Decision{RegimeDrift, metrics.RuleDriftDereference,
			"two or more compact references missed inside the drift window"}, true
	}
	// Corrections and interruptions are the same kind of fact for this rule:
	// the operator spending a turn on the agent's behaviour instead of on the
	// work. Counting them together is what lets a stretch the operator steered
	// by hitting escape register at all; neither alone is drift.
	if in.RecentCorrections+in.RecentInterrupts >= driftEventCount {
		return Decision{RegimeDrift, metrics.RuleDriftControlActions,
			"two or more operator control actions inside the drift window"}, true
	}
	// Both halves of this rule are read over the same window. An obligation
	// repeated once used to make the flag true for the rest of the epoch, so
	// every later turn large enough to clear the inflation threshold reported
	// drift on evidence hundreds of turns old: 2b88a8bf drifted 37 times on one
	// repeat at turn 1598, the last of them 3,836 records later on "give me a
	// complete zip file of all runs". The window is the drift window the other
	// two rules already use; no threshold changed.
	if in.SerializationInflation.AtLeast(in.Thresholds.SerializationWarn) && in.RecentObligationRepeats > 0 {
		return Decision{RegimeDrift, metrics.RuleDriftSerializationObligation,
			"serialization inflation at or over the warn threshold with an obligation repeated inside the drift window"}, true
	}
	return Decision{}, false
}
