package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/harness"
	"github.com/TGPSKI/flow-indicator/internal/render"
	"github.com/TGPSKI/flow-indicator/internal/store"
)

type smokeSemantic struct{ immediateSemantic }

func (smokeSemantic) Classify(ctx context.Context, in classify.Input) (classify.Result, error) {
	if in.Turn.Text == "validation failure" {
		return classify.Result{}, errors.New("validation fixture: rejected semantic response")
	}
	if in.Turn.Text == "validation wait" {
		<-ctx.Done()
		return classify.Result{}, ctx.Err()
	}
	return immediateSemantic{}.Classify(ctx, in)
}

// This optional gate reads an operator-supplied copy, never the live source.
func TestCopiedLongSessionWatchModes(t *testing.T) {
	path := os.Getenv("FLOW_INDICATOR_TEST_SOURCE_COPY")
	if path == "" {
		t.Skip("set FLOW_INDICATOR_TEST_SOURCE_COPY to a copy of the audited transcript")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const auditBytes = 32504875
	if len(raw) < auditBytes {
		t.Fatalf("copy has %d bytes, need audited prefix %d", len(raw), auditBytes)
	}
	raw = raw[:auditBytes]
	if raw[len(raw)-1] != '\n' {
		t.Fatal("audited prefix is not a complete source boundary")
	}
	records, err := harness.DecodeBytes("claude-code", "copy", "", "audited", raw)
	if err != nil {
		t.Fatal(err)
	}
	operatorTurns := 0
	for _, r := range records {
		if r.IsOperatorTurn() {
			operatorTurns++
		}
	}
	if operatorTurns < 125 {
		t.Fatalf("audited prefix has only %d operator turns", operatorTurns)
	}
	t.Logf("copied prefix: %d bytes, %d canonical records, %d operator turns under current adapters", auditBytes, len(records), operatorTurns)
	for _, mode := range []string{config.ModeHeuristic, config.ModeDeferred, config.ModeHybrid} {
		t.Run(mode, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source-copy.jsonl")
			if err := os.WriteFile(source, raw, 0600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			cfg.Classifier.Mode = mode
			var cls classify.Classifier = classify.Heuristic{}
			var hybrid *classify.Hybrid
			if mode != config.ModeHeuristic {
				hybrid, err = classify.NewHybridWithDeadline(smokeSemantic{}, 1, 1, 80*time.Millisecond)
				if err != nil {
					t.Fatal(err)
				}
				cls = hybrid
			}
			root := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- watchFile(ctx, cfg, root, "claude-code", cls, source, "audited", "", 0, false, display{micro: true})
			}()
			dir := store.SessionDir(root, "audited")
			// Count source facts, so reaching the tail cannot depend on model speed.
			for {
				time.Sleep(200 * time.Millisecond)
				es, readErr := store.ReadKind(dir, event.KindRecordObserved)
				if readErr == nil && len(es) == len(records) {
					break
				}
				select {
				case e := <-done:
					t.Fatalf("watch stopped during bootstrap: %v", e)
				default:
				}
				if ctx.Err() != nil {
					t.Fatal("bootstrap did not reach copied tail")
				}
			}
			if hybrid != nil && hybrid.Operational().Requested != 0 {
				t.Fatal("historical backlog consumed live request capacity")
			}
			appendTurn := func(text string) {
				f, e := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				_, e = fmt.Fprintf(f, "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":%q}}\n", text)
				_ = f.Close()
				if e != nil {
					t.Fatal(e)
				}
			}
			appendTurn("validation failure")
			time.Sleep(300 * time.Millisecond)
			appendTurn("Implement the parser.")
			time.Sleep(300 * time.Millisecond)
			for range 12 {
				appendTurn("validation wait")
			}
			time.Sleep(1400 * time.Millisecond)
			cancel()
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			loaded, e := render.Load(dir)
			if e != nil {
				t.Fatal(e)
			}
			if loaded.OperatorTurns != operatorTurns+14 {
				t.Fatalf("observed operator turns=%d", loaded.OperatorTurns)
			}
			if hybrid != nil {
				s := loaded.Semantic
				if s.Eligible != s.Validated+s.Failed+s.TimedOut+s.Canceled+s.Dropped+s.Pending || s.Pending != 0 {
					t.Fatalf("coverage=%+v", s)
				}
				if s.Failed == 0 || s.TimedOut == 0 || s.Validated == 0 || s.Dropped <= operatorTurns {
					t.Fatalf("smoke did not exercise semantic outcomes: %+v", s)
				}
				es, e := store.ReadKind(dir, event.KindSemanticDisposition)
				if e != nil {
					t.Fatal(e)
				}
				seen := map[uint64]bool{}
				for _, ev := range es {
					var c classify.Completion
					if e := json.Unmarshal(ev.Payload, &c); e != nil {
						t.Fatal(e)
					}
					if c.OperatorTurn {
						seen[c.Seq] = true
					}
				}
				if len(seen) != operatorTurns+14 {
					t.Fatalf("operator dispositions=%d", len(seen))
				}
				t.Logf("%s: source bytes=%d source records=%d coverage=%d validated=%d failed=%d timed_out=%d dropped=%d applied=%d", mode, auditBytes, len(records), s.Eligible, s.Validated, s.Failed, s.TimedOut, s.Dropped, s.Applied)
			}
		})
	}
}
