package state

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// wrote returns an agent record that wrote the given paths and said nothing.
// Silence is deliberate: the rule under test reads no words.
func wrote(minute int, paths ...string) stream.Record {
	r := agent("", minute)
	for _, p := range paths {
		r.Actions = append(r.Actions, stream.Action{Verb: stream.VerbWrite, Targets: []string{p}})
	}
	return r
}

// The worked case from the seed corpus, reduced.
//
// The operator's second turn was "install this to userspace instaead of opt and
// make sure i dont lose my .config or other tings when i re-install it". It is
// unmistakably a correction and it carries no correction marker: a correction
// expressed as a redirected instruction shares no vocabulary with the marker
// table. What it does share with a correction is the shape of what followed —
// the agent re-edited the file it had just written.
func TestRepairOpensOnAReEditWithNoCorrectionVocabulary(t *testing.T) {
	p := newProjector()
	push(p,
		operator("i need a script that downloads the latest claude desktop deb and installs it", 0),
		wrote(1, "install-claude-desktop.sh"),
		operator("install this to userspace instaead of opt and make sure i dont lose my .config", 2),
		wrote(3, "install-claude-desktop.sh"),
	)

	repairs := p.Repairs()
	if len(repairs) != 1 {
		t.Fatalf("%d episodes opened, want 1", len(repairs))
	}
	r := repairs[0]
	if !r.Structural {
		t.Error("the episode was attributed to a marker rather than to the re-edit")
	}
	if r.TargetKey != "install-claude-desktop.sh" {
		t.Errorf("target key = %q, want the path both cycles wrote", r.TargetKey)
	}
	if r.FirstCorrectionTurn == 0 {
		t.Error("the episode was not attributed to the operator turn that prompted it")
	}
}

// New work that happens to follow a write is not a correction. The next turn in
// the same session asked for Makefile support, and the cycle that answered it
// wrote a different file.
func TestNewWorkOnADifferentPathOpensNothing(t *testing.T) {
	p := newProjector()
	push(p,
		operator("i need a script that installs the deb", 0),
		wrote(1, "install-claude-desktop.sh"),
		operator("also add the makefile support like the other apps have", 2),
		wrote(3, "Makefile"),
	)

	if n := len(p.Repairs()); n != 0 {
		t.Errorf("%d episodes opened on a cycle that wrote a path the previous one did not", n)
	}
}

// The rule looks back exactly one cycle. A path written, left alone for a turn,
// and written again is ordinary work returning to a file, not the agent
// re-editing what it just produced in answer to what the operator just said.
func TestTheLookbackIsOneCycle(t *testing.T) {
	p := newProjector()
	push(p,
		operator("write the worker", 0),
		wrote(1, "internal/worker.go"),
		operator("now write the docs", 2),
		wrote(3, "docs/worker.md"),
		operator("now add the retry loop", 4),
		wrote(5, "internal/worker.go"),
	)

	if n := len(p.Repairs()); n != 0 {
		t.Errorf("%d episodes opened across a cycle boundary the rule does not look past", n)
	}
}

// The evidence is spent once. A cycle that re-edits the same path four times is
// one correction being answered, not four corrections.
func TestOverlapOpensOneEpisodePerCycle(t *testing.T) {
	p := newProjector()
	push(p,
		operator("write the installer", 0),
		wrote(1, "install.sh"),
		operator("put it in userspace", 2),
		wrote(3, "install.sh"),
		wrote(4, "install.sh"),
		wrote(5, "install.sh"),
	)

	if n := len(p.Repairs()); n != 1 {
		t.Errorf("%d episodes opened from one cycle's repeated writes, want 1", n)
	}
}

// Pollution becomes decidable once actions are observed, and it stays honest
// about the case it cannot decide. A correction that named a path and a cycle
// that wrote it is a verified repair; a write outside it is expansion.
func TestPollutionIsDecidedFromWrites(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("write the worker", 0),
		wrote(1, "internal/worker.go"),
		operator("no, that is wrong, fix internal/worker.go", 2),
		wrote(3, "internal/worker.go"),
		wrote(4, "docs/CHANGELOG.md"),
		operator("thanks, that works", 5),
	)

	status, evidence, repaired := pollutionOf(t, events)
	if repaired == nil || !*repaired {
		t.Fatalf("a write to the path the correction named did not establish repair (status %q: %s)", status, evidence)
	}
	if status != "polluted" {
		t.Errorf("pollution status = %q, want polluted: the cycle repaired the target and also wrote a path nobody asked for", status)
	}
}

// A correction that names no path leaves pollution unknown, and says which
// unknown it is. Substituting the write set for a target the operator never
// named is the defect the whole vocabulary exists to catch.
func TestUnnamedTargetLeavesPollutionUnknown(t *testing.T) {
	p := newProjector()
	events := push(p,
		operator("write the worker", 0),
		wrote(1, "internal/worker.go"),
		operator("no, that is not what i meant", 2),
		wrote(3, "internal/other.go"),
		operator("ok that works", 4),
	)

	status, evidence, _ := pollutionOf(t, events)
	if status != "unknown" {
		t.Errorf("pollution status = %q, want unknown", status)
	}
	if evidence == "" {
		t.Fatal("an unknown assessment carried no premise")
	}
	if !strings.Contains(evidence, "named no path") {
		t.Errorf("the premise does not say which unknown this is: %q", evidence)
	}
}

// pollutionOf returns the last pollution assessment in a run of events. The
// pollution payload shares its kind with an episode's status, so it is picked
// out by carrying a pollution status rather than by the kind alone.
func pollutionOf(t *testing.T, events []event.Event) (status, evidence string, repaired *bool) {
	t.Helper()
	for _, e := range events {
		if e.Kind != event.KindRepairStatus {
			continue
		}
		var pl struct {
			Status         string `json:"pollution_status"`
			Evidence       string `json:"evidence"`
			TargetRepaired *bool  `json:"target_repaired"`
		}
		if err := json.Unmarshal(e.Payload, &pl); err != nil {
			t.Fatalf("decode repair status payload: %v", err)
		}
		if pl.Status != "" {
			status, evidence, repaired = pl.Status, pl.Evidence, pl.TargetRepaired
		}
	}
	return status, evidence, repaired
}
