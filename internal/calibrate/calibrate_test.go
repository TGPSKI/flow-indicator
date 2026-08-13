package calibrate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/labels"
)

// corpus writes a two-turn generic session and a manifest naming it.
func corpus(t *testing.T, split string) (dir string, manifestPath string) {
	t.Helper()
	dir = t.TempDir()
	session := strings.Join([]string{
		`{"stream_id":"S","turn_id":"u1","speaker":"user","text":"write the installer"}`,
		`{"stream_id":"S","turn_id":"a1","speaker":"assistant","text":"","actions":[{"verb":"write","targets":["install.sh"]}]}`,
		`{"stream_id":"S","turn_id":"u2","speaker":"user","text":"put it in userspace instead"}`,
		`{"stream_id":"S","turn_id":"a2","speaker":"assistant","text":"","actions":[{"verb":"write","targets":["install.sh"]}]}`,
	}, "\n") + "\n"
	srcPath := filepath.Join(dir, "S.jsonl")
	if err := os.WriteFile(srcPath, []byte(session), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, err := hashFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{Sessions: []ManifestSession{{
		ID: "S", Path: "S.jsonl", Adapter: "generic", Split: split, SHA256: sum, Domain: "test",
	}}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath = filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, manifestPath
}

func labelDir(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "S.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func run(t *testing.T, manifestPath, labels string, opts func(*Options)) (*Report, error) {
	t.Helper()
	m, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	set, err := loadSet(labels)
	if err != nil {
		t.Fatal(err)
	}
	o := Options{Manifest: m, Labels: set, Config: config.Default(), Split: SplitDev}
	if opts != nil {
		opts(&o)
	}
	return Run(context.Background(), o)
}

func loadSet(dir string) (*labels.Set, error) { return labels.Load(dir) }

// A run record names its own subject. A score whose artifact, corpus, rules and
// labels are not on the page is a number nobody can reproduce.
func TestRunRecordNamesItsSubject(t *testing.T) {
	_, manifestPath := corpus(t, SplitDev)
	ldir := labelDir(t, `{"session":"S","record":"u2","family":"recovery","label":"opens","labeler":"a"}`)

	rep, err := run(t, manifestPath, ldir, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for name, got := range map[string]string{
		"artifact":        rep.Artifact,
		"corpus manifest": rep.ManifestHash,
		"label set":       rep.LabelHash,
		"rule version":    rep.RuleVersion,
		"rule hash":       rep.RuleHash,
	} {
		if got == "" {
			t.Errorf("the run record does not name its %s", name)
		}
	}
	if len(rep.Budget.Parameters) == 0 {
		t.Error("the run record reports no free parameters")
	}
}

func TestRunNamesTheSelectedClassifier(t *testing.T) {
	_, manifestPath := corpus(t, SplitDev)
	ldir := labelDir(t, `{"session":"S","record":"u2","family":"recovery","label":"opens","labeler":"a"}`)
	rep, err := run(t, manifestPath, ldir, func(o *Options) { o.Classifier = classify.None{} })
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Classifier != "none" || rep.RuleVersion != "1" || rep.RuleHash != "none" {
		t.Fatalf("selected classifier provenance = %q %q %q", rep.Classifier, rep.RuleVersion, rep.RuleHash)
	}
}

// The structural recovery rule reaches the labeled correction through the whole
// pipeline: adapter, projector, prediction, alignment, score.
func TestRecoveryScoresEndToEnd(t *testing.T) {
	_, manifestPath := corpus(t, SplitDev)
	ldir := labelDir(t,
		`{"session":"S","record":"u1","family":"recovery","label":"unrelated","labeler":"a"}`,
		`{"session":"S","record":"u2","family":"recovery","label":"opens","labeler":"a"}`,
	)

	rep, err := run(t, manifestPath, ldir, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, s := range rep.Scores {
		if s.Family != labels.FamilyRecovery {
			continue
		}
		if s.TruePositives != 1 {
			t.Errorf("recovery true positives = %d, want 1", s.TruePositives)
		}
		if s.FalsePositives != 0 {
			t.Errorf("recovery false positives = %d, want 0", s.FalsePositives)
		}
		if s.Consequence == "" {
			t.Error("a score was reported with no operator-facing consequence beside it")
		}
		return
	}
	t.Error("no recovery score was produced")
}

// A corpus that changed underneath a score makes the score a number about
// nothing. The manifest seals it, and a mismatch is refused rather than noted.
func TestChangedCorpusIsRefused(t *testing.T) {
	dir, manifestPath := corpus(t, SplitDev)
	ldir := labelDir(t, `{"session":"S","record":"u2","family":"recovery","label":"opens","labeler":"a"}`)

	if err := os.WriteFile(filepath.Join(dir, "S.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, manifestPath, ldir, nil); err == nil {
		t.Error("a corpus that no longer matches its manifest scored cleanly")
	}
}

// Opening the holdout spends budget, and an evaluation that spends budget has to
// say what gate it was spent at.
func TestHoldoutRequiresAReason(t *testing.T) {
	_, manifestPath := corpus(t, SplitHoldout)
	ldir := labelDir(t, `{"session":"S","record":"u2","family":"recovery","label":"opens","labeler":"a"}`)

	if _, err := run(t, manifestPath, ldir, func(o *Options) { o.Split = SplitHoldout }); err == nil {
		t.Error("the holdout opened with no reason recorded")
	}
	if _, err := run(t, manifestPath, ldir, func(o *Options) {
		o.Split = SplitHoldout
		o.Reason = "gate 1: two consecutive dev improvements"
	}); err != nil {
		t.Errorf("a holdout run with a stated reason failed: %v", err)
	}
}

// A dev run must not read a holdout session's labels. Scoring across the seal
// opens the holdout by accident, which is the failure the seal exists to
// prevent.
func TestDevRunIgnoresHoldoutLabels(t *testing.T) {
	_, manifestPath := corpus(t, SplitDev)
	ldir := labelDir(t,
		`{"session":"S","record":"u2","family":"recovery","label":"opens","labeler":"a"}`,
		`{"session":"SEALED","record":"x1","family":"recovery","label":"opens","labeler":"a"}`,
	)

	rep, err := run(t, manifestPath, ldir, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, s := range rep.Scores {
		if s.Family == labels.FamilyRecovery && s.Labeled != 1 {
			t.Errorf("a dev run scored %d labeled units; the sealed session's label was read", s.Labeled)
		}
	}
}

// The units offered to a labeler carry the source and no verdict. A labeler
// shown what the build decided agrees with the build.
func TestEmittedUnitsCarryNoVerdict(t *testing.T) {
	_, manifestPath := corpus(t, SplitDev)
	m, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	units, err := EmitUnits(m, m.Sessions[0], labels.FamilyObligation, SampleTurns(0))
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if len(units) == 0 {
		t.Fatal("no units were emitted")
	}
	for _, u := range units {
		if u.Label != "" || u.Labeler != "" {
			t.Errorf("an emitted unit arrived pre-labeled: %+v", u)
		}
		if u.Text == "" {
			t.Error("an emitted unit carries no source text, so there is nothing to judge")
		}
	}
}
