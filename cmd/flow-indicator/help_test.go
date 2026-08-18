package main

import (
	"sort"
	"strings"
	"testing"
)

// Help is read in the pane the meter runs in. Text wider than the pane wraps
// mid-word, which is how a flag list stops being a flag list: the terminal
// where this is used is narrow, and widening it is not the operator's job.
func TestHelpFitsANarrowPane(t *testing.T) {
	pages := map[string]string{"(front page)": usage}
	for name, text := range commandHelp {
		pages["help "+name] = text
	}
	for name, text := range pages {
		for i, line := range strings.Split(text, "\n") {
			if len(line) > helpColumns {
				t.Errorf("%s line %d is %d columns, over the %d it must fit:\n  %s",
					name, i+1, len(line), helpColumns, line)
			}
		}
	}
}

// A command on the front page with no detail page sends the reader nowhere, and
// a detail page for a command the front page never mentions cannot be found.
func TestEveryListedCommandHasHelp(t *testing.T) {
	listed := commandsInUsage(t)
	for _, name := range listed {
		if _, ok := commandHelp[name]; !ok {
			t.Errorf("%q is listed on the front page but `help %s` has nothing", name, name)
		}
	}
	inUsage := map[string]bool{}
	for _, name := range listed {
		inUsage[name] = true
	}
	documented := make([]string, 0, len(commandHelp))
	for name := range commandHelp {
		documented = append(documented, name)
	}
	sort.Strings(documented)
	for _, name := range documented {
		if !inUsage[name] {
			t.Errorf("`help %s` exists but the front page never lists it", name)
		}
	}
}

// A detail page is the terminal contract for one command. A flag accepted by
// the parser but absent here cannot be discovered without reading source.
func TestCommonFlagsAppearOnEveryCommandThatAcceptsThem(t *testing.T) {
	for _, command := range []string{"replay", "replay-set", "watch", "report", "inspect", "calibrate"} {
		for _, flag := range []string{"--config", "--data-dir", "--profile"} {
			if !strings.Contains(commandHelp[command], flag) {
				t.Errorf("help %s omits %s", command, flag)
			}
		}
	}
}

func TestCalibrateHelpNamesClassifierModes(t *testing.T) {
	text := commandHelp["calibrate"]
	if !strings.Contains(text, "--classifier <mode>") || strings.Contains(text, "--classifier <path>") {
		t.Errorf("calibrate help does not describe --classifier as a mode:\n%s", text)
	}
}

// commandsInUsage reads the command names out of the front page's command list.
func commandsInUsage(t *testing.T) []string {
	t.Helper()
	_, rest, ok := strings.Cut(usage, "\ncommands:\n")
	if !ok {
		t.Fatal("the front page has no commands: block")
	}
	block, _, _ := strings.Cut(rest, "\n\n")
	var out []string
	for _, line := range strings.Split(block, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			out = append(out, fields[0])
		}
	}
	if len(out) == 0 {
		t.Fatal("the commands: block lists nothing")
	}
	return out
}

// The front page has to name the way in. Its whole job is that an operator who
// types the bare binary learns what to type next without reading further.
func TestFrontPageOpensWithWhatToType(t *testing.T) {
	head, _, _ := strings.Cut(usage, "\ncommands:\n")
	for _, want := range []string{
		"flow-indicator sessions",
		"flow-indicator watch --last",
		"flow-indicator watch --current",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("the front page does not open with %q", want)
		}
	}
}
