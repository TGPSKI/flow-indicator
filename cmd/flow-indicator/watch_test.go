package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/store"
)

// Stopping a watcher means observation stopped. It does not mean the operator
// walked away, the repair failed, or the session ended. The interaction may
// still be running in another terminal.
func TestWatchShutdownConcludesNothing(t *testing.T) {
	source := filepath.Join(t.TempDir(), "live.jsonl")
	body := strings.Join([]string{
		`{"speaker":"user","text":"Add the retry loop to internal/worker/retry.go."}`,
		`{"speaker":"assistant","text":"Added it and also updated the release workflow."}`,
		`{"speaker":"user","text":"No. Revert the workflow change."}`,
		"",
	}, "\n")
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	cfg, err := config.Load(writeDefaultConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	// The watcher runs until the signal arrives, exactly as an interrupt does.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- watchFile(ctx, cfg, dataDir, "generic", classify.Heuristic{}, source, "live", "", 0, false, display{micro: true})
	}()
	// Give the tail time to read the whole file before the context ends.
	time.Sleep(500 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("watch: %v", err)
	}

	dir := store.SessionDir(dataDir, "live")
	observations := readOrEmpty(t, filepath.Join(dir, store.FileObservations))
	repairs := readOrEmpty(t, filepath.Join(dir, store.FileRepairs))
	metricEvents := readOrEmpty(t, filepath.Join(dir, store.FileMetrics))

	for _, forbidden := range []string{
		"repair_abandoned", "repair_reset", "repair_durably_closed", "epoch_advanced",
	} {
		for _, body := range []string{observations, repairs, metricEvents} {
			if strings.Contains(body, forbidden) {
				t.Errorf("stopping the watcher emitted %s", forbidden)
			}
		}
	}
	if strings.Contains(metricEvents, `"outcome":"unknown"`) {
		t.Error("stopping the watcher resolved an outstanding pointer to unknown")
	}
	if !strings.Contains(observations, "observation_stopped") {
		t.Fatal("no observation_stopped event was written")
	}

	// The episode the operator opened is still open, because it is.
	if !strings.Contains(repairs, `"status":"open"`) {
		t.Error("the open episode did not survive the observer stopping")
	}

	// The observed event carries what the source established: why watching
	// ended and the byte it reached, so a later watch can start there. The
	// state left open is projected, and travels as a separate derived event.
	stopped := findEvent(t, observations, "observation_stopped")
	if stopped.Class != "observed" {
		t.Errorf("observation_stopped class = %s, want observed", stopped.Class)
	}
	if stopped.Payload.Offset != int64(len(body)) {
		t.Errorf("recorded offset = %d, want %d", stopped.Payload.Offset, len(body))
	}
	if stopped.Payload.OpenRepair != "" || stopped.Payload.PendingPointers != nil {
		t.Error("the observed stop event carries projected state")
	}

	inventory := findEvent(t, metricEvents, "state_at_observation_stop")
	if inventory.Class != "derived" {
		t.Errorf("state_at_observation_stop class = %s, want derived", inventory.Class)
	}
	if inventory.Payload.OpenRepair == "" {
		t.Error("the derived inventory does not name the episode left open")
	}
	if inventory.Payload.PendingPointers == nil {
		t.Error("the derived inventory does not report the references left unresolved")
	}
}

// stopEvent is the part of a stored event these assertions read.
type stopEvent struct {
	Kind    string `json:"kind"`
	Class   string `json:"class"`
	Payload struct {
		Offset          int64  `json:"source_offset"`
		OpenRepair      string `json:"open_repair"`
		PendingPointers *int   `json:"pending_pointers"`
	} `json:"payload"`
}

func findEvent(t *testing.T, body, kind string) stopEvent {
	t.Helper()
	var found bool
	var out stopEvent
	for line := range strings.SplitSeq(strings.TrimSpace(body), "\n") {
		var e stopEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil || e.Kind != kind {
			continue
		}
		out, found = e, true
	}
	if !found {
		t.Fatalf("no %s event was written", kind)
	}
	return out
}
