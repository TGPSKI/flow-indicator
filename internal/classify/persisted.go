package classify

import (
	"context"
	"errors"
	"fmt"
)

// ErrPersistedMissing says strict replay had no validated semantic completion
// for an eligible record. Falling back here would make a partly semantic replay
// look complete, so callers must reject the projection.
var ErrPersistedMissing = errors.New("persisted semantic result is missing")

// ErrPersistedAmbiguous says two retained completions claim the same immutable
// semantic input. Selection must not depend on append order or worker timing.
var ErrPersistedAmbiguous = errors.New("persisted semantic result is ambiguous")

// Persisted selects prior semantic completions by immutable input identity. It
// performs no network work: the completion is an explicit replay input.
type Persisted struct {
	semantic Classifier
	markers  Heuristic
	bySeq    map[uint64][]Completion
	missing  []uint64
	issue    error
}

// NewPersisted constructs a strict replay classifier. Completions belonging to
// another classifier identity remain in the log but cannot license this replay.
func NewPersisted(semantic Classifier, completions []Completion) (*Persisted, error) {
	if semantic == nil {
		return nil, errors.New("persisted semantic: classifier is required")
	}
	p := &Persisted{semantic: semantic, bySeq: make(map[uint64][]Completion)}
	for _, c := range completions {
		if c.Status != CompletionCompleted || c.Result == nil {
			continue
		}
		if c.Classifier != semantic.Name() || c.ClassifierVersion != semantic.Version() || c.ClassifierHash != semantic.Hash() {
			continue
		}
		if c.Result.Provenance.Classifier != c.Classifier || c.Result.Provenance.Version != c.ClassifierVersion || c.Result.Provenance.Hash != c.ClassifierHash {
			return nil, fmt.Errorf("persisted semantic: completion %s result provenance does not match its classifier identity", c.JobID)
		}
		p.bySeq[c.Seq] = append(p.bySeq[c.Seq], c)
	}
	return p, nil
}

func (p *Persisted) Name() string    { return "persisted-" + p.semantic.Name() }
func (p *Persisted) Version() string { return p.semantic.Version() }
func (p *Persisted) Hash() string    { return p.semantic.Hash() }
func (p *Persisted) Capabilities() Capabilities {
	return p.semantic.Capabilities()
}

// Classify returns the exact retained result whose context hash matches this
// projection. Ineligible records retain deterministic marker classification.
func (p *Persisted) Classify(ctx context.Context, in Input) (Result, error) {
	if in.Turn.Text == "" || !worthSending(in) || len(in.Turn.Text) > MaxTurnBytes {
		return p.markers.Classify(ctx, in)
	}
	hash := InputHash(in)
	var matches []Completion
	for _, c := range p.bySeq[in.Turn.Seq] {
		if c.StreamID == in.Turn.StreamID && c.InputHash == hash {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 1:
		return *matches[0].Result, nil
	case 0:
		p.missing = append(p.missing, in.Turn.Seq)
		fallback, err := p.markers.Classify(ctx, in)
		if err != nil {
			return fallback, err
		}
		err = fmt.Errorf("%w: source sequence %d", ErrPersistedMissing, in.Turn.Seq)
		if p.issue == nil {
			p.issue = err
		}
		return fallback, err
	default:
		err := fmt.Errorf("%w: source sequence %d has %d matching completions", ErrPersistedAmbiguous, in.Turn.Seq, len(matches))
		if p.issue == nil {
			p.issue = err
		}
		return Result{}, err
	}
}

// Missing returns a copy of source sequences without a selected completion.
func (p *Persisted) Missing() []uint64 { return append([]uint64(nil), p.missing...) }

// Err reports the first selection failure encountered during a preflight.
func (p *Persisted) Err() error { return p.issue }
