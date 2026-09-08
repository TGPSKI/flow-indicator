package classify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// ErrSemanticQueueFull says both bounded semantic queues were full. The source
// reader continues, and the lost reading remains visible in the counters.
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
	Attempt           int
	input             Input
	marker            *Result
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
	Attempt           int              `json:"attempt,omitempty"`
	Requested         bool             `json:"requested,omitempty"`
	Changed           bool             `json:"changed,omitempty"`
	OperatorTurn      bool             `json:"operator_turn"`
	Confidence        *float64         `json:"confidence"`
}

const (
	CompletionCompleted = "completed"
	CompletionFailed    = "failed"
	CompletionTimedOut  = "timed_out"
	CompletionCanceled  = "canceled"
	CompletionPending   = "pending"
	CompletionDropped   = "dropped"
)

// Operational is a point-in-time view of the semantic lane. It describes the
// measurement apparatus and must never enter a regime comparison.
type Operational struct {
	Eligible   int    `json:"eligible"`
	Requested  int    `json:"requested"`
	Completed  int    `json:"completed"`
	Failed     int    `json:"failed"`
	TimedOut   int    `json:"timed_out"`
	Canceled   int    `json:"canceled"`
	Dropped    int    `json:"dropped"`
	Pending    int    `json:"pending"`
	LastMS     int64  `json:"last_latency_ms,omitempty"`
	P50MS      int64  `json:"p50_latency_ms,omitempty"`
	P95MS      int64  `json:"p95_latency_ms,omitempty"`
	CatchUp    int    `json:"catch_up"`
	Applied    int    `json:"applied"`
	Changed    int    `json:"changed"`
	LastStatus string `json:"last_status,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

// Worker performs semantic classification away from the source reader. Its
// active and catch-up queues each have the configured queue capacity.
type Worker struct {
	classifier Classifier
	deadline   time.Duration
	jobs       chan Job
	results    chan Completion
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	mu         sync.Mutex
	closed     bool
	stats      Operational
	latency    []int64
	catchUp    []Job
	evidence   []Completion
	unfinished map[string]Completion
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
		unfinished: make(map[string]Completion),
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
// active overflow enters the bounded catch-up lane, and only overflow beyond
// both bounds is lost.
func (w *Worker) Submit(streamID string, epoch uint64, in Input) (Job, error) {
	return w.submit(streamID, epoch, in, nil)
}

func (w *Worker) submit(streamID string, epoch uint64, in Input, marker *Result) (Job, error) {
	job := newJob(streamID, epoch, in, w.classifier)
	job.marker = marker
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return Job{}, errors.New("semantic worker: submit after close")
	}
	c := completionFor(job)
	c.Status = CompletionPending
	w.stats.Eligible++
	select {
	case w.jobs <- job:
		w.evidence = append(w.evidence, c)
		w.unfinished[job.ID] = c
		w.stats.Pending++
		return job, nil
	default:
		if len(w.catchUp) >= cap(w.jobs) {
			w.stats.Dropped++
			c.Status, c.Requested, c.Error = CompletionDropped, false, "semantic queues full"
			w.evidence = append(w.evidence, c)
			return job, ErrSemanticQueueFull
		}
		w.evidence = append(w.evidence, c)
		w.unfinished[job.ID] = c
		w.catchUp = append(w.catchUp, job)
		w.stats.CatchUp++
		return job, nil
	}
}

// Results delivers completions in completion order. Consumers retain source
// order when applying them to a deterministic replay.
func (w *Worker) Results() <-chan Completion { return w.results }

func completionFor(job Job) Completion {
	return Completion{JobID: job.ID, StreamID: job.StreamID, Seq: job.Seq,
		OperatorTurn: job.input.Turn.IsOperatorTurn(),
		Epoch:        job.Epoch, Source: job.Source, InputHash: job.InputHash,
		Classifier: job.Classifier, ClassifierVersion: job.ClassifierVersion,
		ClassifierHash: job.ClassifierHash}
}

// DrainEvidence transfers immutable dispositions and outcomes to the event writer.
func (w *Worker) DrainEvidence() []Completion {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.evidence
	w.evidence = nil
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].Status == CompletionPending && out[j].Status != CompletionPending
	})
	return out
}

func (w *Worker) Drop(streamID string, epoch uint64, in Input, reason string) {
	c := completionFor(newJob(streamID, epoch, in, w.classifier))
	c.Status, c.Error = CompletionDropped, reason
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stats.Eligible++
	w.stats.Dropped++
	w.evidence = append(w.evidence, c)
}

// Snapshot returns counters for a live view. It contains no model judgement.
func (w *Worker) Snapshot() Operational {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.stats
	out.Pending += out.CatchUp
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
	ids := make([]string, 0, len(w.unfinished))
	for id := range w.unfinished {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := w.unfinished[id]
		c.Status, c.Error = CompletionCanceled, "observer stopped before completion"
		w.evidence = append(w.evidence, c)
	}
	w.unfinished = make(map[string]Completion)
	// Jobs still buffered when shutdown begins never reached a classifier. They
	// are cancellations, not pending work after the watcher has ended.
	if w.stats.Pending > 0 || w.stats.CatchUp > 0 {
		w.stats.Canceled += w.stats.Pending + w.stats.CatchUp
		w.stats.Pending = 0
		w.stats.CatchUp = 0
		w.catchUp = nil
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
			if w.ctx.Err() != nil {
				return
			}
			w.mu.Lock()
			pending := w.unfinished[job.ID]
			pending.Requested = true
			w.unfinished[job.ID] = pending
			w.evidence = append(w.evidence, pending)
			w.stats.Requested++
			w.mu.Unlock()
			started := time.Now()
			jobCtx, cancel := w.jobContext()
			res, err := w.classifier.Classify(jobCtx, job.input)
			cancel()
			completion := Completion{
				OperatorTurn: job.input.Turn.IsOperatorTurn(),
				Requested:    true,
				JobID:        job.ID, StreamID: job.StreamID, Seq: job.Seq, Epoch: job.Epoch,
				Source: job.Source, InputHash: job.InputHash,
				Classifier: job.Classifier, ClassifierVersion: job.ClassifierVersion, ClassifierHash: job.ClassifierHash,
				LatencyMS: time.Since(started).Milliseconds(), Attempt: job.Attempt,
			}
			switch {
			case err == nil:
				completion.Status, completion.Result = CompletionCompleted, &res
				completion.Confidence = &res.Confidence
				if job.marker != nil {
					a, b := *job.marker, res
					a.Provenance, b.Provenance = Provenance{}, Provenance{}
					a.Confidence, b.Confidence = 0, 0
					completion.Changed = !reflect.DeepEqual(a, b)
				}
			case errors.Is(err, context.DeadlineExceeded):
				completion.Status, completion.Error = CompletionTimedOut, err.Error()
			case errors.Is(err, context.Canceled):
				completion.Status, completion.Error = CompletionCanceled, err.Error()
			default:
				completion.Status, completion.Error = CompletionFailed, err.Error()
			}
			w.record(completion)
			w.promoteCatchUp()
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
	c.Requested = true
	w.evidence = append(w.evidence, c)
	delete(w.unfinished, c.JobID)
	if w.stats.Pending > 0 {
		w.stats.Pending--
	}
	w.stats.LastMS = c.LatencyMS
	w.stats.LastStatus = c.Status
	w.stats.LastError = c.Error
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

func (w *Worker) promoteCatchUp() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.catchUp) == 0 || w.closed {
		return
	}
	select {
	case w.jobs <- w.catchUp[0]:
		w.catchUp = w.catchUp[1:]
		w.stats.CatchUp--
		w.stats.Pending++
	default:
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
	Markers   Heuristic
	Worker    *Worker
	mu        sync.RWMutex
	epoch     uint64
	bootstrap bool
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
	if !worthSending(in) {
		return res, nil
	}
	h.mu.RLock()
	epoch := h.epoch
	bootstrap := h.bootstrap
	h.mu.RUnlock()
	if bootstrap || in.Turn.Text == "" || len(in.Turn.Text) > MaxTurnBytes {
		reason := "bootstrap: semantic classification starts at the live tail"
		if !bootstrap {
			reason = "turn empty or exceeds semantic byte limit"
		}
		h.Worker.Drop(in.Turn.StreamID, epoch, in, reason)
		return res, nil
	}
	if _, err := h.Worker.submit(in.Turn.StreamID, epoch, in, &res); err != nil && !errors.Is(err, ErrSemanticQueueFull) {
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

func (h *Hybrid) SetBootstrap(bootstrap bool) {
	h.mu.Lock()
	h.bootstrap = bootstrap
	h.mu.Unlock()
}

func (h *Hybrid) Results() <-chan Completion { return h.Worker.Results() }
func (h *Hybrid) Operational() Operational   { return h.Worker.Snapshot() }
func (h *Hybrid) Close() error               { return h.Worker.Close() }

// Semantic reports the immutable classifier behind the worker. Callers may
// use its declared capabilities when projecting a retained completion; they
// must never call it from the source reader.
func (h *Hybrid) Semantic() Classifier { return h.Worker.classifier }

// RecordProjection reports that completed semantic evidence was selected by a
// live projection, and whether that selection changed the visible snapshot.
func (h *Hybrid) RecordProjection(applied, changed int) {
	h.Worker.mu.Lock()
	defer h.Worker.mu.Unlock()
	h.Worker.stats.Applied += applied
	h.Worker.stats.Changed += changed
}
