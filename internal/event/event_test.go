package event

import (
	"encoding/json"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func TestIDIsStableAcrossReplays(t *testing.T) {
	first := ID("session-a", 42, KindRecordObserved, 0, Version)
	second := ID("session-a", 42, KindRecordObserved, 0, Version)
	if first != second {
		t.Fatalf("event ID is not stable: %s vs %s", first, second)
	}
	if len(first) != idChars {
		t.Fatalf("event ID length = %d, want %d", len(first), idChars)
	}
}

func TestIDSeparatesEveryInput(t *testing.T) {
	base := ID("session-a", 42, KindRecordObserved, 0, Version)
	cases := map[string]string{
		"stream":  ID("session-b", 42, KindRecordObserved, 0, Version),
		"seq":     ID("session-a", 43, KindRecordObserved, 0, Version),
		"kind":    ID("session-a", 42, KindMetricsComputed, 0, Version),
		"ordinal": ID("session-a", 42, KindRecordObserved, 1, Version),
		"version": ID("session-a", 42, KindRecordObserved, 0, Version+1),
	}
	for name, got := range cases {
		if got == base {
			t.Errorf("changing the %s did not change the event ID", name)
		}
	}
}

func TestIDWithIdentitySeparatesDeferredInterpretations(t *testing.T) {
	first := IDWithIdentity("session-a", 42, KindSemanticCompleted, "job-a", Version)
	second := IDWithIdentity("session-a", 42, KindSemanticCompleted, "job-b", Version)
	if first == second {
		t.Fatal("two semantic attempts for one source turn share an event ID")
	}
	if first != IDWithIdentity("session-a", 42, KindSemanticCompleted, "job-a", Version) {
		t.Fatal("deferred interpretation ID is not stable")
	}
}

func TestBuilderCountsOrdinalsPerKind(t *testing.T) {
	b := NewBuilder("s", 7, 0, stream.SourceRef{})
	first := b.Emit(KindObligationCandidate, ClassClassified, map[string]string{"key": "a"})
	second := b.Emit(KindObligationCandidate, ClassClassified, map[string]string{"key": "b"})
	other := b.Emit(KindStopCandidate, ClassClassified, map[string]string{})
	if first.ID == second.ID {
		t.Error("two events of the same kind on one record share an ID")
	}
	if first.ID == other.ID {
		t.Error("events of different kinds share an ID")
	}
	if first.Version != Version || first.Seq != 7 || first.StreamID != "s" {
		t.Errorf("envelope fields not carried: %+v", first)
	}
}

func TestSetEpochAppliesToLaterEvents(t *testing.T) {
	b := NewBuilder("s", 7, 0, stream.SourceRef{})
	before := b.Emit(KindRepairReset, ClassDerived, map[string]string{})
	b.SetEpoch(1)
	after := b.Emit(KindMetricsComputed, ClassDerived, map[string]string{})
	if before.Epoch != 0 {
		t.Errorf("event before the reset carries epoch %d, want 0", before.Epoch)
	}
	if after.Epoch != 1 {
		t.Errorf("event after the reset carries epoch %d, want 1", after.Epoch)
	}
}

func TestPayloadRoundTrips(t *testing.T) {
	b := NewBuilder("s", 1, 0, stream.SourceRef{Adapter: "generic", RecordSHA256: "abc"})
	e := b.Emit(KindRecordObserved, ClassObserved, map[string]int{"chars": 12})
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var back Event
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != e.ID || back.Source.RecordSHA256 != "abc" || string(back.Payload) != string(e.Payload) {
		t.Fatalf("event did not round trip: %+v", back)
	}
}
