package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/metrics"
	"github.com/TGPSKI/flow-indicator/internal/render"
	"github.com/TGPSKI/flow-indicator/internal/store"
)

// expectation is the load-bearing part of a fixture's expect.json. Absent
// fields are not checked: a fixture asserts what it exists to prove.
type expectation struct {
	Records                *int     `json:"records"`
	HumanTurns             *int     `json:"human_turns"`
	Epochs                 *uint64  `json:"epochs"`
	Corrections            *int     `json:"corrections"`
	RepairEpisodes         *int     `json:"repair_episodes"`
	MaxRepairDepth         *int     `json:"max_repair_depth"`
	MinObligationRepeats   *int     `json:"min_obligation_repeat_events"`
	MinObligationReleases  *int     `json:"min_obligation_release_events"`
	MinObligationRevivals  *int     `json:"min_obligation_revival_events"`
	MinInventoryDecreases  *int     `json:"min_inventory_decreases"`
	MinSerializationInfl   *float64 `json:"min_serialization_inflation"`
	MinMaxHumanTurnChars   *int     `json:"min_max_human_turn_chars"`
	MinPollutedAssessments *int     `json:"min_polluted_assessments"`
	MinCleanAssessments    *int     `json:"min_clean_assessments"`
	MinUnknownAssessments  *int     `json:"min_unknown_assessments"`
	MinRepairClaims        *int     `json:"min_repair_claims"`
	RepairStatuses         []string `json:"repair_statuses"`
	RegimesSeen            []string `json:"regimes_seen"`
	RegimesAbsent          []string `json:"regimes_absent"`
	FinalRegime            *string  `json:"final_regime"`
}

func fixtures() []string {
	return []string{"healthy", "recent-thrash", "bounded-rescue", "large-data-dump", "released-obligation"}
}

// replayFixture runs the replay command exactly as an operator would.
func replayFixture(t *testing.T, name, dataDir string, extra ...string) string {
	t.Helper()
	args := append([]string{
		"--adapter", "generic",
		"--data-dir", dataDir,
		"--config", writeDefaultConfig(t),
		"--quiet",
	}, extra...)
	args = append(args, filepath.Join("..", "..", "fixtures", name, "input.jsonl"))
	if err := replay(args); err != nil {
		t.Fatalf("replay %s: %v", name, err)
	}
	return store.SessionDir(dataDir, name)
}

// writeDefaultConfig writes the documented defaults so a test never reads the
// operator's own configuration.
func writeDefaultConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{
  "window": {"rolling_turns": 30, "correction_durability_turns": 10, "dereference_outcome_turns": 3},
  "thresholds": {"minimum_control_baseline": 8, "thrash_repair_depth": 3, "serialization_warn": 3.0, "serialization_high": 6.0, "repeated_obligations_warn": 2},
  "classifier": {"mode": "heuristic", "endpoint": "", "model": ""},
  "privacy": {"store_text": false, "store_snippets": true, "snippet_chars": 160}
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGoldenReplay(t *testing.T) {
	for _, name := range fixtures() {
		t.Run(name, func(t *testing.T) {
			dir := replayFixture(t, name, t.TempDir())
			session, err := render.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			var want expectation
			raw, err := os.ReadFile(filepath.Join("..", "..", "fixtures", name, "expect.json"))
			if err != nil {
				t.Fatal(err)
			}
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&want); err != nil {
				t.Fatalf("decode expect.json: %v", err)
			}
			check(t, session, want)
		})
	}
}

func check(t *testing.T, s *render.Session, want expectation) {
	t.Helper()
	sum := s.Summarize()

	if want.Records != nil && sum.Records != *want.Records {
		t.Errorf("records = %d, want %d", sum.Records, *want.Records)
	}
	if want.HumanTurns != nil && sum.OperatorTurns != *want.HumanTurns {
		t.Errorf("operator turns = %d, want %d", sum.OperatorTurns, *want.HumanTurns)
	}
	if want.Epochs != nil && sum.Epochs != *want.Epochs {
		t.Errorf("epochs = %d, want %d", sum.Epochs, *want.Epochs)
	}
	if want.Corrections != nil && sum.Corrections != *want.Corrections {
		t.Errorf("corrections = %d, want %d", sum.Corrections, *want.Corrections)
	}
	if want.RepairEpisodes != nil && len(sum.Repairs) != *want.RepairEpisodes {
		t.Errorf("repair episodes = %d, want %d", len(sum.Repairs), *want.RepairEpisodes)
	}
	if want.MaxRepairDepth != nil && sum.MaxDepth != *want.MaxRepairDepth {
		t.Errorf("max repair depth = %d, want %d", sum.MaxDepth, *want.MaxRepairDepth)
	}
	if want.MinObligationRepeats != nil && sum.ObligationRepeats < *want.MinObligationRepeats {
		t.Errorf("obligation repeat events = %d, want at least %d", sum.ObligationRepeats, *want.MinObligationRepeats)
	}
	if want.MinObligationReleases != nil && sum.ObligationReleases < *want.MinObligationReleases {
		t.Errorf("obligation release events = %d, want at least %d", sum.ObligationReleases, *want.MinObligationReleases)
	}
	if want.MinObligationRevivals != nil && sum.ObligationRevivals < *want.MinObligationRevivals {
		t.Errorf("obligation revival events = %d, want at least %d", sum.ObligationRevivals, *want.MinObligationRevivals)
	}
	// The inventory has to be able to go down. A count that only rises stops
	// carrying information after the first few turns, whatever it is counting.
	if want.MinInventoryDecreases != nil {
		var drops int
		for i := 1; i < len(s.Snapshots); i++ {
			if s.Snapshots[i].UnresolvedObligations < s.Snapshots[i-1].UnresolvedObligations {
				drops++
			}
		}
		if drops < *want.MinInventoryDecreases {
			t.Errorf("the unresolved inventory fell %d times, want at least %d", drops, *want.MinInventoryDecreases)
		}
	}
	if want.FinalRegime != nil && sum.FinalRegime != *want.FinalRegime {
		t.Errorf("final regime = %s, want %s", sum.FinalRegime, *want.FinalRegime)
	}

	seen := map[string]bool{}
	for _, r := range sum.RegimesSeen {
		seen[r] = true
	}
	for _, r := range want.RegimesSeen {
		if !seen[r] {
			t.Errorf("regime %s never reached; saw %v", r, sum.RegimesSeen)
		}
	}
	for _, r := range want.RegimesAbsent {
		if seen[r] {
			t.Errorf("regime %s was reached and should not have been; saw %v", r, sum.RegimesSeen)
		}
	}

	if want.RepairStatuses != nil {
		var got []string
		for _, r := range sum.Repairs {
			got = append(got, r.Status)
		}
		if strings.Join(got, ",") != strings.Join(want.RepairStatuses, ",") {
			t.Errorf("repair statuses = %v, want %v", got, want.RepairStatuses)
		}
	}

	polluted, clean, unknown := 0, 0, 0
	for _, history := range sum.PollutionByRepair {
		for _, status := range history {
			switch status {
			case "polluted":
				polluted++
			case "clean":
				clean++
			case "unknown":
				unknown++
			}
		}
	}
	if want.MinPollutedAssessments != nil && polluted < *want.MinPollutedAssessments {
		t.Errorf("polluted assessments = %d, want at least %d", polluted, *want.MinPollutedAssessments)
	}
	if want.MinCleanAssessments != nil && clean < *want.MinCleanAssessments {
		t.Errorf("clean assessments = %d, want at least %d", clean, *want.MinCleanAssessments)
	}
	if want.MinUnknownAssessments != nil && unknown < *want.MinUnknownAssessments {
		t.Errorf("unknown assessments = %d, want at least %d", unknown, *want.MinUnknownAssessments)
	}

	var claims int
	for _, r := range sum.Repairs {
		if r.RepairClaimed {
			claims++
		}
	}
	if want.MinRepairClaims != nil && claims < *want.MinRepairClaims {
		t.Errorf("episodes with an agent repair claim = %d, want at least %d", claims, *want.MinRepairClaims)
	}

	var maxSI float64
	var maxChars int
	for _, snap := range s.Snapshots {
		if snap.SerializationInfl.Known && snap.SerializationInfl.Num > maxSI {
			maxSI = snap.SerializationInfl.Num
		}
		if snap.UserChars > maxChars {
			maxChars = snap.UserChars
		}
	}
	if want.MinSerializationInfl != nil && maxSI < *want.MinSerializationInfl {
		t.Errorf("peak serialization inflation = %.2f, want at least %.2f", maxSI, *want.MinSerializationInfl)
	}
	if want.MinMaxHumanTurnChars != nil && maxChars < *want.MinMaxHumanTurnChars {
		t.Errorf("largest operator turn = %d chars, want at least %d", maxChars, *want.MinMaxHumanTurnChars)
	}
}

// The six metric families must separate a healthy stream from a degraded one.
func TestFixturesAreDistinguishable(t *testing.T) {
	dir := t.TempDir()
	healthy, err := render.Load(replayFixture(t, "healthy", dir))
	if err != nil {
		t.Fatal(err)
	}
	thrash, err := render.Load(replayFixture(t, "recent-thrash", dir))
	if err != nil {
		t.Fatal(err)
	}

	if healthy.MaxRepairDepth() >= thrash.MaxRepairDepth() {
		t.Errorf("repair depth does not separate the fixtures: healthy %d, thrash %d",
			healthy.MaxRepairDepth(), thrash.MaxRepairDepth())
	}
	if healthy.Corrections >= thrash.Corrections {
		t.Errorf("correction count does not separate the fixtures: healthy %d, thrash %d",
			healthy.Corrections, thrash.Corrections)
	}
	if healthy.ObligationRepeats >= thrash.ObligationRepeats {
		t.Errorf("obligation repeats do not separate the fixtures: healthy %d, thrash %d",
			healthy.ObligationRepeats, thrash.ObligationRepeats)
	}
	if peakBurden(healthy) >= peakBurden(thrash) {
		t.Errorf("control-plane burden does not separate the fixtures: healthy %.2f, thrash %.2f",
			peakBurden(healthy), peakBurden(thrash))
	}
	if healthy.Summarize().Epochs >= thrash.Summarize().Epochs {
		t.Error("epoch count does not separate the fixtures")
	}
}

func peakBurden(s *render.Session) float64 {
	var max float64
	for _, snap := range s.Snapshots {
		if snap.ControlBurden.Known && snap.ControlBurden.Num > max {
			max = snap.ControlBurden.Num
		}
	}
	return max
}

// Replaying the same source twice must produce byte-identical event files.
func TestReplayIsByteStable(t *testing.T) {
	for _, name := range fixtures() {
		t.Run(name, func(t *testing.T) {
			first := replayFixture(t, name, t.TempDir())
			second := replayFixture(t, name, t.TempDir())
			for _, file := range []string{
				store.FileObservations, store.FileClassifications, store.FileObligations,
				store.FileRepairs, store.FileMetrics, store.FileReport, store.FileTimeline,
			} {
				a := readOrEmpty(t, filepath.Join(first, file))
				b := readOrEmpty(t, filepath.Join(second, file))
				if a != b {
					t.Fatalf("%s differs between replays", file)
				}
			}
		})
	}
}

func readOrEmpty(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Every displayed metric must be traceable to source turns.
func TestInspectTracesAMetricToItsSource(t *testing.T) {
	dir := replayFixture(t, "recent-thrash", t.TempDir())
	session, err := render.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var correctionTurn uint64
	for _, snap := range session.Snapshots {
		if snap.RepairDepth == 1 {
			correctionTurn = snap.Seq
			break
		}
	}
	if correctionTurn == 0 {
		t.Fatal("no turn opened a repair episode")
	}
	out, err := session.Inspect(correctionTurn)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"source", "sha256", "observed", "classified", "correction_candidate", "repair_opened", "metric contribution"} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect output does not mention %q", want)
		}
	}
}

func TestReportIsGeneratedFromStoredEvents(t *testing.T) {
	dir := replayFixture(t, "bounded-rescue", t.TempDir())
	session, err := render.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	report := session.Report()
	if !strings.Contains(report, "durably_closed") {
		t.Error("report omits the repair status")
	}
	if report != session.Report() {
		t.Error("two renders of the same session differ")
	}
	stored := readOrEmpty(t, filepath.Join(dir, store.FileReport))
	if stored != report {
		t.Error("report.md does not match a fresh render of the same events")
	}
}

func TestReplaySemanticSessionRefusesIncompleteCoverage(t *testing.T) {
	dataDir := t.TempDir()
	semantic, err := store.OpenSession(dataDir, "semantic-input", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := semantic.Close(); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "semantic.json")
	configBody := `{
  "window": {"rolling_turns": 30, "correction_durability_turns": 10, "dereference_outcome_turns": 3, "trend_durability_turns": 2},
  "thresholds": {"minimum_control_baseline": 8, "thrash_repair_depth": 3, "serialization_warn": 3.0, "serialization_high": 6.0, "repeated_obligations_warn": 2},
  "classifier": {"mode": "hybrid", "endpoint": "http://127.0.0.1:8000/v1/chat/completions", "model": "local"},
  "privacy": {"store_text": false, "store_snippets": true, "snippet_chars": 160}
}`
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	err = replay([]string{
		"--adapter", "generic", "--data-dir", dataDir, "--config", configPath,
		"--stream-id", "strict-output", "--semantic-session", semantic.Dir, "--quiet",
		filepath.Join("..", "..", "fixtures", "healthy", "input.jsonl"),
	})
	if err == nil || !strings.Contains(err.Error(), "persisted semantic result is missing") {
		t.Fatalf("replay error = %v, want incomplete persisted coverage", err)
	}
	if _, err := os.Stat(store.SessionDir(dataDir, "strict-output")); !os.IsNotExist(err) {
		t.Fatalf("strict replay created output before coverage was proven: stat error = %v", err)
	}
}

func TestInstancesListsOnlyLiveOperationalRecords(t *testing.T) {
	empty, err := liveInstances(t.TempDir(), time.Now().UTC())
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty instances = %#v, %v", empty, err)
	}
	root := t.TempDir()
	emit, err := newInstanceEmitter(root, "stream-live", "generic", "/tmp/source.jsonl", "/work", "pane-1")
	if err != nil {
		t.Fatal(err)
	}
	defer emit.Close()
	if err := emit.Report(instanceState{Regime: "FLOW", Elapsed: "1m", Mark: "byte 10", Snapshot: metrics.Snapshot{
		TurnIndex: 3, Seq: 8, Epoch: 1, SerializationInfl: metrics.KnownValue(1.2),
	}}); err != nil {
		t.Fatal(err)
	}
	items, err := liveInstances(filepath.Join(root, "instances"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].StreamID != "stream-live" || items[0].Metrics.SerializationInflation.Num != 1.2 {
		t.Fatalf("instances = %+v", items)
	}
	items[0].ExpiresAt = time.Now().UTC().Add(-time.Second)
	if instanceLive(items[0], items[0].Hostname, time.Now().UTC()) {
		t.Fatal("expired instance reported live")
	}
}

// An existing session is not appended to twice: the event files are the source
// of truth and doubling them would double every count.
func TestReplayRefusesToAppendToAnExistingSession(t *testing.T) {
	dir := t.TempDir()
	replayFixture(t, "healthy", dir)
	err := replay([]string{
		"--adapter", "generic", "--data-dir", dir, "--config", writeDefaultConfig(t), "--quiet",
		filepath.Join("..", "..", "fixtures", "healthy", "input.jsonl"),
	})
	if err == nil {
		t.Fatal("second replay into the same session was allowed")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error does not name the way forward: %v", err)
	}
}

func TestReplayRejectsCorruptSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.jsonl")
	if err := os.WriteFile(path, []byte("{\"speaker\":\"user\",\"text\":\"ok\"}\n{oops}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := replay([]string{"--adapter", "generic", "--data-dir", t.TempDir(), "--config", writeDefaultConfig(t), "--quiet", path})
	if err == nil {
		t.Fatal("corrupt source was accepted")
	}
	if !strings.Contains(err.Error(), "offset") {
		t.Errorf("error does not name the source offset: %v", err)
	}
}

func TestReplayRejectsUnknownAdapter(t *testing.T) {
	err := replay([]string{"--adapter", "nope", "--data-dir", t.TempDir(), "--quiet",
		filepath.Join("..", "..", "fixtures", "healthy", "input.jsonl")})
	if err == nil {
		t.Fatal("unknown adapter was accepted")
	}
}

// The fixture inputs are frozen. A fixture that exposes a questionable result
// is a question about the implementation, not an input to reshape until the
// number comes out differently.
func TestFixtureInputsAreFrozen(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	var checked int
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		want, rel, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("malformed SHA256SUMS line: %q", line)
		}
		body, err := os.ReadFile(filepath.Join("..", "..", "fixtures", filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(body)); got != want {
			t.Errorf("%s changed:\n  frozen %s\n  now    %s", rel, want, got)
		}
		checked++
	}
	if checked != len(fixtures()) {
		t.Errorf("SHA256SUMS covers %d inputs, but %d fixtures replay", checked, len(fixtures()))
	}
}
