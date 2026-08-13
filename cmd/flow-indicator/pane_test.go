package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/harness"
)

// agentListFixture is a trimmed capture of herdr agent list from the fleet
// this feature was measured against: a codex pane herdr has joined to its
// session and a claude pane it has not yet.
const agentListFixture = `{"id":"cli:agent:list","result":{"agents":[
{"agent":"codex","agent_session":{"agent":"codex","kind":"id","source":"herdr:codex","value":"01a00472-8a90-7343-b751-0ada0fdac2c3"},"agent_status":"working","pane_id":"w1W:p1","tab_id":"w1W:t1","workspace_id":"w1W"},
{"agent":"claude","agent_status":"working","pane_id":"w1W:p2","tab_id":"w1W:t2","workspace_id":"w1W"}
],"type":"agent_list"}}`

const paneCurrentFixture = `{"id":"cli:pane:current","result":{"pane":{"agent_status":"unknown","pane_id":"w1W:p8","tab_id":"w1W:t2","workspace_id":"w1W"},"type":"pane_current"}}`

func fixtureAgents(t *testing.T) []agentPane {
	t.Helper()
	agents, err := parseAgentPanes([]byte(agentListFixture))
	if err != nil {
		t.Fatal(err)
	}
	return agents
}

func TestParseAgentPanesReadsTheMeasuredShape(t *testing.T) {
	agents := fixtureAgents(t)
	if len(agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(agents))
	}
	if agents[0].Agent != "codex" || agents[0].AgentSession.Value != "01a00472-8a90-7343-b751-0ada0fdac2c3" {
		t.Fatalf("codex row misread: %+v", agents[0])
	}
	if agents[1].Agent != "claude" || agents[1].AgentSession.Value != "" {
		t.Fatalf("claude row misread: %+v", agents[1])
	}
}

func TestParseCurrentPane(t *testing.T) {
	self, err := parseCurrentPane([]byte(paneCurrentFixture))
	if err != nil {
		t.Fatal(err)
	}
	if self.PaneID != "w1W:p8" || self.TabID != "w1W:t2" {
		t.Fatalf("current pane misread: %+v", self)
	}
}

func TestChoosePaneExplicit(t *testing.T) {
	agents := fixtureAgents(t)
	got, err := choosePane("w1W:p1", agentPane{}, agents)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent != "codex" {
		t.Fatalf("got %s, want codex", got.Agent)
	}
}

func TestChoosePaneExplicitMissListsTheChoices(t *testing.T) {
	_, err := choosePane("w9Z:p1", agentPane{}, fixtureAgents(t))
	if err == nil {
		t.Fatal("want an error for a pane with no agent")
	}
	for _, want := range []string{"--pane w1W:p1", "--pane w1W:p2"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not offer %q", err, want)
		}
	}
}

// The operator's measured layout: each tab holds one agent and one terminal
// split. Auto from the split must pick that tab's agent, not the workspace's
// other one.
func TestChoosePaneAutoPrefersTheTab(t *testing.T) {
	agents := fixtureAgents(t)
	self := agentPane{PaneID: "w1W:p8", TabID: "w1W:t2", WorkspaceID: "w1W"}
	got, err := choosePane(paneAuto, self, agents)
	if err != nil {
		t.Fatal(err)
	}
	if got.PaneID != "w1W:p2" {
		t.Fatalf("got %s, want the claude pane sharing tab t2", got.PaneID)
	}
}

func TestChoosePaneAutoWidensToTheWorkspace(t *testing.T) {
	agents := []agentPane{{PaneID: "w2:p1", TabID: "w2:t1", WorkspaceID: "w2", Agent: "claude"}}
	self := agentPane{PaneID: "w2:p4", TabID: "w2:t3", WorkspaceID: "w2"}
	got, err := choosePane(paneAuto, self, agents)
	if err != nil {
		t.Fatal(err)
	}
	if got.PaneID != "w2:p1" {
		t.Fatalf("got %s, want the workspace's only agent", got.PaneID)
	}
}

func TestChoosePaneAutoRefusesTwoCandidates(t *testing.T) {
	agents := fixtureAgents(t)
	self := agentPane{PaneID: "w1W:p9", TabID: "w1W:t9", WorkspaceID: "w1W"}
	_, err := choosePane(paneAuto, self, agents)
	if err == nil || !strings.Contains(err.Error(), "--pane w1W:p1") || !strings.Contains(err.Error(), "--pane w1W:p2") {
		t.Fatalf("want both candidates offered, got %v", err)
	}
}

func TestChoosePaneAutoWithNoAgentsSaysSo(t *testing.T) {
	self := agentPane{TabID: "w1:t1", WorkspaceID: "w1"}
	_, err := choosePane(paneAuto, self, nil)
	if err == nil || !strings.Contains(err.Error(), "no agent pane") {
		t.Fatalf("want a no-agent error, got %v", err)
	}
}

// The session id is the herdr join and nothing else: a joined pane resolves,
// an unjoined one is unresolved — waited on, never scraped for or guessed.
func TestPaneSessionIDIsTheJoinAlone(t *testing.T) {
	agents := fixtureAgents(t)
	id, err := paneSessionID(agents[0])
	if err != nil || id != "01a00472-8a90-7343-b751-0ada0fdac2c3" {
		t.Fatalf("joined pane: got %q, %v", id, err)
	}
	_, err = paneSessionID(agents[1])
	if err == nil || !isUnresolved(err) {
		t.Fatalf("unjoined pane must be unresolved, got %v", err)
	}
	if !strings.Contains(err.Error(), "w1W:p2") {
		t.Fatalf("the wait reason must name the pane, got %v", err)
	}
}

func TestHarnessForAgent(t *testing.T) {
	for agent, want := range map[string]string{
		"claude":   "claude-code",
		"codex":    "codex",
		"opencode": "opencode",
	} {
		got, err := harnessForAgent(agent)
		if err != nil || got != want {
			t.Fatalf("harnessForAgent(%s) = %q, %v; want %q", agent, got, err, want)
		}
	}
	if _, err := harnessForAgent("emacs"); err == nil {
		t.Fatal("want an error for an unknown agent")
	}
}

// The monitor must survive a resolution error and an unchanged id, and act
// only when the pane positively names a different session.
func TestWatchPaneChangeCancelsOnlyOnANewSession(t *testing.T) {
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	defer cancelWatch()
	var changed atomic.Bool
	answers := []struct {
		id  string
		err error
	}{
		{"aaa", nil},
		{"", errors.New("herdr gone")},
		{"", nil},
		{"bbb", nil},
	}
	var call atomic.Int32
	resolve := func() (string, error) {
		i := int(call.Add(1)) - 1
		if i >= len(answers) {
			i = len(answers) - 1
		}
		return answers[i].id, answers[i].err
	}
	done := make(chan struct{})
	go func() {
		watchPaneChange(watchCtx, cancelWatch, resolve, "aaa", time.Millisecond, &changed)
		close(done)
	}()
	select {
	case <-watchCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("watch was not cancelled after the pane's session changed")
	}
	<-done
	if !changed.Load() {
		t.Fatal("the changed flag was not set before cancelling")
	}
	if call.Load() < 4 {
		t.Fatalf("cancelled after %d resolutions; the error and the gap should not have acted", call.Load())
	}
}

func TestWatchPaneChangeStopsWithItsContext(t *testing.T) {
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	var changed atomic.Bool
	done := make(chan struct{})
	go func() {
		watchPaneChange(watchCtx, cancelWatch, func() (string, error) { return "aaa", nil }, "aaa", time.Millisecond, &changed)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancelWatch()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop with its context")
	}
	if changed.Load() {
		t.Fatal("an unchanged id set the changed flag")
	}
}

func TestPaneSessionWaitProbesUntilResolvable(t *testing.T) {
	calls := 0
	s, err := paneSessionWait(context.Background(), time.Millisecond, func() (harness.Session, error) {
		calls++
		if calls < 3 {
			return harness.Session{}, unresolvedf("not yet")
		}
		return harness.Session{ID: "aaa"}, nil
	})
	if err != nil || s.ID != "aaa" || calls != 3 {
		t.Fatalf("got %+v, %v after %d calls", s, err, calls)
	}
}

func TestPaneSessionWaitReturnsFatalErrorsImmediately(t *testing.T) {
	calls := 0
	_, err := paneSessionWait(context.Background(), time.Millisecond, func() (harness.Session, error) {
		calls++
		return harness.Session{}, errors.New("no agent runs in pane w9Z:p9")
	})
	if err == nil || calls != 1 {
		t.Fatalf("a fatal error must not be waited on; got %v after %d calls", err, calls)
	}
}

func TestPaneSessionWaitStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err := paneSessionWait(ctx, time.Millisecond, func() (harness.Session, error) {
		return harness.Session{}, unresolvedf("never resolvable in this test")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestFindSessionByID(t *testing.T) {
	sessions := []harness.Session{
		{Harness: "codex", ID: "aaa"},
		{Harness: "claude-code", ID: "aaa"},
	}
	got, err := findSessionByID(sessions, "claude-code", "aaa")
	if err != nil || got.Harness != "claude-code" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := findSessionByID(sessions, "codex", "zzz"); err == nil {
		t.Fatal("want an error for an unknown id")
	}
}
