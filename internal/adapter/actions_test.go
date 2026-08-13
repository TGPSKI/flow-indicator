package adapter

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// decodeFile runs an adapter over a testdata file.
func decodeFile(t *testing.T, ad Adapter, name string) []stream.Record {
	t.Helper()
	path := filepath.Join("testdata", "actions", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	recs, err := ad.Decode(raw, stream.SourceRef{Adapter: ad.Name(), Path: path})
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return recs
}

// flatActions flattens every record's actions into one sequence.
func flatActions(recs []stream.Record) []stream.Action {
	var out []stream.Action
	for _, r := range recs {
		out = append(out, r.Actions...)
	}
	return out
}

// The action vocabulary is the seam every rule reads through. Two sources
// describing the same session in their own formats must arrive at the same
// sequence, or a rule that holds on one harness is a rule about that harness.
//
// The fixtures are the same session written twice: Claude Code's own JSONL, and
// the generic format stating the vocabulary directly.
func TestAdaptersAgreeOnTheActionSequence(t *testing.T) {
	fromClaude := flatActions(decodeFile(t, Claude{}, "claude.jsonl"))
	fromGeneric := flatActions(decodeFile(t, Generic{}, "generic.jsonl"))

	if !reflect.DeepEqual(fromClaude, fromGeneric) {
		t.Errorf("adapters disagree on the action sequence\nclaude-code: %+v\ngeneric:     %+v", fromClaude, fromGeneric)
	}

	want := []stream.Action{
		{Verb: stream.VerbRead, Targets: []string{"internal/worker.go"}},
		{Verb: stream.VerbSearch, Targets: []string{"internal"}},
		{Verb: stream.VerbWrite, Targets: []string{"internal/worker.go"}},
		{Verb: stream.VerbExecute, Argv: []string{"go test ./internal/worker"}, Failed: true},
		{Verb: stream.VerbWrite, Targets: []string{"/tmp/scratch.txt"}},
		{Verb: stream.VerbUnknown},
	}
	if !reflect.DeepEqual(fromClaude, want) {
		t.Errorf("action sequence = %+v, want %+v", fromClaude, want)
	}
}

// A path under the session root is relative to it, so the same file named from
// two sessions of one project compares equal and no absolute home directory is
// carried. A path outside the root keeps its absolute form, because being
// outside is the fact a scope rule reads.
func TestTargetsAreRelativeToTheSessionRoot(t *testing.T) {
	actions := flatActions(decodeFile(t, Claude{}, "claude.jsonl"))

	var inside, outside bool
	for _, a := range actions {
		for _, tg := range a.Targets {
			switch tg {
			case "internal/worker.go":
				inside = true
			case "/tmp/scratch.txt":
				outside = true
			}
		}
	}
	if !inside {
		t.Error("a path under the session root was not made relative to it")
	}
	if !outside {
		t.Error("a path outside the session root did not keep its absolute form")
	}
}

// An unrecognized tool is counted under the unknown verb and never guessed into
// another one. A call mapped to the wrong verb is worse than one mapped to none:
// the write set is what pollution and scope violation are decided from.
func TestUnrecognizedToolIsCountedNotGuessed(t *testing.T) {
	for _, a := range flatActions(decodeFile(t, Claude{}, "claude.jsonl")) {
		if a.Verb != stream.VerbUnknown {
			continue
		}
		if len(a.Targets) != 0 || len(a.Argv) != 0 {
			t.Errorf("an unrecognized call carried %+v; nothing about it was established", a)
		}
		return
	}
	t.Error("the unrecognized call produced no action at all, so it is invisible rather than counted")
}

// A generic source that names a verb outside the vocabulary does not get to
// extend it. Accepting the name would let a source put targets into a write set
// on its own say-so.
func TestGenericVerbOutsideTheVocabularyBecomesUnknown(t *testing.T) {
	actions := flatActions(decodeFile(t, Generic{}, "generic.jsonl"))
	last := actions[len(actions)-1]
	if last.Verb != stream.VerbUnknown {
		t.Errorf("an invented verb decoded as %q", last.Verb)
	}
}
