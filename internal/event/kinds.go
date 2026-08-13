package event

// Event kinds. Each kind has a fixed evidence class and a fixed payload shape;
// both are documented in docs/EVENTS.md.
const (
	// Observed.
	KindRecordObserved = "record_observed"
	// KindObservationStopped records that the observer stopped watching. It is
	// observed: the facts it carries are why watching ended, the byte offset it
	// reached, and the last record read. What was left open is projected state
	// and travels separately, as KindStateAtObservationStop.
	KindObservationStopped = "observation_stopped"
	// KindOperatorInterrupt records that the harness wrote an interruption
	// marker. The operator authored no text: the fact is that they stopped the
	// agent mid-turn, and nothing about why.
	KindOperatorInterrupt = "operator_interrupt"

	// Classified.
	KindSegmentsClassified  = "segments_classified"
	KindCorrectionCandidate = "correction_candidate"
	KindStopCandidate       = "stop_candidate"
	KindResetCandidate      = "reset_candidate"
	KindPointerCandidate    = "pointer_candidate"
	KindObligationCandidate = "obligation_candidate"
	KindNearRepeatCandidate = "near_repeat_candidate"
	KindExpansionCandidate  = "repair_expansion_candidate"
	KindClassifierFailed    = "classifier_failed"
	// KindSemanticCompleted is a deferred local-model interpretation. It is
	// persisted as classified evidence but does not mutate the current state
	// until a deterministic replay selects it.
	KindSemanticCompleted = "semantic_classification_completed"

	// State transitions over obligations.
	KindObligationIntroduced = "obligation_introduced"
	KindObligationRepeated   = "obligation_repeated"
	KindObligationSatisfied  = "obligation_satisfied"
	KindObligationViolated   = "obligation_violated"
	// KindObligationReleased records the operator withdrawing a requirement
	// they stated earlier.
	KindObligationReleased   = "obligation_released"
	KindObligationSuperseded = "obligation_superseded"
	// KindObligationRevived records a resolved candidate being stated again.
	// The resolution did not hold, and the requirement is back in the inventory
	// under the identity it had before.
	KindObligationRevived = "obligation_revived"
	KindObligationExpired = "obligation_expired"

	// State transitions over repair episodes.
	KindRepairOpened           = "repair_opened"
	KindRepairDeepened         = "repair_deepened"
	KindRepairProvisionalClose = "repair_provisionally_closed"
	KindRepairDurableClose     = "repair_durably_closed"
	KindRepairRecurred         = "repair_recurred"
	KindRepairReset            = "repair_reset"
	// KindRepairAbandoned needs evidence that the operator abandoned the
	// episode. No classifier in this build produces that evidence, so nothing
	// emits this kind; an episode that merely ran out of window becomes
	// unknown via KindRepairStatus. The kind stays declared so that a reader of
	// an older log still resolves it.
	KindRepairAbandoned = "repair_abandoned"
	KindRepairStatus    = "repair_status"

	// State transitions over the stream itself.
	KindPointerResolved = "pointer_resolved"
	KindEpochAdvanced   = "epoch_advanced"
	KindRegimeChanged   = "regime_changed"
	// KindTrendEmerged reports one metric crossing between quality bands and
	// staying there. It is a reading of measurements already recorded, not a
	// new measurement, and no regime rule consults it.
	KindTrendEmerged = "trend_emerged"
	// KindStateAtObservationStop is the projector's inventory of state left
	// open when the observer stopped. It is derived, and it settles nothing:
	// each count is a question the source never answered.
	KindStateAtObservationStop = "state_at_observation_stop"

	// Derived.
	KindMetricsComputed = "metrics_computed"
)
