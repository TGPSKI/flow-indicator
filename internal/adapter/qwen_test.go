package adapter

import (
	"os"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func TestQwenAdapterMapping(t *testing.T) {
	raw, err := os.ReadFile("testdata/qwen.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	recs, err := Qwen{}.Decode(raw, stream.SourceRef{Path: "qwen.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 11 {
		t.Fatalf("records = %d, want 11", len(recs))
	}

	byID := map[string]stream.Record{}
	for _, r := range recs {
		byID[r.TurnID] = r
	}

	cases := []struct {
		id    string
		class stream.SpeakerClass
	}{
		{"u1", stream.SpeakerHuman},
		{"a1", stream.SpeakerAgent},
		{"r1", stream.SpeakerTool},
		{"a2", stream.SpeakerAgent},
		{"r2", stream.SpeakerTool},
		{"s1", stream.SpeakerSystem}, // sidechain task prompt, not the operator
		{"y0", stream.SpeakerHuman},  // slash command invocation
		{"y1", stream.SpeakerSystem}, // slash command result
		{"n1", stream.SpeakerSystem}, // ui telemetry noise
		{"u2", stream.SpeakerHuman},
	}
	for _, c := range cases {
		got, ok := byID[c.id]
		if !ok {
			t.Fatalf("record %s missing", c.id)
		}
		if got.SpeakerClass != c.class {
			t.Errorf("record %s class = %q, want %q", c.id, got.SpeakerClass, c.class)
		}
	}

	if got := byID["a1"].Text; got != "Adding the loop." {
		t.Errorf("assistant text = %q, want the text part only (no thought)", got)
	}
	if got := byID["a1"].Metadata[stream.MetaToolName]; got != "write_file" {
		t.Errorf("assistant tool name = %q, want write_file", got)
	}
	if got := byID["y0"].Text; got != "/model qwen3.8" {
		t.Errorf("slash command text = %q, want the raw command", got)
	}
	if got := byID["y0"].Metadata[stream.MetaInputMode]; got != stream.InputCLICommand {
		t.Errorf("slash command input mode = %q, want %q", got, stream.InputCLICommand)
	}
	if byID["s1"].IsOperatorTurn() {
		t.Error("sub-agent task prompt counted as an operator turn")
	}
	if !byID["u1"].IsOperatorTurn() || !byID["u2"].IsOperatorTurn() {
		t.Error("operator-typed record not counted as an operator turn")
	}

	a1 := byID["a1"]
	if len(a1.Actions) != 1 || a1.Actions[0].Verb != stream.VerbWrite {
		t.Fatalf("a1 actions = %+v, want one write", a1.Actions)
	}
	if got := a1.Actions[0].Targets; len(got) != 1 || got[0] != "retry.go" {
		t.Errorf("a1 write target = %v, want [retry.go] (relative to cwd)", got)
	}
	if a1.Actions[0].Failed {
		t.Error("a1 write action marked failed, want success")
	}

	a2 := byID["a2"]
	if len(a2.Actions) != 1 || a2.Actions[0].Verb != stream.VerbExecute {
		t.Fatalf("a2 actions = %+v, want one execute", a2.Actions)
	}
	if !a2.Actions[0].Failed {
		t.Error("a2 execute action not marked failed, want failed (r2 answered with an error)")
	}

	// Unknown record types survive with provenance and no turn weight.
	var unknown int
	for _, r := range recs {
		if r.SpeakerClass == stream.SpeakerUnknown {
			unknown++
			if r.Metadata[stream.MetaRecordType] != "future-record-kind" {
				t.Errorf("unknown record lost its type: %v", r.Metadata)
			}
			if r.Source.RecordSHA256 == "" {
				t.Error("unknown record lost its provenance")
			}
		}
	}
	if unknown != 1 {
		t.Errorf("unknown-class records = %d, want 1", unknown)
	}
}
