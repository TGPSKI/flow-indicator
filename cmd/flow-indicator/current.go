package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/harness"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// tailFromEnd starts observation at the end of the file.
const tailFromEnd = stream.TailFromEnd

// Current-session discovery.
//
// The live product runs beside an agent IDE, in the terminal the operator is
// already in. Asking them to find a session file and a UUID first is friction
// the meter does not survive. This finds the session being recorded for this
// working directory, and says which one it picked.
//
// It asks every harness, not the one whose flag happens to be the default. An
// operator running Codex in a repo and told "no Claude Code session records
// this directory" has been given a true sentence and no help; worse, a stale
// Claude session from last week matches and gets followed instead.
//
// It reads. It does not watch every session, keep a registry, or run a daemon.

// ambiguityWindow is how close two candidates' modification times may be before
// discovery refuses to choose. Two sessions written this close together are two
// live panes, and picking the newer one would be a coin flip.
const ambiguityWindow = 2 * time.Second

// selectCurrent picks the session recorded for dir out of candidates.
//
// Selection is by the working directory the source itself records, not by any
// encoded directory name, which is lossy. Among matches the most recently
// modified wins, unless two are too close in time to tell apart.
func selectCurrent(dir string, candidates []harness.Session) (harness.Session, error) {
	var matches []harness.Session
	for _, s := range candidates {
		if s.IsSubagent() || s.Root != dir {
			continue
		}
		matches = append(matches, s)
	}
	if len(matches) == 0 {
		return harness.Session{}, fmt.Errorf("no session records the working directory %s", dir)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Modified.After(matches[j].Modified) })
	if len(matches) > 1 && matches[0].Modified.Sub(matches[1].Modified) < ambiguityWindow {
		var b strings.Builder
		fmt.Fprintf(&b, "%s has more than one session written in the last %s; pass the one to follow\n",
			dir, ambiguityWindow)
		for _, s := range matches {
			if matches[0].Modified.Sub(s.Modified) < ambiguityWindow {
				fmt.Fprintf(&b, "  --adapter %s %s  (%s)\n",
					s.Harness, s.Locator, s.Modified.UTC().Format(time.RFC3339))
			}
		}
		return harness.Session{}, errors.New(strings.TrimRight(b.String(), "\n"))
	}
	return matches[0], nil
}

// findCurrentSession returns the Claude Code session recorded for dir under a
// given projects root.
func findCurrentSession(root, dir string) (harness.Session, error) {
	found, err := harness.Claude{}.Discover(root)
	if err != nil {
		return harness.Session{}, err
	}
	for i := range found {
		found[i].Harness = harness.Claude{}.Name()
	}
	s, err := selectCurrent(dir, found)
	if err != nil {
		return harness.Session{}, fmt.Errorf("watch: %w", err)
	}
	return s, nil
}

// currentSession resolves --current to the session recorded for this working
// directory.
//
// only names a single harness to search when the operator asked for one. Empty
// searches them all.
func currentSession(only string, args []string) (harness.Session, error) {
	if len(args) > 0 {
		return harness.Session{}, errors.New("watch: --current finds the session itself; do not also name one")
	}
	dir, err := os.Getwd()
	if err != nil {
		return harness.Session{}, fmt.Errorf("watch: read working directory: %w", err)
	}

	found, errs := discoverSessions(only)

	chosen, err := selectCurrent(dir, found)
	if err != nil {
		return harness.Session{}, fmt.Errorf("watch: %w\n%s%s", err,
			"run flow-indicator sessions to see every session on this machine, "+
				"or flow-indicator watch --last to follow the most recent\n",
			harnessNotes(errs))
	}
	return chosen, nil
}

// harnessNotes reports the harnesses that could not be searched.
//
// A harness that failed to enumerate is not the same as a harness with no
// sessions, and an operator whose session was missed because sqlite3 is absent
// should be told that rather than that nothing matched.
func harnessNotes(errs map[string]error) string {
	if len(errs) == 0 {
		return ""
	}
	names := make([]string, 0, len(errs))
	for name := range errs {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "note: %s could not be searched: %v\n", name, errs[name])
	}
	return strings.TrimRight(b.String(), "\n")
}
