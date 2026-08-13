// Package state projects events into current state: obligations, repair
// episodes, epochs and the interaction regime.
//
// The projector owns one stage of the processing order — observe, classify,
// project, measure. Nothing here parses source formats and nothing here
// renders.
package state

import (
	"fmt"

	"github.com/TGPSKI/flow-indicator/internal/classify"
)

// Obligation statuses.
//
// ObligationUnresolved is the status of a candidate that was introduced and has
// not been resolved since. It is deliberately not called active: nothing
// establishes that a requirement remains in force, only that nothing has
// resolved it.
//
// The resolved statuses are not interchangeable. Released is the operator
// withdrawing the requirement and is directly observed. Superseded is a later
// requirement replacing it and is read off the obligation surface. Satisfied is
// the agent's work meeting it, which needs verification the marker tier cannot
// reach, so it is reachable only under a classifier that declares
// CapObligationSatisfaction and never from the agent's own account of its work.
const (
	ObligationUnresolved = "unresolved"
	ObligationSatisfied  = "satisfied"
	ObligationViolated   = "violated"
	ObligationReleased   = "released"
	ObligationSuperseded = "superseded"
	ObligationExpired    = "expired"
	ObligationUnknown    = "unknown"
)

// resolved reports whether a status means the candidate no longer stands.
// Violated is not among them: a violated requirement is still a requirement,
// which is why it stays in the unresolved inventory.
func resolved(status string) bool {
	switch status {
	case ObligationSatisfied, ObligationReleased, ObligationSuperseded, ObligationExpired:
		return true
	}
	return false
}

// Obligation is an explicit operator requirement and its current projection.
type Obligation struct {
	ID             string `json:"id"`
	IntroducedTurn uint64 `json:"introduced_turn"`
	SourceText     string `json:"source_text"`
	Key            string `json:"key"`
	// RepeatKey is the content-token key a restatement is recognized by. It is
	// stored so a reader can see what a repeat was matched on.
	RepeatKey      string `json:"repeat_key,omitempty"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	RepeatCount    int    `json:"repeat_count"`
	LastRepeatTurn uint64 `json:"last_repeat_turn"`
	Epoch          uint64 `json:"epoch"`
	// ResolvedTurn is the record that resolved the candidate, zero while it
	// stands. A candidate that was resolved and then restated keeps the turn
	// that resolved it, so the round trip is readable from the obligation
	// itself.
	ResolvedTurn uint64 `json:"resolved_turn,omitempty"`

	// introducedIndex is the operator-turn ordinal at introduction, used for
	// age in turns.
	introducedIndex int
}

// Revival is a resolved candidate the operator stated again, with the status it
// is coming back from. The prior status is the point of the record: a
// requirement returning from released says the operator changed their mind,
// and one returning from satisfied says the instrument called it met too early.
type Revival struct {
	Obligation *Obligation
	From       string
}

// Obligations is the obligation surface of one stream.
type Obligations struct {
	order []*Obligation
	byKey map[string]*Obligation
	// byRepeat indexes candidates by their content-token key, so a requirement
	// the operator restates in different words finds the candidate it restates.
	// Identity stays on byKey: this index answers "have they said this before",
	// not "which requirement is this".
	byRepeat map[string]*Obligation
	next     int
}

// NewObligations returns an empty surface.
func NewObligations() *Obligations {
	return &Obligations{
		byKey:    make(map[string]*Obligation),
		byRepeat: make(map[string]*Obligation),
	}
}

// Apply folds one turn's candidates into the surface. A candidate whose
// normalized key already exists is a repeat; anything else is an introduction.
// Similarity is never treated as identity here.
//
// A candidate whose key was resolved earlier is revived rather than introduced
// again. The operator restating a requirement they had withdrawn is evidence
// that the withdrawal no longer holds, and it is evidence about the same
// requirement: a second obligation with a fresh identity would report a first
// statement where the session shows a re-assertion, and would lose the repeat
// count that makes the round trip visible.
func (o *Obligations) Apply(turn uint64, epoch uint64, turnIndex int, candidates []classify.ObligationCandidate) (introduced, repeated []*Obligation, revived []Revival) {
	seen := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		if seen[c.Key] {
			continue
		}
		seen[c.Key] = true

		// Exact identity first, then the content-token index. An operator who
		// writes the same sentence twice and one who rewords it are both
		// repeating; only the first is byte equality, and keying on that alone
		// found no repeat anywhere in the seed corpus.
		existing, ok := o.byKey[c.Key]
		if !ok && c.RepeatKey != "" {
			existing, ok = o.byRepeat[c.RepeatKey]
		}
		if ok {
			existing.RepeatCount++
			existing.LastRepeatTurn = turn
			if resolved(existing.Status) {
				revived = append(revived, Revival{Obligation: existing, From: existing.Status})
				existing.Status = ObligationUnresolved
				continue
			}
			if existing.Status == ObligationUnknown {
				existing.Status = ObligationUnresolved
			}
			repeated = append(repeated, existing)
			continue
		}
		o.next++
		ob := &Obligation{
			ID:              fmt.Sprintf("obl-%d", o.next),
			IntroducedTurn:  turn,
			SourceText:      c.Text,
			Key:             c.Key,
			RepeatKey:       c.RepeatKey,
			Kind:            c.Kind,
			Status:          ObligationUnresolved,
			Epoch:           epoch,
			introducedIndex: turnIndex,
		}
		o.byKey[c.Key] = ob
		if c.RepeatKey != "" {
			o.byRepeat[c.RepeatKey] = ob
		}
		o.order = append(o.order, ob)
		introduced = append(introduced, ob)
	}
	return introduced, repeated, revived
}

// Resolve records that a candidate no longer stands, and returns it. A key that
// is not in the surface, a status that is already resolved, and a resolution
// kind this build does not recognize each resolve nothing and return nil.
//
// The obligation stays in byKey after resolving. Restating it later has to find
// it, or the round trip would read as a new requirement.
func (o *Obligations) Resolve(turn uint64, key, kind string) *Obligation {
	ob, ok := o.byKey[key]
	if !ok || resolved(ob.Status) {
		return nil
	}
	switch kind {
	case classify.ResolutionReleased:
		ob.Status = ObligationReleased
	case classify.ResolutionSuperseded:
		ob.Status = ObligationSuperseded
	case classify.ResolutionSatisfied:
		ob.Status = ObligationSatisfied
	default:
		return nil
	}
	ob.ResolvedTurn = turn
	return ob
}

// Violate marks an obligation violated. It is called when the operator repeats
// an unresolved obligation inside a correction whose identified target is that
// obligation: the restatement plus the linkage is the evidence.
func (o *Obligations) Violate(key string) *Obligation {
	ob, ok := o.byKey[key]
	if !ok {
		return nil
	}
	ob.Status = ObligationViolated
	return ob
}

// Unresolved returns the obligation candidates that have not been resolved, in
// introduction order.
//
// This is an inventory, not a claim about which requirements remain in force.
// A candidate is here because it was introduced in this epoch and nothing since
// resolved it, which is a fact about the instrument's evidence rather than about
// the operator's intent. Leaving the inventory needs evidence, and which kinds
// of evidence are reachable depends on the classifier: see Capabilities. Under
// a classifier that establishes none of them the inventory only grows, and that
// is a statement about the instrument rather than about the session.
func (o *Obligations) Unresolved() []*Obligation {
	var out []*Obligation
	for _, ob := range o.order {
		if ob.Status == ObligationUnresolved || ob.Status == ObligationViolated {
			out = append(out, ob)
		}
	}
	return out
}

// All returns every obligation ever introduced, in order.
func (o *Obligations) All() []*Obligation { return o.order }

// RepeatedUnresolved counts unresolved candidates the operator has had to
// repeat.
func (o *Obligations) RepeatedUnresolved() int {
	var n int
	for _, ob := range o.Unresolved() {
		if ob.RepeatCount > 0 {
			n++
		}
	}
	return n
}

// CountStatus counts obligations in a status.
func (o *Obligations) CountStatus(status string) int {
	var n int
	for _, ob := range o.order {
		if ob.Status == status {
			n++
		}
	}
	return n
}

// MarkUnknown ends the epoch's obligation surface. A reset destroys the
// evidence that would have resolved these, so they become unknown rather than
// satisfied or violated.
func (o *Obligations) MarkUnknown() []string {
	var ids []string
	for _, ob := range o.Unresolved() {
		ob.Status = ObligationUnknown
		ids = append(ids, ob.ID)
	}
	o.byKey = make(map[string]*Obligation)
	o.byRepeat = make(map[string]*Obligation)
	return ids
}
