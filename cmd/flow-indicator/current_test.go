package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/harness"
)

// writeSession writes a minimal session file under root/project and stamps its
// modification time, so selection order is the test's to decide.
func writeSession(t *testing.T, root, project, id, cwd string, sidechain bool, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	body := fmt.Sprintf(
		`{"type":"mode","sessionId":%q}`+"\n"+
			`{"type":"user","sessionId":%q,"cwd":%q,"isSidechain":%t,"message":{"role":"user","content":"go"}}`+"\n",
		id, id, cwd, sidechain)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

// Discovery selects on the working directory the source itself records. The
// encoded project directory name is lossy, and two projects can encode alike.
func TestCurrentSessionMatchesTheRecordedWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "-home-user-git-other", "aaa", "/home/user/git/other", false, time.Minute)
	want := writeSession(t, root, "-home-user-git-mine", "bbb", "/home/user/git/mine", false, 2*time.Minute)

	got, err := findCurrentSession(root, "/home/user/git/mine")
	if err != nil {
		t.Fatal(err)
	}
	if got.Locator != want {
		t.Errorf("chose %s, want %s", got.Locator, want)
	}
	if got.ID != "bbb" {
		t.Errorf("session id = %q, want %q", got.ID, "bbb")
	}
}

// Among sessions for the same directory the one being written now is the one
// the operator is in.
func TestCurrentSessionPrefersTheMostRecentlyModified(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "-p", "old", "/w", false, time.Hour)
	newest := writeSession(t, root, "-p", "new", "/w", false, time.Minute)

	got, err := findCurrentSession(root, "/w")
	if err != nil {
		t.Fatal(err)
	}
	if got.Locator != newest {
		t.Errorf("chose %s, want the most recently modified %s", got.Locator, newest)
	}
}

// A subagent transcript is an agent's stream, not the operator's session.
func TestCurrentSessionSkipsSubagentTranscripts(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "-p", "sub", "/w", true, time.Second)
	want := writeSession(t, root, "-p", "top", "/w", false, time.Hour)

	got, err := findCurrentSession(root, "/w")
	if err != nil {
		t.Fatal(err)
	}
	if got.Locator != want {
		t.Errorf("chose %s, want the top-level session %s", got.Locator, want)
	}
}

// Guessing across unrelated projects is worse than saying nothing matched.
func TestCurrentSessionFailsRatherThanGuessingAnotherProject(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "-p", "elsewhere", "/somewhere/else", false, time.Minute)

	_, err := findCurrentSession(root, "/home/user/git/mine")
	if err == nil {
		t.Fatal("a session from an unrelated project was selected")
	}
	if !strings.Contains(err.Error(), "/home/user/git/mine") {
		t.Errorf("error does not name the directory searched for: %v", err)
	}
}

// Two sessions written seconds apart are two live panes. Picking the newer one
// would be a coin flip, so discovery prints the candidates instead.
func TestCurrentSessionPrintsCandidatesWhenItCannotChoose(t *testing.T) {
	root := t.TempDir()
	a := writeSession(t, root, "-p", "one", "/w", false, 0)
	b := writeSession(t, root, "-p", "two", "/w", false, ambiguityWindow/2)

	_, err := findCurrentSession(root, "/w")
	if err == nil {
		t.Fatal("discovery chose between two sessions written at the same time")
	}
	for _, path := range []string{a, b} {
		if !strings.Contains(err.Error(), path) {
			t.Errorf("candidate %s is missing from the error: %v", path, err)
		}
	}
}

// --current is asked from inside a repo, and the operator may be running any
// harness there. Searching only the one --adapter happens to default to reports
// "no session records this directory" while the session sits in the next
// harness over, or follows a stale Claude transcript from last week.
func TestCurrentSelectsAcrossHarnesses(t *testing.T) {
	now := time.Now()
	candidates := []harness.Session{
		{ID: "stale-claude", Harness: "claude-code", Locator: "/c/stale.jsonl",
			Root: "/w/proj", Modified: now.Add(-7 * 24 * time.Hour)},
		{ID: "live-codex", Harness: "codex", Locator: "/x/rollout.jsonl",
			Root: "/w/proj", Modified: now.Add(-30 * time.Second)},
		{ID: "other-project", Harness: "opencode", Locator: "/db#ses_other",
			Root: "/w/elsewhere", Modified: now},
	}

	got, err := selectCurrent("/w/proj", candidates)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "live-codex" {
		t.Errorf("chose %s from %s, want the most recent session for this directory", got.ID, got.Harness)
	}
}

// A sub-agent thread is an agent's stream. It is excluded whether the source
// named its parent or only marked the transcript as nested.
func TestCurrentSkipsSubagentsHoweverTheyAreMarked(t *testing.T) {
	now := time.Now()
	for _, sub := range []harness.Session{
		{ID: "named-parent", Harness: "codex", Root: "/w", Parent: "p1", Modified: now},
		{ID: "sidechain", Harness: "claude-code", Root: "/w", Sidechain: true, Modified: now},
	} {
		want := harness.Session{ID: "top", Harness: "codex", Root: "/w", Modified: now.Add(-time.Hour)}
		got, err := selectCurrent("/w", []harness.Session{sub, want})
		if err != nil {
			t.Fatalf("%s: %v", sub.ID, err)
		}
		if got.ID != "top" {
			t.Errorf("%s was followed instead of the operator's own session", got.ID)
		}
	}
}

// When it cannot choose, the candidates it prints must be runnable: a locator
// alone is not, because the operator still has to know which harness reads it.
func TestAmbiguityNamesTheHarnessForEachCandidate(t *testing.T) {
	now := time.Now()
	_, err := selectCurrent("/w", []harness.Session{
		{ID: "a", Harness: "codex", Locator: "/x/a.jsonl", Root: "/w", Modified: now},
		{ID: "b", Harness: "opencode", Locator: "/db#ses_b", Root: "/w", Modified: now},
	})
	if err == nil {
		t.Fatal("discovery chose between two sessions written at the same time")
	}
	for _, want := range []string{"--adapter codex", "--adapter opencode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the candidate list does not name %q: %v", want, err)
		}
	}
}
