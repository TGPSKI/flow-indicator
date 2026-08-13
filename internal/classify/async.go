package classify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// ErrSemanticQueueFull says the live semantic lane declined a job rather than
// delaying transcript ingestion. A declined semantic reading is visible in the
// operational counters and never changes the fast projection.
var ErrSemanticQueueFull = errors.New("semantic classifier queue is full")

// Job is one immutable request for deferred semantic classification. Input is
// private so that a persisted completion cannot accidentally retain a second
// transcript copy; InputHash identifies the exact context the model saw.
type Job struct {
	ID                string
	StreamID          string
	Seq               uint64
	Epoch             uint64
	Source            stream.SourceRef
	InputHash         string
	Classifier        string
	ClassifierVersion string
	ClassifierHash    string
	input             Input
}

// Completion is the durable, auditable result of one semantic job. Result is
// present only when Status is completed. Latency measures the worker operation,
// not the interaction stream, and is kept outside the deterministic projector.
type Completion struct {
	JobID             string           `json:"job_id"`
	StreamID          string           `json:"stream_id"`
	Seq               uint64           `json:"source_seq"`
	Epoch             uint64           `json:"epoch"`
	Source            stream.SourceRef `json:"source"`
	InputHash         string           `json:"input_hash"`
	Classifier        string           `json:"classifier"`
	ClassifierVersion string           `json:"classifier_version"`
	ClassifierHash    string           `json:"classifier_hash"`
	Status            string           `json:"status"`
	Result            *Result          `json:"result,omitempty"`
	Error             string           `json:"error,omitempty"`
	LatencyMS         int64            `json:"latency_ms"`
}

const (
	CompletionCompleted = "completed"
	CompletionFailed    = "failed"
	CompletionTimedOut  = "timed_out"
	CompletionCanceled  = "canceled"
)

// Operational is a point-in-time view of the semantic lane. It describes the
// measurement apparatus and must never enter a regime comparison.
type Operational struct {
	Requested int   `json:"requested"`
	Completed int   `json:"completed"`
	Failed    int   `json:"failed"`
	TimedOut  int   `json:"timed_out"`
	Canceled  int   `json:"canceled"`
	Dropped   int   `json:"dropped"`
	Pending   int   `json:"pending"`
	LastMS    int64 `json:"last_latency_ms,omitempty"`
	P50MS     int64 `json:"p50_latency_ms,omitempty"`
	P95MS     int64 `json:"p95_latency_ms,omitempty"`
}

// Worker performs semantic classification away from the source reader. It is
// bounded by workers and queue capacity; it cannot create an unbounded backlog
// of transcript text while a local model is slow.
type Worker struct {
	classifier Classifier
	deadline   time.Duration
	jobs       chan Job
	results    chan Completion
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	stats   Operational
	latency []int64
}

// NewWorker starts a bounded semantic worker pool.
func NewWorker(classifier Classifier, workers, queue int) (*Worker, error) {
	return NewWorkerWithDeadline(classifier, workers, queue, 0)
}

// NewWorkerWithDeadline starts a bounded worker pool with a per-request
// deadline. A zero deadline leaves the caller's context as the only bound,
// which is useful for deterministic tests and strict replay.
func NewWorkerWithDeadline(classifier Classifier, workers, queue int, deadline time.Duration) (*Worker, error) {
	if classifier == nil {
		return nil, errors.New("semantic worker: classifier is required")
	}
	if workers < 1 {
		return nil, fmt.Errorf("semantic worker: workers must be >= 1, got %d", workers)
	}
	if queue < 1 {
		return nil, fmt.Errorf("semantic worker: queue must be >= 1, got %d", queue)
	}
	if deadline < 0 {
		return nil, fmt.Errorf("semantic worker: deadline must be >= 0, got %s", deadline)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Worker{
		classifier: classifier,
		deadline:   deadline,
		jobs:       make(chan Job, queue),
		results:    make(chan Completion, workers),
		ctx:        ctx,
		cancel:     cancel,
	}
	for range workers {
		w.wg.Add(1)
		go w.run()
	}
	go func() {
		w.wg.Wait()
		close(w.results)
	}()
	return w, nil
}

// Submit copies input before it crosses the worker boundary. It never blocks:
// a full queue is an operational fact, not a reason to stop observing a live
// source.
func (w *Worker) Submit(streamID string, epoch uint64, in Input) (Job, error) {
	job := newJob(streamID, epoch, in, w.classifier)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return Job{}, errors.New("semantic worker: submit after close")
	}
	select {
	case w.jobs <- job:
		w.stats.Requested++
		w.stats.Pending++
		return job, nil
	default:
		w.stats.Dropped++
		return Job{}, ErrSemanticQueueFull
	}
}

// Results delivers completions in completion order. Consumers retain source
// order when applying them to a deterministic replay.
func (w *Worker) Results() <-chan Completion { return w.results }

// Snapshot returns counters for a live view. It contains no model judgement.
func (w *Worker) Snapshot() Operational {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.stats
	if len(w.latency) == 0 {
		return out
	}
	latency := append([]int64(nil), w.latency...)
	sort.Slice(latency, func(i, j int) bool { return latency[i] < latency[j] })
	out.P50MS = percentile(latency, 50)
	out.P95MS = percentile(latency, 95)
	return out
}

// Close cancels in-flight work and closes Results after workers have stopped.
func (w *Worker) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.cancel()
	w.mu.Unlock()
	w.wg.Wait()
	w.mu.Lock()
	// Jobs still buffered when shutdown begins never reached a classifier. They
	// are cancellations, not pending work after the watcher has ended.
	if w.stats.Pending > 0 {
		w.stats.Canceled += w.stats.Pending
		w.stats.Pending = 0
	}
	w.mu.Unlock()
	return nil
}

func (w *Worker) run() {
	defer w.wg.Done()
	for {
		select {
		case <-w.ctx.Done():
			return
		case job := <-w.jobs:
			started := time.Now()
			jobCtx, cancel := w.jobContext()
			res, err := w.classifier.Classify(jobCtx, job.input)
			cancel()
			completion := Completion{
				JobID: job.ID, StreamID: job.StreamID, Seq: job.Seq, Epoch: job.Epoch,
				Source: job.Source, InputHash: job.InputHash,
				Classifier: job.Classifier, ClassifierVersion: job.ClassifierVersion, ClassifierHash: job.ClassifierHash,
				LatencyMS: time.Since(started).Milliseconds(),
			}
			switch {
			case err == nil:
				completion.Status, completion.Result = CompletionCompleted, &res
			case errors.Is(err, context.DeadlineExceeded):
				completion.Status, completion.Error = CompletionTimedOut, err.Error()
			case errors.Is(err, context.Canceled):
				completion.Status, completion.Error = CompletionCanceled, err.Error()
			default:
				completion.Status, completion.Error = CompletionFailed, err.Error()
			}
			w.record(completion)
			select {
			case w.results <- completion:
			case <-w.ctx.Done():
				return
			}
		}
	}
}

func (w *Worker) jobContext() (context.Context, context.CancelFunc) {
	if w.deadline == 0 {
		return context.WithCancel(w.ctx)
	}
	return context.WithTimeout(w.ctx, w.deadline)
}

func (w *Worker) record(c Completion) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stats.Pending > 0 {
		w.stats.Pending--
	}
	w.stats.LastMS = c.LatencyMS
	w.latency = append(w.latency, c.LatencyMS)
	if len(w.latency) > 256 {
		copy(w.latency, w.latency[len(w.latency)-256:])
		w.latency = w.latency[:256]
	}
	switch c.Status {
	case CompletionCompleted:
		w.stats.Completed++
	case CompletionCanceled:
		w.stats.Canceled++
	case CompletionTimedOut:
		w.stats.TimedOut++
	default:
		w.stats.Failed++
	}
}

func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := (len(sorted)*p + 99) / 100
	if i == 0 {
		i = 1
	}
	return sorted[i-1]
}

func newJob(streamID string, epoch uint64, in Input, classifier Classifier) Job {
	copy := cloneInput(in)
	hash := InputHash(copy)
	name, version, classifierHash := classifier.Name(), classifier.Version(), classifier.Hash()
	seed := streamID + "\x00" + fmt.Sprintf("%d", copy.Turn.Seq) + "\x00" + name + "\x00" + version + "\x00" + classifierHash + "\x00" + hash
	sum := sha256.Sum256([]byte(seed))
	return Job{
		ID:                hex.EncodeToString(sum[:])[:24],
		StreamID:          streamID,
		Seq:               copy.Turn.Seq,
		Epoch:             epoch,
		Source:            copy.Turn.Source,
		InputHash:         hash,
		Classifier:        name,
		ClassifierVersion: version,
		ClassifierHash:    classifierHash,
		input:             copy,
	}
}

// InputHash fingerprints every source/context field a semantic classifier can
// read. It provides provenance without writing an extra copy of that context.
func InputHash(in Input) string {
	raw, err := json.Marshal(in)
	if err != nil {
		panic(fmt.Sprintf("classify: marshal semantic input: %v", err))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func cloneInput(in Input) Input {
	out := in
	out.Turn = cloneRecord(in.Turn)
	out.Prior = append([]stream.Record(nil), in.Prior...)
	for i := range out.Prior {
		out.Prior[i] = cloneRecord(out.Prior[i])
	}
	out.PriorOperatorText = append([]string(nil), in.PriorOperatorText...)
	out.UnresolvedObligations = append([]ObligationRef(nil), in.UnresolvedObligations...)
	if in.ActiveRepair != nil {
		repair := *in.ActiveRepair
		repair.TargetPaths = append([]string(nil), in.ActiveRepair.TargetPaths...)
		out.ActiveRepair = &repair
	}
	return out
}

func cloneRecord(in stream.Record) stream.Record {
	out := in
	if in.Metadata != nil {
		out.Metadata = make(stream.Metadata, len(in.Metadata))
		for k, v := range in.Metadata {
			out.Metadata[k] = v
		}
	}
	out.Actions = append([]stream.Action(nil), in.Actions...)
	for i := range out.Actions {
		out.Actions[i].Targets = append([]string(nil), in.Actions[i].Targets...)
		out.Actions[i].Argv = append([]string(nil), in.Actions[i].Argv...)
	}
	return out
}

// Hybrid keeps the deterministic marker tier on the source path and sends a
// copy to a semantic worker. Its capabilities intentionally remain those of
// the marker tier until a selected semantic completion is applied by replay.
type Hybrid struct {
	Markers Heuristic
	Worker  *Worker
	mu      sync.RWMutex
	epoch   uint64
}

func NewHybrid(semantic Classifier, workers, queue int) (*Hybrid, error) {
	return NewHybridWithDeadline(semantic, workers, queue, 0)
}

// NewHybridWithDeadline creates a fast marker tier plus a bounded semantic
// worker. The deadline applies only to semantic jobs, never to source ingest.
func NewHybridWithDeadline(semantic Classifier, workers, queue int, deadline time.Duration) (*Hybrid, error) {
	w, err := NewWorkerWithDeadline(semantic, workers, queue, deadline)
	if err != nil {
		return nil, err
	}
	return &Hybrid{Worker: w}, nil
}

func (h *Hybrid) Name() string               { return "hybrid" }
func (h *Hybrid) Version() string            { return h.Markers.Version() }
func (h *Hybrid) Hash() string               { return h.Markers.Hash() }
func (h *Hybrid) Capabilities() Capabilities { return h.Markers.Capabilities() }

func (h *Hybrid) Classify(ctx context.Context, in Input) (Result, error) {
	res, err := h.Markers.Classify(ctx, in)
	if err != nil {
		return res, err
	}
	if in.Turn.Text == "" || !worthSending(in) || len(in.Turn.Text) > MaxTurnBytes {
		return res, nil
	}
	h.mu.RLock()
	epoch := h.epoch
	h.mu.RUnlock()
	if _, err := h.Worker.Submit(in.Turn.StreamID, epoch, in); err != nil && !errors.Is(err, ErrSemanticQueueFull) {
		return res, fmt.Errorf("hybrid: submit semantic job: %w", err)
	}
	return res, nil
}

// SetEpoch lets the projector stamp deferred work with the epoch current when
// the source record was accepted. It does not let the worker mutate state.
func (h *Hybrid) SetEpoch(epoch uint64) {
	h.mu.Lock()
	h.epoch = epoch
	h.mu.Unlock()
}

func (h *Hybrid) Results() <-chan Completion { return h.Worker.Results() }
func (h *Hybrid) Operational() Operational   { return h.Worker.Snapshot() }
func (h *Hybrid) Close() error               { return h.Worker.Close() }
