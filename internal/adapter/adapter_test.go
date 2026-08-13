package adapter

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func TestNewUnknownAdapter(t *testing.T) {
	if _, err := New("nope"); err == nil {
		t.Fatal("want error for unknown adapter name")
	}
}

func TestGenericDecodeFixture(t *testing.T) {
	raw, err := os.ReadFile("../../fixtures/healthy/input.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	recs, err := Generic{}.Decode(raw, stream.SourceRef{Path: "input.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 28 {
		t.Fatalf("records = %d, want 28", len(recs))
	}
	if recs[0].SpeakerClass != stream.SpeakerHuman {
		t.Fatalf("record 1 class = %q, want human", recs[0].SpeakerClass)
	}
	if !recs[0].IsOperatorTurn() {
		t.Fatal("record 1 is not counted as an operator turn")
	}
	if recs[1].SpeakerClass != stream.SpeakerAgent {
		t.Fatalf("record 2 class = %q, want agent", recs[1].SpeakerClass)
	}
	if recs[0].Source.RecordSHA256 == "" || recs[0].Source.Adapter != "generic" {
		t.Fatalf("provenance missing: %+v", recs[0].Source)
	}
	if recs[0].Seq != 1 || recs[27].Seq != 28 {
		t.Fatalf("seq not 1-based contiguous: %d..%d", recs[0].Seq, recs[27].Seq)
	}
}

// Offsets must be absolute and identical whether the file is decoded whole or
// in chunks. A resumed tail depends on this.
func TestGenericChunkedDecodeKeepsOffsets(t *testing.T) {
	raw, err := os.ReadFile("../../fixtures/bounded-rescue/input.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	whole, err := Generic{}.Decode(raw, stream.SourceRef{})
	if err != nil {
		t.Fatal(err)
	}

	split := bytes.IndexByte(raw[len(raw)/2:], '\n') + len(raw)/2 + 1
	head, err := Generic{}.Decode(raw[:split], stream.SourceRef{})
	if err != nil {
		t.Fatal(err)
	}
	tail, err := Generic{}.Decode(raw[split:], stream.SourceRef{Offset: int64(split)})
	if err != nil {
		t.Fatal(err)
	}
	if len(head)+len(tail) != len(whole) {
		t.Fatalf("chunked record count = %d, whole = %d", len(head)+len(tail), len(whole))
	}
	for i, r := range append(head, tail...) {
		if r.Source.Offset != whole[i].Source.Offset {
			t.Fatalf("record %d offset = %d, want %d", i, r.Source.Offset, whole[i].Source.Offset)
		}
		if r.Source.RecordSHA256 != whole[i].Source.RecordSHA256 {
			t.Fatalf("record %d hash differs across chunk boundary", i)
		}
	}
}

func TestGenericMalformedLineReportsOffset(t *testing.T) {
	raw := []byte("{\"speaker\":\"user\",\"text\":\"ok\"}\n{not json}\n")
	_, err := Generic{}.Decode(raw, stream.SourceRef{Offset: 1000})
	if err == nil {
		t.Fatal("want decode error")
	}
	if got := err.Error(); !bytes.Contains([]byte(got), []byte("offset 1031")) {
		t.Fatalf("error does not name the source offset: %s", got)
	}
}

func TestClaudeAdapterMapping(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	recs, err := Claude{}.Decode(raw, stream.SourceRef{Path: "claude.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 10 {
		t.Fatalf("records = %d, want 10", len(recs))
	}

	byID := map[string]stream.Record{}
	for _, r := range recs {
		byID[r.TurnID] = r
	}

	cases := []struct {
		id    string
		class stream.SpeakerClass
	}{
		{"u0", stream.SpeakerSystem}, // isMeta
		{"u1", stream.SpeakerHuman},
		{"a1", stream.SpeakerAgent},
		{"r1", stream.SpeakerTool}, // tool_result carried on a user record
		{"s1", stream.SpeakerHuman},
		{"y1", stream.SpeakerSystem},
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
		t.Errorf("assistant text = %q, want the text block only (no thinking, no tool_use)", got)
	}
	if got := byID["a1"].Metadata[stream.MetaToolName]; got != "Edit" {
		t.Errorf("assistant tool name = %q, want Edit", got)
	}
	if byID["s1"].IsOperatorTurn() {
		t.Error("sidechain human record counted as an operator turn")
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

func TestClaudeDecodeIsDeterministic(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	first, err := Claude{}.Decode(raw, stream.SourceRef{Path: "claude.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Claude{}.Decode(raw, stream.SourceRef{Path: "claude.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("two decodes of the same bytes produced different records")
	}
}
