package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/render"
	"github.com/TGPSKI/flow-indicator/internal/state"
	"github.com/TGPSKI/flow-indicator/internal/store"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func TestLongSemanticProjectionBoundsAndReconstruction(t *testing.T) {
	cfg := config.Default()
	h, err := classify.NewHybrid(immediateSemantic{}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	p := state.New(cfg, h.Markers, "long")
	var records []stream.Record
	var base []event.Event
	var completions []classify.Completion
	sourceBytes := 0
	padding := strings.Repeat("tool output ", 2800)
	for i := 0; i < 1125; i++ {
		r := stream.Record{StreamID: "long", Seq: uint64(i + 1), TurnID: fmt.Sprint(i), SpeakerClass: stream.SpeakerTool, Text: padding}
		if i%9 == 0 {
			r.SpeakerClass = stream.SpeakerHuman
			r.Text = "Implement the parser."
		}
		records = append(records, r)
		sourceBytes += len(r.Text)
		base = append(base, p.Push(context.Background(), r)...)
		if r.IsOperatorTurn() {
			res, _ := immediateSemantic{}.Classify(context.Background(), classify.Input{Turn: r})
			completions = append(completions, classify.Completion{JobID: fmt.Sprint(i), StreamID: "long", Seq: r.Seq, Classifier: h.Semantic().Name(), ClassifierVersion: h.Semantic().Version(), ClassifierHash: h.Semantic().Hash(), Status: classify.CompletionCompleted, Result: &res})
		}
	}
	if sourceBytes < 32*1024*1024 {
		t.Fatalf("fixture only %d bytes", sourceBytes)
	}
	r := buildProjection(1, cfg, "long", h, records, completions, base, 0)
	if r.err != nil {
		t.Fatal(r.err)
	}
	bytesStored, maxLine := 0, 0
	for _, e := range r.updates {
		raw, _ := json.Marshal(e)
		bytesStored += len(raw) + 1
		if len(raw) > maxLine {
			maxLine = len(raw)
		}
	}
	if bytesStored > 64*1024*1024 || maxLine >= 1024*1024 {
		t.Fatalf("projection bytes=%d max line=%d", bytesStored, maxLine)
	}
	again := buildProjection(1, cfg, "long", h, records, completions, base, 0)
	a, _ := json.Marshal(r.updates)
	b, _ := json.Marshal(again.updates)
	if !bytes.Equal(a, b) {
		t.Fatal("retained evidence produced different event bytes")
	}
	partial, err := render.LoadEvents(append(append([]event.Event(nil), base...), r.updates[:len(r.updates)-1]...))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(partial.Final(), p.Snapshot()) {
		t.Fatal("uncommitted projection became visible")
	}
	loaded, err := render.LoadEvents(append(base, r.updates...))
	if err != nil {
		t.Fatal(err)
	}
	want, err := render.LoadEvents(r.events)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Semantic = want.Semantic
	if !reflect.DeepEqual(loaded.Summarize(), want.Summarize()) || loaded.Report() != want.Report() || loaded.Timeline() != want.Timeline() || !reflect.DeepEqual(loaded.Snapshots, want.Snapshots) {
		t.Fatal("delta log did not reproduce all projections")
	}
	if next := projectionDelta(r.p, r.events, r.events, completions, 0, 0); len(next) != 0 {
		t.Fatal("unchanged selection appended history")
	}
	t.Logf("source=%d projection=%d largest line=%d records=%d operator turns=%d", sourceBytes, bytesStored, maxLine, len(records), len(completions))
}

func TestWatchIngestsAndHeartbeatsDuringProjection(t *testing.T) {
	source := filepath.Join(t.TempDir(), "live.jsonl")
	if err := os.WriteFile(source, nil, 0600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg := config.Default()
	cfg.Classifier.Mode = config.ModeHybrid
	h, err := classify.NewHybrid(immediateSemantic{}, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	build := func(v uint64, c config.Config, id string, h *classify.Hybrid, rr []stream.Record, cc []classify.Completion, base []event.Event, prior int) projectionResult {
		once.Do(func() { close(entered); <-release })
		return buildProjection(v, c, id, h, rr, cc, base, prior)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- watchFileWithBuilder(ctx, cfg, root, "generic", h, source, "heartbeat", "", 0, false, display{micro: true}, build)
	}()
	appendTurn := func(text string) {
		t.Helper()
		f, err := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fmt.Fprintf(f, "{\"speaker\":\"user\",\"text\":%q}\n", text)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	appendTurn("Implement the parser.")
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("projection did not start")
	}
	appendTurn("Add parser tests.")
	// A blocked rebuild spans more than two heartbeats. The instance record
	// must continue refreshing while the second source turn is ingested.
	var beats []time.Time
	for range 3 {
		time.Sleep(1050 * time.Millisecond)
		paths, _ := filepath.Glob(filepath.Join(root, "instances", "*.json"))
		if len(paths) != 1 {
			close(release)
			t.Fatalf("instance files=%v", paths)
		}
		info, err := os.Stat(paths[0])
		if err != nil {
			close(release)
			t.Fatal(err)
		}
		beats = append(beats, info.ModTime())
	}
	if !beats[1].After(beats[0]) || !beats[2].After(beats[1]) {
		close(release)
		t.Fatal("instance missed consecutive heartbeats")
	}
	observed, err := store.ReadKind(store.SessionDir(root, "heartbeat"), event.KindRecordObserved)
	if err != nil || len(observed) != 2 {
		close(release)
		t.Fatalf("source ingestion blocked: %d records, %v", len(observed), err)
	}
	close(release)
	time.Sleep(200 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	loaded, err := render.Load(store.SessionDir(root, "heartbeat"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Final().TurnIndex != 2 || loaded.Semantic.Eligible != 2 || loaded.Semantic.Validated != 2 || loaded.Semantic.Applied != 2 {
		t.Fatalf("latest projection/coverage = %+v / %+v", loaded.Final(), loaded.Semantic)
	}
	stats := h.Operational()
	if stats.Eligible != stats.Completed+stats.Failed+stats.TimedOut+stats.Canceled+stats.Dropped+stats.Pending {
		t.Fatalf("coverage does not balance: %+v", stats)
	}
}
