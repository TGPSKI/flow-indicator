package render

import (
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
	"github.com/TGPSKI/flow-indicator/internal/state"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func TestSemanticProjectionUpdateSupersedesDerivedView(t *testing.T) {
	b := event.NewBuilder("stream", 1, 0, stream.SourceRef{Adapter: "test"})
	marker := b.Emit(event.KindMetricsComputed, event.ClassDerived, metrics.Snapshot{Seq: 1, Regime: "FLOW"})
	semantic := b.Emit(event.KindMetricsComputed, event.ClassDerived, metrics.Snapshot{Seq: 1, Regime: "RECOVERY"})
	update := event.NewWithIdentity("stream", 1, 0, stream.SourceRef{Adapter: "projector"},
		event.KindSemanticProjectionUpdated, event.ClassDerived, "job-1",
		state.SemanticProjectionUpdate{Events: []event.Event{semantic}})

	loaded, err := LoadEvents([]event.Event{marker, update})
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Final().Regime; got != "RECOVERY" {
		t.Fatalf("final regime = %q, want semantic projection", got)
	}
}
