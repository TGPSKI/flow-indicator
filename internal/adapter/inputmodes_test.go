package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Claude Code records more than typing under its user record type. The shapes
// below were read from a real session file; each is evidence of a different
// act, and conflating them corrupts a different metric.
func TestClaudeInputModes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "claude-input-modes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	records, err := Claude{}.Decode(raw, stream.SourceRef{Adapter: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]stream.Record{}
	for _, r := range records {
		byID[r.TurnID] = r
	}

	for _, tc := range []struct {
		id        string
		class     stream.SpeakerClass
		mode      string
		operator  bool
		why       string
		wantText  string
		wantEmpty bool
	}{
		{id: "m1", class: stream.SpeakerHuman, mode: stream.InputTyped, operator: true,
			why: "ordinary typed instruction", wantText: "Implement the retry loop in Golang."},
		{id: "m3", class: stream.SpeakerHuman, mode: stream.InputStructuredAnswer, operator: true,
			why:      "the operator answered a question the agent asked",
			wantText: "Issue templates, CI workflow + CODEOWNERS\nGPL-3.0 (matches the sibling library)"},
		{id: "m4", class: stream.SpeakerHuman, mode: stream.InputShellEscape, operator: false,
			why: "typed at a shell, not at the agent"},
		{id: "m5", class: stream.SpeakerTool, operator: false,
			why: "shell output the operator never typed"},
		{id: "m6", class: stream.SpeakerHuman, mode: stream.InputCLICommand, operator: false,
			why: "a command to the harness"},
		{id: "m7", class: stream.SpeakerTool, operator: false,
			why: "slash-command output"},
		{id: "m10", class: stream.SpeakerSystem, mode: stream.InputCompactSummary, operator: false,
			why: "the harness composed the context summary; the operator typed only the command"},
		{id: "m8", class: stream.SpeakerSystem, mode: stream.InputInterrupt, operator: false,
			why: "the CLI wrote the marker; the operator authored no text"},
		{id: "m9", class: stream.SpeakerHuman, mode: stream.InputTyped, operator: true,
			why: "typing resumes after the interruption"},
	} {
		rec, ok := byID[tc.id]
		if !ok {
			t.Errorf("record %s is missing", tc.id)
			continue
		}
		if rec.SpeakerClass != tc.class {
			t.Errorf("%s (%s): speaker class = %s, want %s", tc.id, tc.why, rec.SpeakerClass, tc.class)
		}
		if got := rec.Metadata[stream.MetaInputMode]; got != tc.mode {
			t.Errorf("%s (%s): input mode = %q, want %q", tc.id, tc.why, got, tc.mode)
		}
		if rec.IsOperatorTurn() != tc.operator {
			t.Errorf("%s (%s): operator turn = %v, want %v", tc.id, tc.why, rec.IsOperatorTurn(), tc.operator)
		}
		if tc.wantText != "" && rec.Text != tc.wantText {
			t.Errorf("%s (%s): text = %q, want %q", tc.id, tc.why, rec.Text, tc.wantText)
		}
	}
}

// The answers are the operator's. The questions were the agent's, and charging
// the operator for the agent's prose would inflate every transmission metric.
func TestStructuredAnswerCarriesOnlyTheAnswers(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "claude-input-modes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	records, err := Claude{}.Decode(raw, stream.SourceRef{Adapter: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.TurnID != "m3" {
			continue
		}
		for _, unwanted := range []string{
			"Which license for flow-indicator?",
			"Your questions have been answered",
			"Same as the sibling library",
		} {
			if strings.Contains(r.Text, unwanted) {
				t.Errorf("structured answer carries agent-authored text: %q", unwanted)
			}
		}
		return
	}
	t.Fatal("record m3 is missing")
}

// An "answers" object is not on its own evidence that the operator answered
// anything. The record is read as operator input only when it answers a call of
// the tool that asks the operator questions, and the source establishes which
// tool that was.
func TestStructuredAnswerNeedsTheOriginatingTool(t *testing.T) {
	const body = `{"parentUuid":null,"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]},"uuid":"a1","sessionId":"linkage"}
{"parentUuid":"a1","type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"graded"}]},"toolUseResult":{"answers":{"q":"graded"}},"uuid":"u1","sessionId":"linkage"}
{"parentUuid":"u1","type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"AskUserQuestion","input":{}}]},"uuid":"a2","sessionId":"linkage"}
{"parentUuid":"a2","type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"ok"}]},"toolUseResult":{"answers":{"q":"chosen"}},"uuid":"u2","sessionId":"linkage"}
`
	records, err := Claude{}.Decode([]byte(body), stream.SourceRef{Adapter: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]stream.Record{}
	for _, r := range records {
		byID[r.TurnID] = r
	}

	other := byID["u1"]
	if other.IsOperatorTurn() {
		t.Error("a Bash result carrying an answers object was counted as operator serialization")
	}
	if other.SpeakerClass != stream.SpeakerTool {
		t.Errorf("unlinked answers record: speaker class = %s, want %s", other.SpeakerClass, stream.SpeakerTool)
	}

	asked := byID["u2"]
	if !asked.IsOperatorTurn() {
		t.Error("an answer to the tool that asks the operator questions was not counted as operator input")
	}
	if asked.Text != "chosen" {
		t.Errorf("linked answers text = %q, want %q", asked.Text, "chosen")
	}
}

// Answers arrive keyed by the agent's question, and a JSON object carries no
// order. The join is lexical by key so that a replay produces the same text; it
// is not the order the questions were asked, and nothing may read it as such.
func TestStructuredAnswerJoinsInLexicalKeyOrder(t *testing.T) {
	const body = `{"parentUuid":null,"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"AskUserQuestion","input":{}}]},"uuid":"a1","sessionId":"order"}
{"parentUuid":"a1","type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]},"toolUseResult":{"answers":{"zebra question":"first asked","alpha question":"second asked"}},"uuid":"u1","sessionId":"order"}
`
	records, err := Claude{}.Decode([]byte(body), stream.SourceRef{Adapter: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.TurnID != "u1" {
			continue
		}
		if got, want := r.Text, "second asked\nfirst asked"; got != want {
			t.Fatalf("answer text = %q, want %q: the join is lexical by question key", got, want)
		}
		return
	}
	t.Fatal("record u1 is missing")
}

// Machine output must never reach the operator's character count.
func TestCommandOutputIsNotOperatorSerialization(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "claude-input-modes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	records, err := Claude{}.Decode(raw, stream.SourceRef{Adapter: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	var operatorChars int
	for _, r := range records {
		if r.IsOperatorTurn() {
			operatorChars += len(r.Text)
		}
	}
	// Typed instruction + structured answer only.
	const want = len("Implement the retry loop in Golang.") +
		len("Issue templates, CI workflow + CODEOWNERS\nGPL-3.0 (matches the sibling library)") +
		len("Now add the backoff test.")
	if operatorChars != want {
		t.Errorf("operator characters = %d, want %d; machine output entered the count", operatorChars, want)
	}
}

// A compaction summary is tens of thousands of characters the harness wrote.
// Counting it as operator serialization made a routine compaction the largest
// steering turn of a session: in 6a2581f9 three of them carried 50,616
// characters and drove serialization inflation to 450x and the final regime to
// THRASH.
func TestCompactSummaryIsNotOperatorSerialization(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "claude-input-modes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	records, err := Claude{}.Decode(raw, stream.SourceRef{Adapter: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.TurnID != "m10" {
			continue
		}
		if r.IsOperatorTurn() {
			t.Error("the compaction summary counts as an operator turn")
		}
		if r.SpeakerClass != stream.SpeakerSystem {
			t.Errorf("speaker class = %s, want %s", r.SpeakerClass, stream.SpeakerSystem)
		}
		if got := r.Metadata[stream.MetaInputMode]; got != stream.InputCompactSummary {
			t.Errorf("input mode = %q, want %q", got, stream.InputCompactSummary)
		}
		return
	}
	t.Fatal("the compaction summary record was not decoded")
}

// A queued message with something attached arrives as an array of content
// blocks rather than as a string. Both shapes are the same act — the operator
// typed a message while the agent was working — and a decoder that handles only
// one of them abandons the whole chunk on the other.
//
// Only the text is the operator's. An attached image rides in the same array as
// base64, and counting it as operator serialization would charge tens of
// thousands of characters to one paste.
func TestQueuedPromptWithAnAttachment(t *testing.T) {
	line := `{"type":"attachment","uuid":"q1","sessionId":"s","attachment":{"type":"queued_command",` +
		`"commandMode":"prompt","origin":{"kind":"human"},"prompt":[` +
		`{"type":"text","text":"resize breaks the ui"},` +
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` +
		strings.Repeat("A", 4096) + `"}}]}}`

	recs, err := Claude{}.Decode([]byte(line), stream.SourceRef{})
	if err != nil {
		t.Fatalf("a queued message with an attachment failed the whole chunk: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("%d records, want 1", len(recs))
	}
	r := recs[0]
	if !r.IsOperatorTurn() {
		t.Error("a queued message the operator typed is not an operator turn")
	}
	if r.Text != "resize breaks the ui" {
		t.Errorf("text = %q, want the typed words alone", r.Text)
	}
	if len(r.Text) > 100 {
		t.Errorf("the attachment's bytes reached the operator's character count: %d characters", len(r.Text))
	}
}
