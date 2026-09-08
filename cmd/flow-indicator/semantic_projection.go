package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/state"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

type projectionResult struct {
	version  uint64
	p        *state.Projector
	events   []event.Event
	updates  []event.Event
	selected int
	changed  int
	err      error
}

func bySequence(events []event.Event) map[uint64][]event.Event {
	out := make(map[uint64][]event.Event)
	for _, e := range events {
		out[e.Seq] = append(out[e.Seq], e)
	}
	return out
}

// projectionDelta serializes only changed source records, in bounded parts.
// A commit names the new evidence; source arrivals never duplicate old history.
func projectionDelta(p *state.Projector, before, after []event.Event, completions []classify.Completion, applied, changed int) []event.Event {
	old, next := bySequence(before), bySequence(after)
	seqs := make([]uint64, 0, len(next))
	for seq := range next {
		seqs = append(seqs, seq)
	}
	for seq := range old {
		if _, ok := next[seq]; !ok {
			seqs = append(seqs, seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	identity := semanticProjectionIdentity(completions)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", p.Seq(), identity)))
	revision := hex.EncodeToString(sum[:])
	var out []event.Event
	part := 0
	for _, seq := range seqs {
		a, _ := json.Marshal(old[seq])
		b, _ := json.Marshal(next[seq])
		if bytes.Equal(a, b) {
			continue
		}
		remaining := next[seq]
		first := true
		for {
			n, size := 0, 0
			for n < len(remaining) {
				raw, _ := json.Marshal(remaining[n])
				if n > 0 && size+len(raw) > 256*1024 {
					break
				}
				size += len(raw)
				n++
			}
			payload := state.SemanticProjectionDelta{Revision: revision, Part: part, SourceSeq: seq, First: first, Events: remaining[:n]}
			out = append(out, event.NewWithIdentity(p.StreamID(), p.Seq(), p.Epoch(), stream.SourceRef{Adapter: "projector"}, event.KindSemanticProjectionDelta, event.ClassDerived, fmt.Sprintf("%s:%d", revision, part), payload))
			part++
			first = false
			remaining = remaining[n:]
			if len(remaining) == 0 {
				break
			}
		}
	}
	ids := make([]string, 0, applied)
	for _, c := range completions {
		if c.Status == classify.CompletionCompleted && c.Result != nil {
			ids = append(ids, c.JobID)
		}
	}
	if applied < len(ids) {
		ids = ids[len(ids)-applied:]
	}
	sort.Strings(ids)
	if part == 0 && applied == 0 {
		return nil
	}
	out = append(out, event.NewWithIdentity(p.StreamID(), p.Seq(), p.Epoch(), stream.SourceRef{Adapter: "projector"}, event.KindSemanticProjectionUpdated, event.ClassDerived, revision, state.SemanticProjectionUpdate{Revision: revision, Parts: part, CompletionIDs: ids, Applied: applied, Changed: changed}))
	return out
}

func buildProjection(version uint64, cfg config.Config, id string, hybrid *classify.Hybrid, records []stream.Record, completions []classify.Completion, baseline []event.Event, priorSelected int) projectionResult {
	r := projectionResult{version: version}
	r.p, r.events, r.err = semanticProjection(cfg, id, hybrid, records, completions)
	if r.err != nil {
		return r
	}
	r.selected = completedCount(completions)
	seen := 0
	for _, c := range completions {
		if c.Status != classify.CompletionCompleted || c.Result == nil {
			continue
		}
		seen++
		if seen > priorSelected && c.Changed {
			r.changed++
		}
	}
	r.updates = projectionDelta(r.p, baseline, r.events, completions, r.selected-priorSelected, r.changed)
	return r
}
