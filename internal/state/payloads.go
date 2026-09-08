package state

import (
	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
	"github.com/TGPSKI/flow-indicator/internal/observe"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Event payloads. One struct per kind, documented in docs/EVENTS.md. Payload
// shapes are part of the stored format: add fields, do not repurpose them.

type observedPayload struct {
	TurnID       string              `json:"turn_id"`
	Speaker      string              `json:"speaker"`
	SpeakerClass stream.SpeakerClass `json:"speaker_class"`
	Timestamp    string              `json:"timestamp,omitempty"`
	Observation  observe.Observation `json:"observation"`
	Snippet      string              `json:"snippet,omitempty"`
}

// interruptPayload carries an interruption marker. The record has no operator
// text to quote, so the turn index it landed on is the whole of the evidence.
type interruptPayload struct {
	TurnIndex int    `json:"turn_index"`
	License   string `json:"license"`
}

type failurePayload struct {
	Classifier string `json:"classifier"`
	Version    string `json:"classifier_version"`
	Error      string `json:"error"`
}

// SemanticProjectionUpdate carries a complete source-ordered projection after
// one or more late semantic results became available. Its events are derived
// from the source records and the named completion results; the append-only
// log keeps both the initial marker projection and every later update.
type SemanticProjectionUpdate struct {
	Events []event.Event `json:"events"`
}

type segmentsPayload struct {
	Provenance classify.Provenance `json:"provenance"`
	Segments   []classify.Segment  `json:"segments"`
	Forward    int                 `json:"forward_chars"`
	Control    int                 `json:"control_chars"`
	Recovery   int                 `json:"recovery_chars"`
	Restate    int                 `json:"restate_chars"`
	Other      int                 `json:"other_chars"`
}

type correctionPayload struct {
	Provenance classify.Provenance `json:"provenance"`
	Correction classify.Correction `json:"correction"`
	TriggerSeq uint64              `json:"trigger_agent_turn"`
}

type markerPayload struct {
	Provenance classify.Provenance `json:"provenance"`
	Marker     string              `json:"marker"`
}

type pointerPayload struct {
	Provenance classify.Provenance `json:"provenance"`
	Pointer    classify.Pointer    `json:"pointer"`
}

type obligationCandidatePayload struct {
	Provenance classify.Provenance          `json:"provenance"`
	Candidate  classify.ObligationCandidate `json:"candidate"`
}

type nearRepeatPayload struct {
	Provenance classify.Provenance `json:"provenance"`
	NearRepeat classify.NearRepeat `json:"near_repeat"`
}

type expansionPayload struct {
	Provenance classify.Provenance   `json:"provenance"`
	Repair     classify.RepairSignal `json:"repair"`
	RepairID   string                `json:"repair_id,omitempty"`
}

type obligationPayload struct {
	Obligation *Obligation `json:"obligation"`
	License    string      `json:"license,omitempty"`
}

// obligationRepeatPayload records a repeat and whether it happened inside a
// correction turn. Both are observations. Neither on its own says the
// obligation was violated: that needs the correction to be about this
// obligation.
type obligationRepeatPayload struct {
	Obligation       *Obligation `json:"obligation"`
	DuringCorrection bool        `json:"during_correction"`
	License          string      `json:"license,omitempty"`
}

// obligationResolvedPayload records a candidate leaving the inventory.
//
// Resolution is the one obligation transition that removes something an
// operator asked for, so it carries more than the others: the kind of evidence,
// the classifier that established it, and that classifier's own words. A reader
// auditing a requirement that stopped being counted needs all three without
// re-deriving the stream.
type obligationResolvedPayload struct {
	Obligation *Obligation         `json:"obligation"`
	Kind       string              `json:"resolution_kind"`
	Provenance classify.Provenance `json:"provenance"`
	License    string              `json:"license,omitempty"`
}

// obligationRevivedPayload records a resolved candidate being stated again.
// ResolvedTurn is kept on the obligation, so the payload carries the status the
// candidate is coming back from.
type obligationRevivedPayload struct {
	Obligation *Obligation `json:"obligation"`
	From       string      `json:"from_status"`
	License    string      `json:"license,omitempty"`
}

// Transition licenses. A derived transition needs premises, and a premise that
// is weaker than the conclusion is the defect this vocabulary exists to expose.
// The license is stored with the transition so a reader can audit what permitted
// exactly this conclusion without re-deriving the stream. It is prose, not a
// flag: there is nothing here for a program to trust.
const (
	licenseOpened = "a correction candidate arrived with no episode open"
	// licenseStructural is the recovery rule that reads actions rather than
	// words. It is stated as the comparison it made, because that comparison is
	// the whole of the premise: no marker was consulted and no threshold applied.
	licenseStructural  = "the response cycle following an operator turn wrote to a path the immediately preceding cycle also wrote"
	licenseDeepened    = "the correction's established target key equals the open episode's established target key"
	licenseRecurred    = "the correction's established target key equals the target key of an episode awaiting durability"
	licenseProvisional = "after the agent answered, the operator accepted or moved the work forward"
	licenseDurable     = "the durability window passed with no correction naming this episode's target key"
	licenseReset       = "the turn carried a session-reset marker while the episode was open"

	licenseObligationIntroduced = "a sentence stating a requirement whose normalized key was not already in the inventory; this records the statement, not that the requirement is in force"
	licenseObligationRepeated   = "a sentence whose normalized key equals a candidate already in the inventory"
	licenseObligationViolated   = "the correction's identified target key equals this candidate's key, and the turn repeated it"
	licenseObligationRevived    = "the operator stated this candidate again after it had been resolved; the resolution did not hold"
	licenseEpochAdvanced        = "the turn carried a session-reset marker naming a transition being performed"

	licenseOperatorInterrupt = "the harness wrote an interruption marker: the operator stopped the agent mid-turn"
)

type repairPayload struct {
	Repair *Repair `json:"repair"`
	// License names the observed and classified facts that permit this exact
	// transition. Coexistence in time is not among them.
	License string `json:"license,omitempty"`
}

// repairStatusPayload records a status an episode reached without a transition
// of its own, and why. It carries the whole episode for the same reason the
// transition payloads do: a reader folds the log forward, and an episode whose
// status changed only here would otherwise still project as open.
type repairStatusPayload struct {
	RepairID string  `json:"repair_id"`
	Status   string  `json:"status"`
	Reason   string  `json:"reason"`
	Repair   *Repair `json:"repair"`
}

type pollutionPayload struct {
	RepairID  string `json:"repair_id"`
	AgentTurn uint64 `json:"agent_turn"`
	// CycleRecords is every agent and tool record the cycle covered, from the
	// correction to the record before the next operator turn.
	CycleRecords int `json:"cycle_records"`
	// ClaimedRepaired reports that the agent said it repaired the target.
	ClaimedRepaired bool `json:"claimed_repaired"`
	// TargetRepaired is verified repair, null when nothing established it.
	TargetRepaired      *bool `json:"target_repaired"`
	Expansions          int   `json:"repair_expansion_count"`
	ImmediateCorrection bool  `json:"immediate_correction"`
	// CycleWrites is how many write actions the cycle carried. With zero, an
	// unknown assessment means no evidence rather than a negative reading.
	CycleWrites int `json:"cycle_writes"`
	// CycleInterrupts and CycleFailedActions are observed facts about the cycle,
	// carried so they can be scored as recovery discriminators. No rule reads
	// them.
	CycleInterrupts    int `json:"cycle_interrupts"`
	CycleFailedActions int `json:"cycle_failed_actions"`
	// Status is the pollution status of the cycle. It is spelled out rather
	// than called "status" because an episode's status shares this event kind,
	// and a reader keying on the shorter name would count an episode whose
	// outcome was never established as a cycle assessed as unknown.
	Status string `json:"pollution_status"`
	// Evidence names what the assessment rests on.
	Evidence string `json:"evidence,omitempty"`
}

// observationStoppedPayload records that the observer stopped. Every field is
// directly established: why watching ended, the byte it reached, and the
// ordinal of the last record read. Nothing here is projected state, and nothing
// here concludes anything about the interaction.
type observationStoppedPayload struct {
	Reason     string `json:"reason"`
	Offset     int64  `json:"source_offset"`
	LastRecord uint64 `json:"last_record"`
}

// stateAtObservationStopPayload is the projector's inventory of what the
// observer left open. Every field is derived: these are counts of projected
// state, not facts read out of the source, and each is an open question rather
// than an outcome.
type stateAtObservationStopPayload struct {
	Epoch                uint64 `json:"epoch"`
	OpenRepair           string `json:"open_repair,omitempty"`
	PendingPointers      int    `json:"pending_pointers"`
	AwaitingDurability   int    `json:"repairs_awaiting_durability"`
	UnresolvedCandidates int    `json:"unresolved_obligation_candidates"`
}

type pointerOutcomePayload struct {
	PointerTurn uint64 `json:"pointer_turn"`
	Type        string `json:"pointer_type"`
	Outcome     string `json:"outcome"`
	Evidence    string `json:"evidence"`
}

type epochPayload struct {
	From                     uint64   `json:"from"`
	To                       uint64   `json:"to"`
	Trigger                  uint64   `json:"trigger_turn"`
	ObligationsMarkedUnknown []string `json:"obligations_marked_unknown"`
	License                  string   `json:"license,omitempty"`
}

// regimePayload carries the rule that decided the new regime. The rules are a
// fixed precedence with no composite score, so naming the one that fired is the
// whole premise.
type regimePayload struct {
	From    string             `json:"from"`
	To      string             `json:"to"`
	Rule    metrics.RegimeRule `json:"rule"`
	License string             `json:"license,omitempty"`
}

// classifiedEvents emits one event per classified fact. Emission order is
// fixed so replay produces byte-identical files.
func (p *Projector) classifiedEvents(b *event.Builder, rec stream.Record, res classify.Result) []event.Event {
	var out []event.Event

	if len(res.Segments) > 0 {
		buckets := bucketsOf(res)
		out = append(out, b.Emit(event.KindSegmentsClassified, event.ClassClassified, segmentsPayload{
			Provenance: res.Provenance,
			Segments:   res.Segments,
			Forward:    buckets.Forward,
			Control:    buckets.Control,
			Recovery:   buckets.Recovery,
			Restate:    buckets.Restate,
			Other:      buckets.Other,
		}))
	}
	if res.Correction.IsCorrection {
		out = append(out, b.Emit(event.KindCorrectionCandidate, event.ClassClassified, correctionPayload{
			Provenance: res.Provenance,
			Correction: res.Correction,
			TriggerSeq: p.lastAgentSeq,
		}))
	}
	if res.Stop {
		out = append(out, b.Emit(event.KindStopCandidate, event.ClassClassified, markerPayload{
			Provenance: res.Provenance, Marker: "stop",
		}))
	}
	if res.Reset {
		out = append(out, b.Emit(event.KindResetCandidate, event.ClassClassified, markerPayload{
			Provenance: res.Provenance, Marker: "reset",
		}))
	}
	if res.Pointer.IsPointer {
		out = append(out, b.Emit(event.KindPointerCandidate, event.ClassClassified, pointerPayload{
			Provenance: res.Provenance, Pointer: res.Pointer,
		}))
	}
	for _, c := range res.Obligations {
		out = append(out, b.Emit(event.KindObligationCandidate, event.ClassClassified, obligationCandidatePayload{
			Provenance: res.Provenance, Candidate: c,
		}))
	}
	for _, n := range res.NearRepeats {
		out = append(out, b.Emit(event.KindNearRepeatCandidate, event.ClassClassified, nearRepeatPayload{
			Provenance: res.Provenance, NearRepeat: n,
		}))
	}
	if rec.SpeakerClass == stream.SpeakerAgent && (res.Repair.Expansions() > 0 || res.Repair.ClaimedRepaired || res.Repair.TargetRepaired != nil) {
		payload := expansionPayload{Provenance: res.Provenance, Repair: res.Repair}
		if p.active != nil {
			payload.RepairID = p.active.ID
		}
		out = append(out, b.Emit(event.KindExpansionCandidate, event.ClassClassified, payload))
	}
	return out
}

// trendPayload records one metric crossing between quality bands and holding
// there. The bands are named, not numbered, so a reader of the log does not
// have to know this build's boundaries to know what was reported.
type trendPayload struct {
	Metric    string `json:"metric"`
	From      string `json:"from_band"`
	To        string `json:"to_band"`
	Direction string `json:"direction"`
	Turns     int    `json:"turns_held"`
	TurnIndex int    `json:"turn_index"`
	// License is the comparison that reported the crossing, in words. A reader
	// states the rule that ran rather than re-deriving one from the bands.
	License string `json:"license"`
}
