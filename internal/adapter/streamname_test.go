package adapter

import (
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Claude Code names a session on its own record type and rewrites the name as
// the conversation develops. The name is carried as metadata, not as text: the
// harness authored it, and text is measured.
func TestClaudeSessionNameIsMetadataAndLatestWins(t *testing.T) {
	raw := []byte(`{"type":"ai-title","aiTitle":"First guess at the topic","sessionId":"s1"}
{"type":"user","uuid":"u1","sessionId":"s1","message":{"role":"user","content":"Do the thing."}}
{"type":"ai-title","aiTitle":"What the session turned out to be","sessionId":"s1"}
`)
	records, err := Claude{}.Decode(raw, stream.SourceRef{Adapter: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("decoded %d records, want 3", len(records))
	}

	var names []string
	for _, r := range records {
		if n := r.Metadata[stream.MetaStreamName]; n != "" {
			names = append(names, n)
			if r.Text != "" {
				t.Errorf("session name leaked into text: %q", r.Text)
			}
			if r.IsOperatorTurn() {
				t.Error("a name record counted as an operator turn")
			}
		}
	}
	want := []string{"First guess at the topic", "What the session turned out to be"}
	if len(names) != len(want) {
		t.Fatalf("got %d named records, want %d", len(names), len(want))
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("name %d = %q, want %q", i, names[i], want[i])
		}
	}

	// The operator turn between them carries no name of its own, so a reader
	// holding the last one seen keeps the right answer.
	if n := records[1].Metadata[stream.MetaStreamName]; n != "" {
		t.Errorf("operator record carries a name: %q", n)
	}
}

// A message typed while the agent is still working reaches the stream as a
// queued_command attachment and never as a user record. Reading only user
// records loses it — and those turns are disproportionately corrections.
func TestClaudeQueuedMessageIsAnOperatorTurn(t *testing.T) {
	raw := []byte(`{"type":"queue-operation","operation":"enqueue","sessionId":"s1","content":"no, revert that"}
{"type":"attachment","uuid":"a1","sessionId":"s1","attachment":{"type":"queued_command","prompt":"no, revert that","commandMode":"prompt","origin":{"kind":"human"}}}
{"type":"queue-operation","operation":"remove","sessionId":"s1","content":"no, revert that"}
{"type":"attachment","uuid":"a2","sessionId":"s1","attachment":{"type":"queued_command","prompt":"/clear","commandMode":"command","origin":{"kind":"human"}}}
{"type":"attachment","uuid":"a3","sessionId":"s1","attachment":{"type":"queued_command","prompt":"agent wrote this","commandMode":"prompt","origin":{"kind":"agent"}}}
`)
	records, err := Claude{}.Decode(raw, stream.SourceRef{Adapter: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]stream.Record{}
	for _, r := range records {
		byID[r.TurnID] = r
	}

	if got := byID["a1"]; !got.IsOperatorTurn() || got.Text != "no, revert that" {
		t.Errorf("queued message is not an operator turn: operator=%v text=%q",
			got.IsOperatorTurn(), got.Text)
	}
	// A queued slash command is aimed at the harness, not the agent.
	if got := byID["a2"]; got.IsOperatorTurn() {
		t.Error("a queued slash command counted as serialization to the agent")
	}
	// Only the operator's own queued messages count.
	if got := byID["a3"]; got.IsOperatorTurn() {
		t.Error("an agent-origin queued command counted as an operator turn")
	}
	// The queue operations carry the same text and must not double it.
	for _, r := range records {
		if r.Metadata[stream.MetaRecordType] == "queue-operation" && r.IsOperatorTurn() {
			t.Error("a queue-operation record counted as an operator turn, doubling the text")
		}
	}
}
