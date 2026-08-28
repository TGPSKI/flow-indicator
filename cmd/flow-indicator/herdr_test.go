package main

import (
	"slices"
	"strings"
	"testing"
)

// The row is pushed as tokens herdr can render in a sidebar row, with a TTL so
// a dead meter expires instead of leaving a phase that stopped being true.
func TestHerdrArgs(t *testing.T) {
	args := herdrArgs("pane", "w1V:p1", 7, herdrRow{
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

// A workspace target reports through the workspace surface, which herdr
// renders on the space row every workspace has, promoted agent or not.
func TestHerdrArgsWorkspaceScope(t *testing.T) {
	args := herdrArgs("workspace", "w3G", 7, herdrRow{phase: "FLOW"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "workspace report-metadata w3G") {
		t.Errorf("workspace scope not used:\n%s", joined)
	}
}

// Target resolution: explicit ids pass through, both flags at once is an
// error, and workspace auto is this process's own workspace from the
// environment. The pane auto branch reuses choosePane, which pane_test.go
// covers, and needs a live herdr, so it is not exercised here.
func TestResolveHerdrTarget(t *testing.T) {
	if _, _, err := resolveHerdrTarget("w1:p1", "w1"); err == nil {
		t.Error("both flags set did not error")
	}
	scope, id, err := resolveHerdrTarget("", "")
	if err != nil || scope != "pane" || id != "" {
		t.Errorf("empty flags = (%s, %q, %v); the reporter must stay disabled", scope, id, err)
	}
	scope, id, err = resolveHerdrTarget("w1:p1", "")
	if err != nil || scope != "pane" || id != "w1:p1" {
		t.Errorf("explicit pane = (%s, %q, %v)", scope, id, err)
	}
	scope, id, err = resolveHerdrTarget("", "w3G")
	if err != nil || scope != "workspace" || id != "w3G" {
		t.Errorf("explicit workspace = (%s, %q, %v)", scope, id, err)
	}
	t.Setenv("HERDR_WORKSPACE_ID", "w9")
	scope, id, err = resolveHerdrTarget("", "auto")
	if err != nil || scope != "workspace" || id != "w9" {
		t.Errorf("workspace auto = (%s, %q, %v); want the workspace HERDR_WORKSPACE_ID names", scope, id, err)
	}
}

// A nil reporter is the disabled case and must be inert.
func TestHerdrReporterNilIsInert(t *testing.T) {
	var h *herdrReporter
	h.Report(herdrRow{phase: "FLOW"})
	h.Close()
}

// Disabling is by empty target id, and must not start a worker.
func TestHerdrDisabledWithoutPane(t *testing.T) {
	if h := newHerdrReporter(t.Context(), "pane", ""); h != nil {
		t.Error("a reporter was started with no pane to report to")
	}
}
