package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func sample(kind string, seq uint64) event.Event {
	b := event.NewBuilder("s", seq, 0, stream.SourceRef{Adapter: "generic", RecordSHA256: "abc"})
	return b.Emit(kind, event.ClassObserved, map[string]uint64{"seq": seq})
}

func TestAppendAndReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(1); i <= 3; i++ {
		if err := w.Append(sample(event.KindRecordObserved, i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := ReadEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("read %d events, want 3", len(events))
	}
	if events[2].Seq != 3 || events[0].Source.RecordSHA256 != "abc" {
		t.Fatalf("events did not round trip: %+v", events)
	}
}

func TestAppendDoesNotRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	for i := uint64(1); i <= 2; i++ {
		w, err := OpenWriter(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Append(sample(event.KindRecordObserved, i)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	events, err := ReadEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("read %d events, want 2: reopening must append, not truncate", len(events))
	}
}

func TestMissingFileReadsAsNoEvents(t *testing.T) {
	events, err := ReadEvents(filepath.Join(t.TempDir(), "absent.jsonl"))
	if err != nil {
		t.Fatalf("missing file returned an error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("read %d events from a missing file", len(events))
	}
}

func TestCorruptLineNamesItsLocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("{\"id\":\"a\"}\nnot json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadEvents(path)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error = %v, want one naming line 2", err)
	}
}

// Candidate kinds share the "repair_" prefix with state transitions. Routing is
// by exact kind so a candidate is never filed as a state change.
func TestFileRouting(t *testing.T) {
	cases := map[string]string{
		event.KindRecordObserved:      FileObservations,
		event.KindSegmentsClassified:  FileClassifications,
		event.KindExpansionCandidate:  FileClassifications,
		event.KindSemanticCompleted:   FileClassifications,
		event.KindObligationCandidate: FileClassifications,
		event.KindObligationRepeated:  FileObligations,
		event.KindRepairOpened:        FileRepairs,
		event.KindRepairStatus:        FileRepairs,
		event.KindMetricsComputed:     FileMetrics,
		event.KindRegimeChanged:       FileMetrics,
		event.KindPointerResolved:     FileMetrics,
	}
	for kind, want := range cases {
		if got := FileFor(kind); got != want {
			t.Errorf("%s routed to %s, want %s", kind, got, want)
		}
	}
}

func TestSourceWriteDoesNotAddWallClockMetadata(t *testing.T) {
	source := Source{StreamID: "abc", Adapter: "generic", Path: "input.jsonl", Records: 3}
	var written []string
	for range 2 {
		root := t.TempDir()
		s, err := OpenSession(root, "abc", false)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteSource(source); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(s.Path(FileSource))
		if err != nil {
			t.Fatal(err)
		}
		written = append(written, string(raw))
	}
	if written[0] != written[1] {
		t.Fatalf("source metadata differs between writes:\n%s\n%s", written[0], written[1])
	}
}

func TestSessionRefusesToDoubleAnExistingLog(t *testing.T) {
	root := t.TempDir()
	s, err := OpenSession(root, "abc", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(sample(event.KindRecordObserved, 1)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession(root, "abc", false); err == nil {
		t.Fatal("a second session opened over an existing event log")
	}
	forced, err := OpenSession(root, "abc", true)
	if err != nil {
		t.Fatalf("--force did not reopen the session: %v", err)
	}
	if err := forced.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := ReadEvents(filepath.Join(SessionDir(root, "abc"), FileObservations))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("forced session kept %d old events", len(events))
	}
}

func TestSanitizeID(t *testing.T) {
	cases := map[string]string{
		"":                      "unknown-stream",
		"5803f1":                "5803f1",
		"../../etc/passwd":      "etc-passwd",
		"project/session.jsonl": "project-session.jsonl",
	}
	for in, want := range cases {
		if got := SanitizeID(in); got != want {
			t.Errorf("SanitizeID(%q) = %q, want %q", in, got, want)
		}
	}
}
