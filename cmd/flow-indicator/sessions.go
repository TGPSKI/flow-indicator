package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/harness"
)

// Naming a session without knowing where it lives.
//
// Every harness keeps its transcripts somewhere only it knows: Codex under a
// dated directory with a UUID in the file name, opencode as rows in SQLite
// addressed by a generated key. An operator who wants to watch one of those has
// no way to type its name from memory, and the fallback observed in practice was
// `find ~ -name '*<uuid>*'` for Codex and a hand-written SELECT for opencode.
//
// Discovery already knows all of it and costs about 20ms for a couple of
// thousand sessions. What was missing was a door to it that is not --current,
// which only answers for the directory the shell happens to be in.

// listedSessions is how many sessions the listing shows before --all.
const listedSessions = 15

// perProjectRows is how many sessions from one project the listing shows.
//
// A tool that spawns a session per task puts thousands of them in one
// directory, and without a cap that one project fills the page while the
// session the operator is actually sitting in falls off the bottom.
const perProjectRows = 3

// ambiguousRows is how many candidates an ambiguous name lists before it stops.
const ambiguousRows = 10

// capPerProject keeps one project's churn from crowding out every other. Input
// must already be newest-first, so what survives is each project's most recent.
func capPerProject(in []harness.Session, n int) []harness.Session {
	seen := map[string]int{}
	var out []harness.Session
	for _, s := range in {
		if seen[s.Root] >= n {
			continue
		}
		seen[s.Root]++
		out = append(out, s)
	}
	return out
}

func sessionsCmd(args []string) error {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	harnessName := fs.String("adapter", "", "list only this harness")
	all := fs.Bool("all", false, "list every session, not the most recent few")
	here := fs.Bool("here", false, "list only sessions recorded for this working directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *harnessName != "" {
		if _, err := harness.New(*harnessName); err != nil {
			return err
		}
	}
	found, errs := discoverSessions(*harnessName)
	found = filterSessions(found, *harnessName, *here)
	if len(found) == 0 {
		fmt.Println("no sessions found")
		if note := harnessNotes(errs); note != "" {
			fmt.Println(note)
		}
		return nil
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Modified.After(found[j].Modified) })

	shown := found
	if !*all {
		// The cap keeps one project from filling a listing that spans them all.
		// --here is already one project by definition, and capping there would
		// hide the very sessions that were asked for.
		if !*here {
			shown = capPerProject(shown, perProjectRows)
		}
		if len(shown) > listedSessions {
			shown = shown[:listedSessions]
		}
	}
	handles := uniquePrefixes(found)
	printSessions(shown, handles)
	if len(shown) < len(found) {
		fmt.Printf("\n%d more; pass --all to list them\n", len(found)-len(shown))
	}
	fmt.Printf("\nfollow the most recent:  flow-indicator watch --last\n")
	fmt.Printf("follow one by name:      flow-indicator watch %s\n", handles[shown[0].ID])
	if note := harnessNotes(errs); note != "" {
		fmt.Println(note)
	}
	return nil
}

// filterSessions keeps the sessions an operator could actually follow.
//
// Two kinds are dropped whatever the flags say. A sub-agent thread is an
// agent's own stream, never what someone means by "my session". A transcript
// that records no working directory is not a session at all: it is the sidecar
// a harness leaves beside one, carrying a title and no conversation, and
// offering it gives the operator something that draws an empty meter.
func filterSessions(in []harness.Session, harnessName string, here bool) []harness.Session {
	dir := ""
	if here {
		dir, _ = os.Getwd()
	}
	var out []harness.Session
	for _, s := range in {
		switch {
		case s.IsSubagent(), s.Root == "",
			harnessName != "" && s.Harness != harnessName,
			here && s.Root != dir:
			continue
		}
		out = append(out, s)
	}
	return out
}

// discoverSessions narrows the read before discovery starts when only is set.
// Filtering DiscoverAll afterward would already have opened every store and
// invoked sqlite3 for opencode.
func discoverSessions(only string) ([]harness.Session, map[string]error) {
	if only != "" {
		return harness.DiscoverOnly(only)
	}
	return harness.DiscoverAll()
}

// printSessions writes the listing as a table.
func printSessions(sessions []harness.Session, handles map[string]string) {
	rows := make([][4]string, 0, len(sessions))
	widths := [4]int{4, 7, 7, 7}
	for _, s := range sessions {
		handle := handles[s.ID]
		if handle == "" {
			handle = shortID(s.ID)
		}
		row := [4]string{ago(s.Modified), s.Harness, handle, tildePath(s.Root)}
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
		rows = append(rows, row)
	}
	header := [4]string{"WHEN", "HARNESS", "SESSION", "PROJECT"}
	fmt.Printf("%-*s  %-*s  %-*s  %s\n",
		widths[0], header[0], widths[1], header[1], widths[2], header[2], header[3])
	for _, r := range rows {
		fmt.Printf("%-*s  %-*s  %-*s  %s\n",
			widths[0], r[0], widths[1], r[1], widths[2], r[2], r[3])
	}
}

// shortIDLen is the shortest identifier prefix the listing will print. Below
// this a handle stops being recognisable even when it is unique.
const shortIDLen = 8

// shortID is a fallback for callers with no corpus to compare against.
func shortID(id string) string {
	if len(id) <= shortIDLen {
		return id
	}
	return id[:shortIDLen]
}

// uniquePrefixes gives each session the shortest prefix of its identifier that
// no other session shares.
//
// The listing prints a handle so it can be retyped, and a handle that resolves
// to two sessions is not a handle. It is computed against the same set
// resolution searches: expanding a prefix to disambiguate a sub-agent thread
// nobody can follow would make every handle longer for no reason.
//
// Sorting puts identifiers that share a prefix next to each other, so each one
// is compared with its two neighbours rather than with all the others.
func uniquePrefixes(all []harness.Session) map[string]string {
	ids := make([]string, 0, len(all))
	for _, s := range all {
		ids = append(ids, s.ID)
	}
	sort.Strings(ids)

	out := make(map[string]string, len(ids))
	for i, id := range ids {
		n := shortIDLen
		for _, j := range [2]int{i - 1, i + 1} {
			if j < 0 || j >= len(ids) || ids[j] == id {
				continue
			}
			if shared := commonPrefixLen(id, ids[j]); shared >= n {
				n = shared + 1
			}
		}
		if n > len(id) {
			n = len(id)
		}
		out[id] = id[:n]
	}
	return out
}

// commonPrefixLen is how many leading bytes two identifiers share.
func commonPrefixLen(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// tildePath shortens a home-relative path, because the home prefix is the same
// on every row and carries nothing.
func tildePath(p string) string {
	if p == "" {
		return "—"
	}
	home, err := os.UserHomeDir()
	if err != nil || !strings.HasPrefix(p, home) {
		return p
	}
	return "~" + strings.TrimPrefix(p, home)
}

// ago renders how long ago a session was last written to.
func ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < 90*time.Second:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// resolveSession finds the session an operator named.
//
// The argument is whatever they typed: a path to a transcript, a store locator,
// or — the case this exists for — a bare identifier or a prefix of one, with no
// harness and no path. Discovery answers the last of those, so nobody has to
// know that Codex files are named after a UUID or that opencode has no files.
func resolveSession(arg, harnessName string, explicit bool) (harness.Session, error) {
	// Anything that names a place on disk is taken at its word. Discovery is
	// for identifiers, and a path is not an identifier.
	if strings.Contains(arg, "#") || strings.ContainsRune(arg, filepath.Separator) || fileExists(arg) {
		return harness.Session{Harness: harnessName, Locator: arg, ID: harness.IDOf(arg)}, nil
	}

	// An identifier already names exactly one session across every harness, so
	// discovery is narrowed only when the operator asked for a harness. Reading
	// the --adapter default here would search claude-code and miss every Codex
	// and opencode session on the machine.
	only := ""
	if explicit {
		only = harnessName
	}
	found, errs := discoverSessions(only)
	candidates := filterSessions(found, only, false)
	var matches []harness.Session
	for _, s := range candidates {
		if strings.HasPrefix(s.ID, arg) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return harness.Session{}, fmt.Errorf(
			"watch: no session identifier begins with %q\n"+
				"run flow-indicator sessions to see what is here%s", arg, noteSuffix(errs))
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].Modified.After(matches[j].Modified) })
	// The handles offered here have to resolve, or the fix for an ambiguous
	// name is another ambiguous name.
	handles := uniquePrefixes(candidates)
	listed := matches
	if len(listed) > ambiguousRows {
		listed = listed[:ambiguousRows]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "watch: %q names %d sessions; type more of it\n", arg, len(matches))
	for _, s := range listed {
		fmt.Fprintf(&b, "  %-14s  %-11s  %-9s  %s\n",
			handles[s.ID], s.Harness, ago(s.Modified), tildePath(s.Root))
	}
	if len(listed) < len(matches) {
		fmt.Fprintf(&b, "  ...and %d older\n", len(matches)-len(listed))
	}
	return harness.Session{}, errors.New(strings.TrimRight(b.String(), "\n"))
}

// announce prints the session that was chosen and returns the byte to start at.
//
// Saying which session is being measured is not decoration: discovery picked it,
// and an operator who cannot see what it picked cannot tell a right answer from
// a plausible one.
func announce(s harness.Session, tailOnly bool) int64 {
	mode := "building state from the start of the session"
	var start int64
	if tailOnly {
		start = tailFromEnd
		mode = "following from the end; earlier turns are not in this state"
	}
	fmt.Printf("harness %s\nsession %s\n%s\n%s\n\n", s.Harness, s.ID, s.Locator, mode)
	return start
}

// lastSession is the most recently written session on this machine.
//
// It is what "watch what I am doing" means when the shell is not sitting in the
// project directory, which inside a pane it usually is not.
func lastSession(harnessName string) (harness.Session, error) {
	found, errs := discoverSessions(harnessName)
	found = filterSessions(found, harnessName, false)
	if len(found) == 0 {
		return harness.Session{}, fmt.Errorf("watch: no sessions found%s", noteSuffix(errs))
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Modified.After(found[j].Modified) })
	return found[0], nil
}

func noteSuffix(errs map[string]error) string {
	if note := harnessNotes(errs); note != "" {
		return "\n" + note
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// parseInterspersed parses flags that appear after positional arguments.
//
// The flag package stops at the first operand, so `watch 019ffd20 --full` would
// take the identifier and silently ignore the flag. Now that the ordinary way
// to name a session is an identifier rather than a trailing path, that ordering
// is the one people type.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var operands []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return operands, nil
		}
		operands = append(operands, rest[0])
		args = rest[1:]
	}
}
