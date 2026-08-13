package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// writeOverlay puts an overlay beside a configuration file and returns that
// file's path.
func writeOverlay(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "profile.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// An overlay beside the configuration file reaches the classifier. This is the
// whole point of the profile mechanism, and it went unwired for one release
// because nothing above internal/profile asserted it.
func TestOverlayReachesTheClassifier(t *testing.T) {
	cfgPath := writeOverlay(t, `{"name":"test","markers":{"correction_tier1":{"phrases":["bzzt"]}}}`)
	common := commonFlags{configPath: cfgPath}
	rules, err := common.rules()
	if err != nil {
		t.Fatal(err)
	}
	if !rules.Match("correction_tier1", "bzzt, not like that") {
		t.Error("the overlay's phrase does not match under the resolved ruleset")
	}
	if !rules.Match("correction_tier1", "that is wrong") {
		t.Error("the baseline's phrases were lost; the overlay must add, not replace")
	}

	res, err := (classify.Heuristic{Rules: rules}).Classify(context.Background(), classify.Input{
		Turn: stream.Record{Seq: 1, SpeakerClass: stream.SpeakerHuman, Text: "bzzt, not like that"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Correction.IsCorrection {
		t.Error("a turn carrying only the overlay's marker was not read as a correction")
	}
}

// The rule hash separates sessions classified under an overlay from sessions
// classified under the baseline. A shared hash would merge two lexicons in one
// calibration score.
func TestOverlayChangesTheRuleHash(t *testing.T) {
	cfgPath := writeOverlay(t, `{"name":"test","markers":{"correction_tier1":{"phrases":["bzzt"]}}}`)
	overlaid := commonFlags{configPath: cfgPath}
	rules, err := overlaid.rules()
	if err != nil {
		t.Fatal(err)
	}
	bare := commonFlags{configPath: filepath.Join(t.TempDir(), "config.json")}
	baseline, err := bare.rules()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := (classify.Heuristic{Rules: rules}).Hash(), (classify.Heuristic{Rules: baseline}).Hash(); got == want {
		t.Errorf("an overlay left the rule hash at %s", got)
	}
}

// Every tier reads the same ruleset. The marker tier is the fast half of both
// semantic modes, so an overlay that reached one tier and not the other would
// put two lexicons in one session.
func TestEveryTierReadsTheOverlay(t *testing.T) {
	cfgPath := writeOverlay(t, `{"name":"test","markers":{"correction_tier1":{"phrases":["bzzt"]}}}`)
	common := commonFlags{configPath: cfgPath}
	rules, err := common.rules()
	if err != nil {
		t.Fatal(err)
	}
	want := (classify.Heuristic{Rules: rules}).Hash()

	cfg := config.Default()
	cfg.Classifier.Endpoint = "http://127.0.0.1:1/v1/chat/completions"
	cfg.Classifier.Model = "test-model"

	cfg.Classifier.Mode = config.ModeHeuristic
	cls, err := classifierFor(cfg, rules, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := cls.Hash(); got != want {
		t.Errorf("heuristic mode classifies under %s, not the overlay's %s", got, want)
	}

	cfg.Classifier.Mode = config.ModeOpenAI
	cls, err = classifierFor(cfg, rules, false)
	if err != nil {
		t.Fatal(err)
	}
	semantic, ok := cls.(*classify.OpenAI)
	if !ok {
		t.Fatalf("openai-compatible mode returned %T", cls)
	}
	if got := semantic.Markers.Hash(); got != want {
		t.Errorf("the semantic tier's markers classify under %s, not the overlay's %s", got, want)
	}

	cfg.Classifier.Mode = config.ModeHybrid
	cls, err = classifierFor(cfg, rules, true)
	if err != nil {
		t.Fatal(err)
	}
	hybrid, ok := cls.(*classify.Hybrid)
	if !ok {
		t.Fatalf("hybrid mode returned %T", cls)
	}
	defer hybrid.Close()
	if got := hybrid.Hash(); got != want {
		t.Errorf("the hybrid marker tier classifies under %s, not the overlay's %s", got, want)
	}
}

// An overlay the operator named must exist; the default one need not. A
// misspelled --profile that silently classified under the baseline would be
// reported as a result the overlay produced.
func TestMissingOverlay(t *testing.T) {
	named := commonFlags{
		configPath:  filepath.Join(t.TempDir(), "config.json"),
		profilePath: filepath.Join(t.TempDir(), "absent.json"),
	}
	if _, err := named.rules(); err == nil {
		t.Error("a --profile path that does not exist was accepted")
	}
	unnamed := commonFlags{configPath: filepath.Join(t.TempDir(), "config.json")}
	if _, err := unnamed.rules(); err != nil {
		t.Errorf("no overlay beside the configuration file is not an error: %v", err)
	}
}
