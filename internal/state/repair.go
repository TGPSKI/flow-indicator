package state

import (
	"fmt"
	"time"
)

// Repair episode statuses.
const (
	RepairOpen        = "open"
	RepairProvisional = "provisionally_closed"
	RepairDurable     = "durably_closed"
	RepairReset       = "reset"
	RepairAbandoned   = "abandoned"
	RepairUnknown     = "unknown"
)

// Repair is one repair episode: an operator correction and everything spent
// before the work moved forward again.
type Repair struct {
	ID                  string `json:"id"`
	TriggerAgentTurn    uint64 `json:"trigger_agent_turn"`
	FirstCorrectionTurn uint64 `json:"first_correction_turn"`
	TargetType          string `json:"target_type"`
	TargetKey           string `json:"target_key,omitempty"`
	// TargetPaths are the paths the correction named. A write to one of them is
	// verified repair; a write outside them is expansion. Empty means the
	// correction named no path, and both questions stay unanswered.
	TargetPaths []string `json:"target_paths,omitempty"`
	// Structural reports that the episode was opened by what the agent did — a
	// re-edit of a path the previous cycle wrote — rather than by what the
	// operator's words matched.
	Structural bool   `json:"structural,omitempty"`
	Depth      int    `json:"depth"`
	Status     string `json:"status"`

	Start time.Time `json:"start"`
	// LastActivity is the timestamp of the last record attributed to the
	// episode. It moves while the episode is open and says nothing about the
	// episode having ended.
	LastActivity time.Time `json:"last_activity"`
	// Closed is the timestamp at which the episode left the open state. It is
	// absent while the episode is open, and is cleared again when a later
	// correction reopens it: an episode that is open has not ended.
	Closed *time.Time `json:"closed,omitempty"`

	Epoch uint64 `json:"epoch"`

	// Chars is RCC: operator characters spent inside the episode.
	Chars int `json:"chars"`
	// Records is RTC: records inside the episode.
	Records int `json:"records"`
	// PointerChars is the size of the compact reference that triggered the
	// episode, when it began with one.
	PointerChars int `json:"pointer_chars"`

	// Expansions is REC summed over the correction response cycle.
	Expansions int    `json:"repair_expansion_count"`
	Pollution  string `json:"pollution_status"`
	Recurred   bool   `json:"recurred"`
	// RepairClaimed reports that the agent said it repaired the target. It is
	// the agent's account of its own work, not verification.
	RepairClaimed bool `json:"repair_claimed"`

	// closeIndex is the operator-turn ordinal at provisional close, used to
	// measure the durability window.
	closeIndex int
	// activeIndex is the operator-turn ordinal of the last correction in this
	// episode, used to bound how long an episode may stay open.
	activeIndex int
}

// newRepair opens an episode. Depth starts at 1: the correction itself.
func newRepair(id int, epoch uint64, triggerTurn, correctionTurn uint64, targetType, targetKey string, pointerChars int, start time.Time) *Repair {
	return &Repair{
		ID:                  fmt.Sprintf("rep-%d", id),
		TriggerAgentTurn:    triggerTurn,
		FirstCorrectionTurn: correctionTurn,
		TargetType:          targetType,
		TargetKey:           targetKey,
		Depth:               1,
		Status:              RepairOpen,
		Start:               start,
		LastActivity:        start,
		Epoch:               epoch,
		PointerChars:        pointerChars,
		Pollution:           "",
	}
}

// close records that the episode left the open state at t, and the status it
// left for. Nothing else writes Closed: an episode that is still open must not
// carry a timestamp whose documented meaning is that it ended.
func (r *Repair) close(status string, t time.Time) {
	r.Status = status
	at := t
	r.Closed = &at
}

// reopen puts the episode back in the open state. The closing timestamp is
// dropped because the episode did not end when it was written.
func (r *Repair) reopen() {
	r.Status = RepairOpen
	r.Closed = nil
}

// touch records that a record attributed to the episode arrived at t. A record
// carrying no timestamp is not evidence about time and leaves the endpoint
// where the last timestamped record put it.
func (r *Repair) touch(t time.Time) {
	if t.IsZero() {
		return
	}
	r.LastActivity = t
}

// end is the endpoint of the episode's duration. A closed episode ran until it
// left the open state; an open one has no end yet, so the duration runs to the
// last record attributed to it and is partial. Measuring a closed episode to
// LastActivity would report the span up to some record inside it rather than
// the span of the episode.
func (r *Repair) end() time.Time {
	if r.Closed != nil {
		return *r.Closed
	}
	return r.LastActivity
}
