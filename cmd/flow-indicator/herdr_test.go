package main

import (
	"slices"
	"strings"
	"testing"
)

// The row is pushed as tokens herdr can render in a sidebar row, with a TTL so
// a dead meter expires instead of leaving a phase that stopped being true.
func TestHerdrArgs(t *testing.T) {
	args := herdrArgs("w1V:p1", 7, herdrRow{
		phase: "THRASH", turn: "turn 12", elapsed: "53m40s", trend: "CPB mid → bad",
	})
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"pane report-metadata w1V:p1",
		"--source flow-indicator",
		"--seq 7",
		"--ttl-ms 30000",
		"--token flow_phase=THRASH",
		"--token flow_turn=turn 12",
		"--token flow_elapsed=53m40s",
		"--token flow_trend=CPB mid → bad",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q:\n%s", want, joined)
		}
	}
}

// An empty field clears its token rather than pushing a blank one, so a row
// never keeps a value that no longer holds.
func TestHerdrClearsEmptyTokens(t *testing.T) {
	args := herdrRow{phase: "FLOW"}.tokens()
	if !slices.Contains(args, "--token") || !slices.Contains(args, "flow_phase=FLOW") {
		t.Errorf("phase not pushed: %v", args)
	}
	for _, name := range []string{"flow_turn", "flow_elapsed", "flow_trend"} {
		i := slices.Index(args, name)
		if i < 1 || args[i-1] != "--clear-token" {
			t.Errorf("%s was not cleared: %v", name, args)
		}
	}
}

// A nil reporter is the disabled case and must be inert.
func TestHerdrReporterNilIsInert(t *testing.T) {
	var h *herdrReporter
	h.Report(herdrRow{phase: "FLOW"})
	h.Close()
}

// Disabling is by empty pane, and must not start a worker.
func TestHerdrDisabledWithoutPane(t *testing.T) {
	if h := newHerdrReporter(t.Context(), ""); h != nil {
		t.Error("a reporter was started with no pane to report to")
	}
}
