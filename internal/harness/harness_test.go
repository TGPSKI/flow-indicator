package harness

import (
	"fmt"
	"sort"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Every harness owes the same contract, and no fixture proves it. The contract
// is about live stores: that operator turns come out as operator turns, that
// actions land in the closed verb vocabulary, and that nothing a harness cannot
// establish is invented. This decodes the busiest session each harness holds on
// this machine and checks that much.
//
// A harness the operator has never used is skipped, not failed. Not having run
// Codex is not a defect in this program.
func TestEachHarnessDecodesItsBusiestSession(t *testing.T) {
	type result struct {
		session                 Session
		records, turns, actions int
		verbs                   map[stream.ActionVerb]int
	}

	for _, name := range Names() {
		h, err := New(name)
		if err != nil {
			t.Fatal(err)
		}
		root, err := h.DefaultRoot()
		if err != nil {
			t.Logf("%-12s no default root: %v", name, err)
			continue
		}
		sessions, err := h.Discover(root)
		if err != nil {
			t.Logf("%-12s not readable here: %v", name, err)
			continue
		}
		var top []Session
		for _, s := range sessions {
			if !s.IsSubagent() {
				top = append(top, s)
			}
		}
		if len(top) == 0 {
			t.Logf("%-12s no sessions under %s", name, root)
			continue
		}

		var best result
		for _, s := range top {
			recs, err := h.Open(s)
			if err != nil {
				continue
			}
			r := result{session: s, records: len(recs), verbs: map[stream.ActionVerb]int{}}
			for _, rec := range recs {
				if rec.IsOperatorTurn() {
					r.turns++
				}
				r.actions += len(rec.Actions)
				for _, a := range rec.Actions {
					r.verbs[a.Verb]++
				}
			}
			if r.turns > best.turns {
				best = r
			}
		}

		verbs := make([]string, 0, len(best.verbs))
		for v, n := range best.verbs {
			verbs = append(verbs, fmt.Sprintf("%s:%d", v, n))
		}
		sort.Strings(verbs)
		t.Logf("%-12s %4d sessions (%d top-level); busiest %d records, %d operator turns, %d actions %v",
			name, len(sessions), len(top), best.records, best.turns, best.actions, verbs)

		if best.records == 0 {
			t.Errorf("%s decoded no records from any of its %d sessions", name, len(top))
			continue
		}
		if best.turns == 0 {
			t.Errorf("%s found no operator turn in any session; every transmission metric divides by that count", name)
		}
		for v := range best.verbs {
			switch v {
			case stream.VerbRead, stream.VerbWrite, stream.VerbExecute,
				stream.VerbSearch, stream.VerbAsk, stream.VerbUnknown:
			default:
				t.Errorf("%s produced the verb %q, which is outside the vocabulary", name, v)
			}
		}
	}
}

// A sub-agent thread is not an operator's session: the turns in it were written
// by an agent. Counting them would report an operator who steers far more than
// they do, so every harness has to be able to tell them apart.
func TestSubagentThreadsAreIdentifiable(t *testing.T) {
	for _, name := range Names() {
		h, _ := New(name)
		root, err := h.DefaultRoot()
		if err != nil {
			continue
		}
		sessions, err := h.Discover(root)
		if err != nil || len(sessions) == 0 {
			continue
		}
		var sub int
		for _, s := range sessions {
			if s.IsSubagent() {
				sub++
				// A sub-agent is excluded either because the source named its
				// parent or because the transcript marks itself as a nested
				// stream. One of the two has to be true, or IsSubagent is
				// reporting on nothing.
				if s.Parent == "" && !s.Sidechain {
					t.Errorf("%s reported a sub-agent session with neither a parent nor a sidechain mark", name)
				}
			}
		}
		t.Logf("%-12s %d of %d sessions are sub-agent threads", name, sub, len(sessions))
	}
}

// The registry is what makes a harness addable without touching anything else.
func TestRegistryHoldsTheFourHarnesses(t *testing.T) {
	want := map[string]bool{"claude-code": true, "codex": true, "opencode": true, "qwen": true}
	for _, name := range Names() {
		delete(want, name)
	}
	if len(want) != 0 {
		t.Errorf("harnesses missing from the registry: %v", want)
	}
	if _, err := New("nope"); err == nil {
		t.Error("an unregistered harness resolved")
	}
}
