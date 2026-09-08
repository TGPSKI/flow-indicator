package classify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

type queuedClassifier struct {
	gate <-chan struct{}
	err  error
}

func (queuedClassifier) Name() string               { return "queued-test" }
func (queuedClassifier) Version() string            { return "1" }
func (queuedClassifier) Hash() string               { return "queued-test-hash" }
func (queuedClassifier) Capabilities() Capabilities { return nil }
func (q queuedClassifier) Classify(ctx context.Context, in Input) (Result, error) {
	if q.gate != nil {
		select {
		case <-q.gate:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	if q.err != nil {
		return Result{}, q.err
	}
	return Result{Provenance: Provenance{Classifier: q.Name(), Version: q.Version(), Hash: q.Hash(), SourceTurn: in.Turn.Seq}}, nil
}

func semanticInput(seq uint64) Input {
	return Input{Turn: stream.Record{StreamID: "stream", Seq: seq, SpeakerClass: stream.SpeakerHuman, Text: "No. Revert it.", Metadata: stream.Metadata{}}}
}

func TestSemanticJobIdentityIncludesClassifierAndInput(t *testing.T) {
	in := semanticInput(4)
	a := newJob("stream", 0, in, queuedClassifier{})
	b := newJob("stream", 0, in, queuedClassifier{})
	if a.ID != b.ID || a.InputHash != b.InputHash {
		t.Fatalf("same semantic request is unstable: %+v %+v", a, b)
	}
	changed := semanticInput(5)
	c := newJob("stream", 0, changed, queuedClassifier{})
	if a.ID == c.ID || a.InputHash == c.InputHash {
		t.Fatal("different source input reused semantic identity")
	}
}

func TestWorkerCompletesWithoutBlockingSubmit(t *testing.T) {
	w, err := NewWorker(queuedClassifier{}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	started := time.Now()
	job, err := w.Submit("stream", 3, semanticInput(8))
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 100*time.Millisecond {
		t.Fatalf("Submit blocked for %s", took)
	}
	select {
	case got := <-w.Results():
		if got.JobID != job.ID || got.Status != CompletionCompleted || got.Result == nil {
			t.Fatalf("completion = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("worker produced no completion")
	}
	stats := w.Snapshot()
	if stats.Requested != 1 || stats.Completed != 1 || stats.Pending != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestWorkerCatchesUpWithoutBlockingWhenQueueIsFull(t *testing.T) {
	gate := make(chan struct{})
	w, err := NewWorker(queuedClassifier{gate: gate}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Submit("stream", 0, semanticInput(1)); err != nil {
		t.Fatal(err)
	}
	// Let the worker take the first job and block inside the classifier.
	time.Sleep(10 * time.Millisecond)
	if _, err := w.Submit("stream", 0, semanticInput(2)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Submit("stream", 0, semanticInput(3)); err != nil {
		t.Fatal(err)
	}
	if got := w.Snapshot().CatchUp; got != 1 {
		t.Fatalf("catch-up = %d, want 1", got)
	}
	if _, err := w.Submit("stream", 0, semanticInput(4)); !errors.Is(err, ErrSemanticQueueFull) {
		t.Fatalf("overflow error = %v, want ErrSemanticQueueFull", err)
	}
	if got := w.Snapshot().Dropped; got != 1 {
		t.Fatalf("dropped = %d, want one job beyond both bounded queues", got)
	}
	close(gate)
	for range 3 {
		select {
		case <-w.Results():
		case <-time.After(time.Second):
			t.Fatal("catch-up job did not complete")
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHybridReturnsHeuristicImmediatelyAndDefersSemanticEvidence(t *testing.T) {
	gate := make(chan struct{})
	h, err := NewHybrid(queuedClassifier{gate: gate}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	started := time.Now()
	res, err := h.Classify(context.Background(), semanticInput(9))
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 100*time.Millisecond {
		t.Fatalf("hybrid classifier blocked the fast path for %s", took)
	}
	if res.Provenance.Classifier != (Heuristic{}).Name() {
		t.Fatalf("fast result came from %q, want heuristic", res.Provenance.Classifier)
	}
	if h.Capabilities().Can(CapObligationSupersession) {
		t.Fatal("unapplied semantic work expanded live projector capabilities")
	}
	close(gate)
	select {
	case completion := <-h.Results():
		if completion.Status != CompletionCompleted || completion.Result == nil {
			t.Fatalf("completion = %+v", completion)
		}
	case <-time.After(time.Second):
		t.Fatal("hybrid worker did not complete")
	}
}

func TestPercentileUsesTheBoundedOperationalSample(t *testing.T) {
	got := []int64{1, 2, 3, 4, 5}
	if p50 := percentile(got, 50); p50 != 3 {
		t.Errorf("p50 = %d, want 3", p50)
	}
	if p95 := percentile(got, 95); p95 != 5 {
		t.Errorf("p95 = %d, want 5", p95)
	}
}

func TestWorkerCloseMarksBufferedJobsCanceled(t *testing.T) {
	gate := make(chan struct{})
	w, err := NewWorker(queuedClassifier{gate: gate}, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Submit("stream", 0, semanticInput(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Submit("stream", 0, semanticInput(2)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	stats := w.Snapshot()
	if stats.Pending != 0 || stats.Canceled != 2 {
		t.Fatalf("stats after close = %+v, want both jobs canceled", stats)
	}
}

func TestWorkerRecordsDeadlineSeparatelyFromFailure(t *testing.T) {
	gate := make(chan struct{})
	w, err := NewWorkerWithDeadline(queuedClassifier{gate: gate}, 1, 1, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Submit("stream", 0, semanticInput(1)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-w.Results():
		if got.Status != CompletionTimedOut {
			t.Fatalf("status = %q, want %q", got.Status, CompletionTimedOut)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not report deadline")
	}
	stats := w.Snapshot()
	if stats.TimedOut != 1 || stats.Failed != 0 {
		t.Fatalf("stats = %+v, want one timeout and no failure", stats)
	}
}

func TestWorkerRetriesOneTimeoutThroughCatchUp(t *testing.T) {
	gate := make(chan struct{})
	w, err := NewWorkerWithDeadline(queuedClassifier{gate: gate}, 1, 1, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	first, err := w.Submit("stream", 0, semanticInput(1))
	if err != nil {
		t.Fatal(err)
	}

	var got []Completion
	for range 2 {
		select {
		case completion := <-w.Results():
			got = append(got, completion)
		case <-time.After(time.Second):
			t.Fatal("worker did not finish both timeout attempts")
		}
	}
	if got[0].JobID != first.ID || got[0].Attempt != 0 {
		t.Fatalf("first attempt = %+v", got[0])
	}
	if got[1].JobID == first.ID || got[1].Attempt != 1 {
		t.Fatalf("retry attempt = %+v", got[1])
	}
	stats := w.Snapshot()
	if stats.Requested != 2 || stats.TimedOut != 2 || stats.Pending != 0 || stats.CatchUp != 0 || stats.Dropped != 0 {
		t.Fatalf("stats after retry = %+v", stats)
	}
}

func TestPersistedSelectsOnlyMatchingCompletion(t *testing.T) {
	in := semanticInput(7)
	semantic := queuedClassifier{}
	result := Result{Correction: Correction{IsCorrection: true}, Provenance: Provenance{
		Classifier: semantic.Name(), Version: semantic.Version(), Hash: semantic.Hash(), SourceTurn: in.Turn.Seq,
	}}
	persisted, err := NewPersisted(semantic, []Completion{{
		JobID: "selected", StreamID: "stream", Seq: in.Turn.Seq, InputHash: InputHash(in),
		Classifier: semantic.Name(), ClassifierVersion: semantic.Version(), ClassifierHash: semantic.Hash(),
		Status: CompletionCompleted, Result: &result,
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := persisted.Classify(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Correction.IsCorrection || got.Provenance.Classifier != semantic.Name() {
		t.Fatalf("selected result = %+v", got)
	}

	changed := in
	changed.Turn.Text = "Use the previous approach instead."
	if _, err := persisted.Classify(context.Background(), changed); !errors.Is(err, ErrPersistedMissing) {
		t.Fatalf("changed input error = %v, want missing result", err)
	}
}

func TestPersistedRejectsAmbiguousMatchingCompletion(t *testing.T) {
	in := semanticInput(7)
	semantic := queuedClassifier{}
	result := Result{Provenance: Provenance{Classifier: semantic.Name(), Version: semantic.Version(), Hash: semantic.Hash(), SourceTurn: in.Turn.Seq}}
	completion := Completion{StreamID: "stream", Seq: in.Turn.Seq, InputHash: InputHash(in), Classifier: semantic.Name(), ClassifierVersion: semantic.Version(), ClassifierHash: semantic.Hash(), Status: CompletionCompleted, Result: &result}
	persisted, err := NewPersisted(semantic, []Completion{completion, completion})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persisted.Classify(context.Background(), in); !errors.Is(err, ErrPersistedAmbiguous) {
		t.Fatalf("ambiguous error = %v, want ErrPersistedAmbiguous", err)
	}
}
