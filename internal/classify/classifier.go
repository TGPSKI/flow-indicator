// Package classify turns a record's text into labelled candidates.
//
// Everything this package produces is CLASSIFIED evidence: an interpretation
// by a named, versioned classifier. Candidates are not truth. State transitions
// are decided in internal/state, never here.
package classify

import (
	"context"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// SegmentLabel is the label of one contiguous span of operator text.
type SegmentLabel string

const (
	LabelForwardWork SegmentLabel = "forward_work"
	LabelNewTask     SegmentLabel = "new_task"
	LabelNewEvidence SegmentLabel = "new_evidence"

	LabelCorrection         SegmentLabel = "correction"
	LabelScopeConstraint    SegmentLabel = "scope_constraint"
	LabelNegativeConstraint SegmentLabel = "negative_constraint"
	LabelPositiveConstraint SegmentLabel = "positive_constraint"
	LabelStopCondition      SegmentLabel = "stop_condition"
	LabelMetaProcess        SegmentLabel = "meta_process"
	LabelHandoff            SegmentLabel = "handoff"

	LabelReferentDisambiguation  SegmentLabel = "referent_disambiguation"
	LabelTemporalDisambiguation  SegmentLabel = "temporal_disambiguation"
	LabelNamespaceDisambiguation SegmentLabel = "namespace_disambiguation"
	LabelRestartReconstruction   SegmentLabel = "restart_reconstruction"
	// LabelHandoffAfterFailure is a handoff issued while a repair episode is
	// open. The bucket of a label must be a pure function of the label, so the
	// failure-triggered case carries its own label.
	LabelHandoffAfterFailure SegmentLabel = "handoff_after_failure"

	LabelRestatedPriorState SegmentLabel = "restated_prior_state"

	LabelOther SegmentLabel = "other"
)

// Bucket is the aggregate a label contributes to.
type Bucket string

const (
	BucketForward  Bucket = "FORWARD"
	BucketControl  Bucket = "CONTROL"
	BucketRecovery Bucket = "RECOVERY"
	BucketRestate  Bucket = "RESTATE"
	BucketOther    Bucket = "OTHER"
)

// Bucket maps a label to its aggregate. Unlabelled text is OTHER and is never
// counted as forward work.
func (l SegmentLabel) Bucket() Bucket {
	switch l {
	case LabelForwardWork, LabelNewTask, LabelNewEvidence:
		return BucketForward
	case LabelScopeConstraint, LabelNegativeConstraint, LabelPositiveConstraint,
		LabelStopCondition, LabelMetaProcess, LabelHandoff:
		return BucketControl
	case LabelCorrection, LabelReferentDisambiguation, LabelTemporalDisambiguation,
		LabelNamespaceDisambiguation, LabelRestartReconstruction, LabelHandoffAfterFailure:
		return BucketRecovery
	case LabelRestatedPriorState:
		return BucketRestate
	default:
		return BucketOther
	}
}

// Segment is one labelled span. Start and End are byte offsets into the
// record's original text.
type Segment struct {
	Label SegmentLabel `json:"label"`
	Start int          `json:"start"`
	End   int          `json:"end"`
	Chars int          `json:"chars"`
}

// PointerType names the kind of compact reference an operator used.
type PointerType string

const (
	PointerNode      PointerType = "node"
	PointerAlias     PointerType = "alias"
	PointerFile      PointerType = "file"
	PointerPath      PointerType = "path"
	PointerTask      PointerType = "task"
	PointerTemporal  PointerType = "temporal"
	PointerQuote     PointerType = "quote"
	PointerNamespace PointerType = "namespace"
	PointerRelation  PointerType = "relation"
	PointerOperation PointerType = "operation"
	PointerUnknown   PointerType = "unknown"
)

// Pointer is the compact reference found in a turn, if any.
type Pointer struct {
	IsPointer bool        `json:"is_pointer"`
	Type      PointerType `json:"type"`
	Chars     int         `json:"chars"`
	Text      string      `json:"text,omitempty"`
}

// Correction is the turn's correction candidacy.
type Correction struct {
	IsCorrection bool   `json:"is_correction"`
	TargetType   string `json:"target_type"`
	TargetKey    string `json:"target_key,omitempty"`
	Marker       string `json:"marker,omitempty"`
	// TargetPaths are the paths the correction's text named, in first-seen
	// order. They are what verified repair is decided against: a write to one of
	// them establishes the repair, and a write outside them is expansion.
	//
	// A correction that names no path has no target here, and pollution stays
	// unknown. Substituting a guess for a missing target is the defect the whole
	// vocabulary exists to catch.
	TargetPaths []string `json:"target_paths,omitempty"`
	// Structural reports that the correction was established from what the agent
	// did rather than from what the operator's words matched. Both are recorded
	// so each can be scored alone.
	Structural bool `json:"structural,omitempty"`
}

// ObligationCandidate is an explicit operator requirement found in a turn.
type ObligationCandidate struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	Text string `json:"text"`
	// RepeatKey is the sentence's content tokens, sorted. Repeat detection
	// compares this rather than Key, because an operator restating a requirement
	// rewords it and Key is byte equality after normalization. Identity in the
	// inventory stays on Key: a requirement is the sentence the operator wrote.
	RepeatKey string `json:"repeat_key,omitempty"`
	// Directive is the structural evidence about whether this sentence places a
	// requirement on the agent or describes something. It travels with the
	// candidate so each discriminator can be scored against labels on its own.
	Directive Directive `json:"directive"`
	Start     int       `json:"start"`
	End       int       `json:"end"`
}

// Obligation kinds.
const (
	ObligationStop     = "stop"
	ObligationScope    = "scope"
	ObligationNegative = "negative"
	ObligationPositive = "positive"
)

// Obligation resolution kinds. Each names the evidence that resolved a
// candidate, because the three are not interchangeable: a released requirement
// was withdrawn by the operator, a superseded one was replaced, and a satisfied
// one was met by the agent's work.
const (
	// ResolutionReleased is the operator stating the requirement no longer
	// applies. It is directly observed and needs no inference about the agent.
	ResolutionReleased = "released"
	// ResolutionSuperseded is a later requirement replacing an earlier one.
	ResolutionSuperseded = "superseded"
	// ResolutionSatisfied is the agent's work meeting the requirement. It needs
	// verification, never the agent's own account of what it did, and no marker
	// rule establishes it.
	ResolutionSatisfied = "satisfied"
)

// ObligationResolution is classified evidence that an obligation candidate no
// longer stands. It names the candidate by the same normalized key the
// inventory holds, so the projector applies it without matching text again.
//
// Evidence is the premise in words. A resolution with nothing behind it is the
// defect this field exists to expose, and the projector stores it as the
// transition's license.
type ObligationResolution struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Evidence string `json:"evidence"`
}

// NearRepeat is a candidate repeat that is not byte-equal after normalization.
// Only a semantic classifier may promote it to a repeat.
type NearRepeat struct {
	Key      string  `json:"key"`
	PriorKey string  `json:"prior_key"`
	Jaccard  float64 `json:"jaccard"`
	Text     string  `json:"text"`
}

// RepairSignal describes an agent turn that follows a correction.
//
// ClaimedRepaired and TargetRepaired are different facts. "Removed it." is a
// claim by the agent about its own work; it is not verification that the target
// the operator named was repaired. TargetRepaired is nil unless something
// establishes the repair, and the heuristic classifier never establishes it.
type RepairSignal struct {
	// ClaimedRepaired reports that the agent's text asserts the repair.
	ClaimedRepaired bool `json:"claimed_repaired"`
	// TargetRepaired is verified repair, nil when nothing establishes it.
	TargetRepaired *bool `json:"target_repaired"`
	NewScope       int   `json:"new_scope"`
	NewTasks       int   `json:"new_tasks"`
	NewValidation  int   `json:"new_validation"`
	NewConstraints int   `json:"new_constraints"`
}

// Expansions is the repair expansion count, REC.
func (r RepairSignal) Expansions() int {
	return r.NewScope + r.NewTasks + r.NewValidation + r.NewConstraints
}

// Provenance identifies the classifier behind a result.
type Provenance struct {
	Classifier string  `json:"classifier"`
	Version    string  `json:"classifier_version"`
	Hash       string  `json:"classifier_hash"`
	SourceTurn uint64  `json:"source_turn"`
	Confidence float64 `json:"confidence"`
	// Markers names the classifier that supplied marker-level facts (reset,
	// stop, acceptance) when a semantic classifier supplied the rest.
	Markers string `json:"marker_classifier,omitempty"`
}

// Result is one classification of one record.
type Result struct {
	Segments    []Segment             `json:"segments"`
	Pointer     Pointer               `json:"pointer"`
	Correction  Correction            `json:"correction"`
	Obligations []ObligationCandidate `json:"obligations"`
	// Rejected are sentences that carried a requirement marker and were read as
	// descriptions rather than directives. They never reach the inventory.
	//
	// They are carried because a rule that drops evidence silently cannot be
	// scored: calibration needs the units the build considered, not only the
	// ones it kept, or every dropped sentence is invisible to recall. Each
	// carries its structural evidence, so each discriminator can be scored on
	// its own before any are combined.
	Rejected []ObligationCandidate `json:"rejected,omitempty"`
	// Resolutions are candidates the turn established as no longer standing.
	// A classifier that cannot establish resolution returns none, which is not
	// the same as establishing that everything still stands.
	Resolutions []ObligationResolution `json:"resolutions,omitempty"`
	NearRepeats []NearRepeat           `json:"near_repeats,omitempty"`
	Repair      RepairSignal           `json:"repair"`

	// Marker-level facts. These are literal matches, not inferences.
	Reset      bool `json:"reset"`
	Stop       bool `json:"stop"`
	Acceptance bool `json:"acceptance"`
	// Continuation is permission to keep going. It is not acceptance: it says
	// nothing about whether what came before was right.
	Continuation bool `json:"continuation"`

	Confidence float64    `json:"confidence"`
	Provenance Provenance `json:"provenance"`
}

// CharsIn totals the characters labelled into one bucket.
func (r Result) CharsIn(b Bucket) int {
	var n int
	for _, s := range r.Segments {
		if s.Label.Bucket() == b {
			n += s.Chars
		}
	}
	return n
}

// ObligationRef is the projector's view of an unresolved obligation candidate,
// passed back to the classifier as context. It is a copy, not the state itself:
// classifiers never mutate state.
type ObligationRef struct {
	ID string `json:"-"`
	// Key is the candidate's identity. RepeatKey is what a reworded restatement
	// of the same requirement is recognized by.
	Key       string `json:"key"`
	RepeatKey string `json:"repeat_key,omitempty"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
}

// RepairRef is the projector's view of the open repair episode.
type RepairRef struct {
	ID     string `json:"id"`
	Depth  int    `json:"depth"`
	Status string `json:"status"`
	// TargetKey names what the correction was about, for recurrence comparison.
	// TargetPaths are the paths it named, which is what a write can be checked
	// against.
	TargetKey   string   `json:"target_key"`
	TargetPaths []string `json:"target_paths"`
	TriggerSeq  uint64   `json:"trigger_seq"`
}

// Input is everything a classifier may see about one record.
type Input struct {
	Turn                  stream.Record   `json:"turn"`
	Prior                 []stream.Record `json:"prior"`
	PriorOperatorText     []string        `json:"prior_operator_text"`
	UnresolvedObligations []ObligationRef `json:"unresolved_obligation_candidates"`
	ActiveRepair          *RepairRef      `json:"active_repair"`
}

// Classifier interprets one record.
type Classifier interface {
	Name() string
	Version() string
	Hash() string
	// Capabilities names the facts this classifier can establish. A fact absent
	// from the set is one this build cannot measure, which is a different
	// statement from measuring it and getting no answer.
	Capabilities() Capabilities
	Classify(ctx context.Context, in Input) (Result, error)
}
