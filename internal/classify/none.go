package classify

import "context"

// None classifies nothing. It exists so an operator can run the instrument on
// observations alone: every classified metric then reports unknown instead of
// a marker guess.
type None struct{}

func (None) Name() string    { return "none" }
func (None) Version() string { return "1" }
func (None) Hash() string    { return "none" }

// Capabilities is empty: a classifier that interprets nothing establishes
// nothing.
func (None) Capabilities() Capabilities { return nil }

func (None) Classify(_ context.Context, in Input) (Result, error) {
	return Result{
		Pointer:    Pointer{Type: PointerUnknown},
		Correction: Correction{TargetType: "unknown"},
		Provenance: Provenance{
			Classifier: "none",
			Version:    "1",
			Hash:       "none",
			SourceTurn: in.Turn.Seq,
		},
	}, nil
}
