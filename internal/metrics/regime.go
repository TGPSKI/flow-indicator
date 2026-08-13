package metrics

// RegimeRule identifies the comparison that decided a turn's regime.
//
// The identifier lives with the snapshot rather than with the rules because it
// is a derived value that travels: the projector writes it, the report and the
// side-pane meter read it. A renderer that re-derives a likely reason from the
// metric values can name a comparison that never fired, which is what these
// identifiers exist to prevent.
//
// The rule is the identity of the comparison. The license is its sentence.
type RegimeRule string

const (
	// RuleUnknown is a snapshot written before rule identity was recorded, or
	// by a path that did not evaluate the rules.
	RuleUnknown RegimeRule = ""

	RuleFlow  RegimeRule = "flow"
	RuleReset RegimeRule = "reset"

	// RuleRecoveryOpen is an open episode with no thrash rule satisfied.
	RuleRecoveryOpen RegimeRule = "recovery_open"

	RuleThrashRepairDepth         RegimeRule = "thrash_repair_depth"
	RuleThrashRecoveryChars       RegimeRule = "thrash_recovery_chars"
	RuleThrashObligationInflation RegimeRule = "thrash_obligation_inflation"

	RuleDriftDereference             RegimeRule = "drift_dereference"
	RuleDriftControlActions          RegimeRule = "drift_control_actions"
	RuleDriftSerializationObligation RegimeRule = "drift_serialization_obligation"
)
