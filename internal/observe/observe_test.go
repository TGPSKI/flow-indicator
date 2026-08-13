package observe

import (
	"reflect"
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func human(text string, ts string) stream.Record {
	r := stream.Record{SpeakerClass: stream.SpeakerHuman, Text: text, Metadata: stream.Metadata{}}
	if ts != "" {
		r.Timestamp, _ = time.Parse(time.RFC3339, ts)
	}
	return r
}

func TestCounts(t *testing.T) {
	cases := []struct {
		text                       string
		bytes, chars, words, lines int
	}{
		{"", 0, 0, 0, 0},
		{"stop after CI", 13, 13, 3, 1},
		{"one\ntwo\n", 8, 8, 2, 3},
		{"é", 2, 1, 1, 1},
	}
	for _, c := range cases {
		var o Observation
		countChars(c.text, &o)
		got := [4]int{o.Bytes, o.Chars, o.Words, o.Lines}
		want := [4]int{c.bytes, c.chars, c.words, c.lines}
		if got != want {
			t.Errorf("countChars(%q) = %v, want %v", c.text, got, want)
		}
	}
}

func TestQuotedChars(t *testing.T) {
	text := "look at this:\n```\nFAIL retry_test.go:91\n```\nand `go vet` output"
	o := New(30)
	obs := o.Observe(human(text, ""))
	if obs.QuotedChars == 0 {
		t.Fatal("fenced block and inline span counted as zero quoted chars")
	}
	if obs.QuotedFraction <= 0 || obs.QuotedFraction > 1 {
		t.Fatalf("quoted fraction = %v, want (0,1]", obs.QuotedFraction)
	}
}

func TestRepeatCharsCountsPriorOperatorLines(t *testing.T) {
	o := New(30)
	o.Observe(human("stop after CI passes", ""))
	o.Observe(human("add the backoff test", ""))
	obs := o.Observe(human("stop after CI passes", ""))
	if obs.RepeatChars != 20 {
		t.Fatalf("repeat chars = %d, want 20", obs.RepeatChars)
	}
	if obs.RepeatFraction != 1 {
		t.Fatalf("repeat fraction = %v, want 1", obs.RepeatFraction)
	}
}

func TestShortLinesAreNotRepeats(t *testing.T) {
	o := New(30)
	o.Observe(human("run it", ""))
	obs := o.Observe(human("run it", ""))
	if obs.RepeatChars != 0 {
		t.Fatalf("repeat chars = %d, want 0 for a line below the repeat floor", obs.RepeatChars)
	}
}

func TestAgentTurnsCarryCountsOnly(t *testing.T) {
	o := New(30)
	obs := o.Observe(stream.Record{SpeakerClass: stream.SpeakerAgent, Text: "done", Metadata: stream.Metadata{}})
	if obs.OperatorTurn {
		t.Fatal("agent record marked as an operator turn")
	}
	if obs.Chars != 4 {
		t.Fatalf("chars = %d, want 4", obs.Chars)
	}
	if obs.RepeatChars != 0 || obs.LexicalOverlapPrevious != 0 {
		t.Fatal("operator-only observations computed for an agent record")
	}
}

func TestGapsUnknownWithoutTimestamps(t *testing.T) {
	o := New(30)
	first := o.Observe(human("first turn here", ""))
	if first.GapSecondsPrevious != nil {
		t.Fatal("gap reported for a record with no timestamp")
	}
	o2 := New(30)
	o2.Observe(human("first turn here", "2026-03-02T09:00:00Z"))
	second := o2.Observe(human("second turn here", "2026-03-02T09:02:00Z"))
	if second.GapSecondsPrevious == nil || *second.GapSecondsPrevious != 120 {
		t.Fatalf("gap = %v, want 120", second.GapSecondsPrevious)
	}
}

func TestObserveIsDeterministic(t *testing.T) {
	texts := []string{
		"stop after CI passes",
		"add the backoff test",
		"stop after CI passes and wait",
		"```\nlog line\n```",
	}
	run := func() []Observation {
		o := New(30)
		var out []Observation
		for i, tx := range texts {
			out = append(out, o.Observe(human(tx, time.Unix(int64(i)*60, 0).UTC().Format(time.RFC3339))))
		}
		return out
	}
	if !reflect.DeepEqual(run(), run()) {
		t.Fatal("two observation passes over the same input differ")
	}
}

func TestResetClearsRollingHistory(t *testing.T) {
	o := New(30)
	o.Observe(human("stop after CI passes", ""))
	o.Reset()
	obs := o.Observe(human("stop after CI passes", ""))
	if obs.RepeatChars != 0 {
		t.Fatalf("repeat chars = %d after reset, want 0", obs.RepeatChars)
	}
}
