package calibrate

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSessions lays out a fake harness directory: project -> session files.
func writeSessions(t *testing.T, layout map[string][]string) string {
	t.Helper()
	root := t.TempDir()
	// One operator turn per line, enough of them to clear any minimum a test sets.
	var body string
	for i := range 20 {
		body += `{"type":"user","uuid":"u` + string(rune('a'+i%26)) + `","sessionId":"S","cwd":"/p","message":{"role":"user","content":"do the thing"}}` + "\n"
	}
	for project, sessions := range layout {
		dir := filepath.Join(root, project)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, s := range sessions {
			if err := os.WriteFile(filepath.Join(dir, s+".jsonl"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Backdate everything past the quiet period.
	old := time.Now().Add(-48 * time.Hour)
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			_ = os.Chtimes(p, old, old)
		}
		return nil
	})
	return root
}

func scan(t *testing.T, root string, min int) []SessionStat {
	t.Helper()
	stats, err := Scan(root, ScanOptions{MinOperatorTurns: min, QuietFor: time.Hour})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return Candidates(stats)
}

// The split is a function of the identifiers and nothing else. A split chosen by
// a person is one that can be chosen again when the first gives a disappointing
// number, and a holdout that can be reshuffled is not a holdout.
func TestTheSplitIsDeterministic(t *testing.T) {
	root := writeSessions(t, map[string][]string{
		"alpha": {"a1", "a2", "a3"},
		"beta":  {"b1", "b2"},
		"gamma": {"g1"},
	})
	c := scan(t, root, 5)

	first := Assign(c, nil, 0)
	second := Assign(c, nil, 0)
	if len(first) != len(second) {
		t.Fatalf("two deals produced %d and %d sessions", len(first), len(second))
	}
	for i := range first {
		if first[i].ID != second[i].ID || first[i].Split != second[i].Split {
			t.Errorf("the deal is not reproducible at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
}

// A session already in the manifest keeps the split it had. One that has been
// iterated against cannot become holdout by being rescanned, and a holdout
// session that has been opened stays spent.
func TestPriorAssignmentsAreNotReshuffled(t *testing.T) {
	root := writeSessions(t, map[string][]string{"alpha": {"a1", "a2"}})
	c := scan(t, root, 5)

	first := Assign(c, nil, 0)
	// Force every prior entry to the opposite side, then re-deal.
	prior := &Manifest{}
	for _, s := range first {
		flipped := s
		flipped.Split = SplitDev
		if s.Split == SplitDev {
			flipped.Split = SplitHoldout
		}
		prior.Sessions = append(prior.Sessions, flipped)
	}
	second := Assign(c, prior, 0)

	byID := map[string]string{}
	for _, s := range second {
		byID[s.ID] = s.Split
	}
	for _, s := range prior.Sessions {
		if byID[s.ID] != s.Split {
			t.Errorf("session %s was reshuffled from %s to %s", s.ID, s.Split, byID[s.ID])
		}
	}
}

// Projects with one session must not all land on the same side. Most projects
// have one session, and dealing every project from the same parity sends all of
// them to dev — the holdout then holds only projects dev has also seen, and the
// question it exists to answer is one it cannot ask.
func TestSingleSessionProjectsReachBothSides(t *testing.T) {
	layout := map[string][]string{}
	for _, p := range []string{"one", "two", "three", "four", "five", "six", "seven", "eight"} {
		layout[p] = []string{p + "-only"}
	}
	c := scan(t, writeSessions(t, layout), 5)
	assigned := Assign(c, nil, 0)

	var dev, holdout int
	for _, s := range assigned {
		if s.Split == SplitHoldout {
			holdout++
		} else {
			dev++
		}
	}
	if holdout == 0 {
		t.Error("every single-session project landed in dev; the holdout contains no unseen project")
	}
	if dev == 0 {
		t.Error("every single-session project landed in holdout")
	}
}

// The per-project cap keeps one project's vocabulary from dominating.
func TestPerProjectCapIsHonoured(t *testing.T) {
	root := writeSessions(t, map[string][]string{"big": {"s1", "s2", "s3", "s4", "s5"}})
	assigned := Assign(scan(t, root, 5), nil, 2)

	if len(assigned) != 2 {
		t.Errorf("%d sessions taken from one project, want the cap of 2", len(assigned))
	}
}

// A scan reports what it skipped as well as what it took. A corpus built by
// silently discarding candidates cannot be audited.
func TestSkippedSessionsAreReportedNotDropped(t *testing.T) {
	root := writeSessions(t, map[string][]string{"alpha": {"a1"}})
	stats, err := Scan(root, ScanOptions{MinOperatorTurns: 1000, QuietFor: time.Hour})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("%d sessions reported, want the skipped one to still appear", len(stats))
	}
	if stats[0].Skipped == "" {
		t.Error("a session below the minimum was reported as a candidate")
	}
	if len(Candidates(stats)) != 0 {
		t.Error("a skipped session was offered as a candidate")
	}
}

// A file still being written cannot be sealed by a hash, and it may belong to a
// session running in another terminal right now.
func TestRecentlyWrittenSessionsAreSkipped(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "alpha")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"u1","sessionId":"S","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "live.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	stats, err := Scan(root, ScanOptions{MinOperatorTurns: 0, QuietFor: time.Hour})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(stats) != 1 || stats[0].Skipped == "" {
		t.Errorf("a session written seconds ago was taken as a candidate: %+v", stats)
	}
}

// The sample is drawn over turns and is a function of the record identifier, so
// the same hand comes back every time and nobody chose it. The same turn is
// selected for every family, which is the point: one reading pass yields all
// three.
func TestTurnSampleIsDeterministicAndFamilyIndependent(t *testing.T) {
	pick := SampleTurns(250)
	var first []bool
	for i := range 400 {
		first = append(first, pick(fmt.Sprintf("record-%d", i)))
	}
	again := SampleTurns(250)
	for i := range 400 {
		if again(fmt.Sprintf("record-%d", i)) != first[i] {
			t.Fatalf("the sample is not reproducible at record-%d", i)
		}
	}

	var taken int
	for _, ok := range first {
		if ok {
			taken++
		}
	}
	// A hash is not a balanced deal; the check is that it lands near the rate
	// rather than on it, which is what makes the sample a sample.
	if taken < 60 || taken > 140 {
		t.Errorf("%d of 400 turns sampled at 250 per mille; the selection is not near-uniform", taken)
	}
}

// Zero takes everything, so a small corpus can be labeled whole.
func TestZeroSampleTakesEveryTurn(t *testing.T) {
	pick := SampleTurns(0)
	for i := range 50 {
		if !pick(fmt.Sprintf("record-%d", i)) {
			t.Fatal("a zero sample rate dropped a turn")
		}
	}
}
