package metrics

// Repair pollution statuses.
const (
	PollutionClean    = "clean"
	PollutionPolluted = "polluted"
	PollutionFailed   = "failed"
	PollutionUnknown  = "unknown"
)

// PollutionStatus classifies the agent response to a correction.
//
// targetRepaired is nil when no evidence establishes it, and the answer is then
// unknown. A row is never filled in to avoid an empty cell.
func PollutionStatus(targetRepaired *bool, expansions int, immediateCorrection bool) string {
	status, _ := PollutionAssessment(Cycle{
		TargetRepaired:      targetRepaired,
		Expansions:          expansions,
		ImmediateCorrection: immediateCorrection,
	})
	return status
}

// Cycle is what a pollution assessment is made from: the verdict on repair, the
// expansion count, whether the operator corrected again immediately, and how
// much evidence there was to read.
type Cycle struct {
	TargetRepaired      *bool
	Expansions          int
	ImmediateCorrection bool
	// Writes is the number of write actions the cycle carried, and Named
	// reports that the correction named at least one path.
	//
	// The two separate the reasons an assessment can come back unknown. A cycle
	// where the correction named no path had no target to check against; one
	// where no write was observed had no evidence; one with both, whose writes
	// missed the target, was measured. All three render as an absent value and
	// they are different facts about the instrument.
	Writes int
	Named  bool
}

// PollutionAssessment returns the status and the premise behind it, in words,
// so the assessment travels with what it rests on.
func PollutionAssessment(c Cycle) (status, premise string) {
	targetRepaired, expansions, immediateCorrection := c.TargetRepaired, c.Expansions, c.ImmediateCorrection
	switch {
	case targetRepaired == nil && !c.Named:
		return PollutionUnknown, "the correction named no path, so there was no target to check a write against; the agent's own claim is not verification"
	case targetRepaired == nil && c.Writes == 0:
		return PollutionUnknown, "the correction named a path and the response cycle carried no observed write, so nothing established whether the target was repaired"
	case targetRepaired == nil:
		return PollutionUnknown, "nothing established whether the target was repaired; the agent's own claim is not verification"
	case !*targetRepaired:
		return PollutionFailed, "the response was established not to have repaired the target"
	case expansions > 0:
		return PollutionPolluted, "the response was established as repairing the target and announced work nobody asked for"
	case !immediateCorrection:
		return PollutionClean, "the response was established as repairing the target, announced no extra work, and drew no correction"
	default:
		// Repaired, no expansion, and corrected anyway: the evidence does not
		// support any of the three named outcomes.
		return PollutionUnknown, "the response was established as repairing the target and announced no extra work, yet the operator corrected it anyway; no named outcome fits"
	}
}
