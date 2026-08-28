package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/harness"
)

// Pane-scoped discovery.
//
// --current keys on the working directory, and two agents working in one
// directory make that selection a coin flip: when this was written the
// measured fleet had three agent panes recording the same cwd. The pane is
// the exact key.
//
// Resolution order for --pane:
//
//  1. target pane: an explicit pane id, or auto — the agent pane sharing
//     this process's tab, widening to its workspace when the tab has none.
//     Two candidates are an error naming each, never a guess.
//  2. pane -> session id: herdr's agent_session join, uniformly for every
//     agent. Each installed herdr integration reports its agent's session
//     id into the daemon, so one field answers for claude, codex, opencode
//     and qwen alike, and no agent-specific scraping exists to rot. A pane
//     the integration has not reported yet is waited on — identity arrives
//     with the pane's next activity — never scraped for.
//  3. session id -> harness.Session through that harness's own discovery,
//     so what follows is the same watch every other mode runs.
//
// Everything here reads: herdr query commands and harness stores. Nothing
// acts on the pane it resolves.

// paneAuto asks discovery to find the agent pane adjacent to this one.
const paneAuto = "auto"

// herdrQueryTimeout bounds one herdr read. Discovery must fail fast when the
// server is gone, not hang a watch the operator could have given a UUID.
const herdrQueryTimeout = 3 * time.Second

// agentPane is the slice of a herdr pane record that discovery reads.
type agentPane struct {
	PaneID       string `json:"pane_id"`
	TabID        string `json:"tab_id"`
	WorkspaceID  string `json:"workspace_id"`
	Agent        string `json:"agent"`
	AgentSession struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"agent_session"`
}

// herdrEnvelope is the wrapper every herdr CLI query prints.
type herdrEnvelope struct {
	Result struct {
		Agents []agentPane `json:"agents"`
		Pane   *agentPane  `json:"pane"`
	} `json:"result"`
}

// parseAgentPanes reads herdr agent list output.
func parseAgentPanes(raw []byte) ([]agentPane, error) {
	var env herdrEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("pane: decode herdr agent list: %w", err)
	}
	return env.Result.Agents, nil
}

// parseCurrentPane reads herdr pane current output.
func parseCurrentPane(raw []byte) (agentPane, error) {
	var env herdrEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return agentPane{}, fmt.Errorf("pane: decode herdr pane current: %w", err)
	}
	if env.Result.Pane == nil {
		return agentPane{}, errors.New("pane: herdr pane current returned no pane")
	}
	return *env.Result.Pane, nil
}

// paneChoices formats the agent panes as arguments for the named flag, for
// an error.
func paneChoices(flag string, agents []agentPane) string {
	if len(agents) == 0 {
		return "; no agent panes are running"
	}
	sorted := append([]agentPane(nil), agents...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PaneID < sorted[j].PaneID })
	var b strings.Builder
	for _, a := range sorted {
		fmt.Fprintf(&b, "\n  %s %s  (%s)", flag, a.PaneID, a.Agent)
	}
	return b.String()
}

// choosePane picks the pane to follow.
//
// An explicit id only needs to exist. Auto scopes to the asking pane's tab
// first: a workspace holds every tab's agents, and an operator asking from a
// split means the agent beside that split, not one three tabs away. A tab
// with no agent widens to the workspace, for the layout where the meter runs
// in a tab of its own.
func choosePane(flag, target string, self agentPane, agents []agentPane) (agentPane, error) {
	if target != paneAuto {
		for _, a := range agents {
			if a.PaneID == target {
				return a, nil
			}
		}
		return agentPane{}, fmt.Errorf("pane: no agent runs in pane %s%s", target, paneChoices(flag, agents))
	}
	candidates := filterAgentPanes(agents, func(a agentPane) bool { return a.TabID == self.TabID })
	if len(candidates) == 0 {
		candidates = filterAgentPanes(agents, func(a agentPane) bool { return a.WorkspaceID == self.WorkspaceID })
	}
	switch len(candidates) {
	case 0:
		return agentPane{}, fmt.Errorf("pane: no agent pane in tab %s or workspace %s%s",
			self.TabID, self.WorkspaceID, paneChoices(flag, agents))
	case 1:
		return candidates[0], nil
	}
	return agentPane{}, fmt.Errorf("pane: more than one agent pane is adjacent; pass the one to follow%s",
		paneChoices(flag, candidates))
}

func filterAgentPanes(agents []agentPane, keep func(agentPane) bool) []agentPane {
	var out []agentPane
	for _, a := range agents {
		if keep(a) {
			out = append(out, a)
		}
	}
	return out
}

// unresolvedError marks a pane whose agent is present but whose session
// identity is not knowable right now, as opposed to a pane that cannot be
// followed at all. Waiting cures the former and never the latter.
type unresolvedError struct{ msg string }

func (e unresolvedError) Error() string { return e.msg }

func unresolvedf(format string, args ...any) error {
	return unresolvedError{msg: fmt.Sprintf(format, args...)}
}

func isUnresolved(err error) bool {
	var u unresolvedError
	return errors.As(err, &u)
}

// paneSessionID is the herdr join, and deliberately nothing else.
//
// An earlier version fell back to scraping the pane's processes — a session
// variable for claude, an open state database for codex. Each was a second
// data path for the same fact, correct only for one agent and one version of
// it, and the claude one was measured working only mid-tool-call. One
// uniform source with an honest wait beats three sources that rot
// separately: a pane the integration has not reported yet resolves on its
// next activity.
func paneSessionID(pane agentPane) (string, error) {
	if pane.AgentSession.Value != "" {
		return pane.AgentSession.Value, nil
	}
	return "", unresolvedf("pane: herdr records no session for %s (%s) yet", pane.PaneID, pane.Agent)
}

// harnessForAgent maps herdr's agent names onto harness names.
func harnessForAgent(agent string) (string, error) {
	switch agent {
	case "claude":
		return harness.Claude{}.Name(), nil
	case "codex":
		return harness.Codex{}.Name(), nil
	case "opencode":
		return harness.OpenCode{}.Name(), nil
	case "qwen":
		return harness.Qwen{}.Name(), nil
	}
	return "", fmt.Errorf("pane: no harness reads %q sessions", agent)
}

// findSessionByID picks the harness's session with the given identifier.
func findSessionByID(sessions []harness.Session, harnessName, id string) (harness.Session, error) {
	for _, s := range sessions {
		if s.Harness == harnessName && s.ID == id {
			return s, nil
		}
	}
	return harness.Session{}, fmt.Errorf("pane: %s records no session %s", harnessName, id)
}

// herdrQuery runs one read-only herdr command and returns its stdout.
func herdrQuery(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrQueryTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "herdr", args...).Output()
	if err != nil {
		if _, look := exec.LookPath("herdr"); look != nil {
			return nil, errors.New("pane: --pane needs the herdr command, which is not on PATH")
		}
		return nil, fmt.Errorf("pane: herdr %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// paneTarget resolves the target pane and the session id running in it.
func paneTarget(target string) (agentPane, string, error) {
	raw, err := herdrQuery("agent", "list")
	if err != nil {
		return agentPane{}, "", err
	}
	agents, err := parseAgentPanes(raw)
	if err != nil {
		return agentPane{}, "", err
	}
	var self agentPane
	if target == paneAuto {
		currentRaw, err := herdrQuery("pane", "current")
		if err != nil {
			return agentPane{}, "", err
		}
		if self, err = parseCurrentPane(currentRaw); err != nil {
			return agentPane{}, "", err
		}
	}
	pane, err := choosePane("--pane", target, self, agents)
	if err != nil {
		return agentPane{}, "", err
	}
	id, err := paneSessionID(pane)
	if err != nil {
		return agentPane{}, "", err
	}
	return pane, id, nil
}

// paneSession resolves --pane to the session running in that pane.
func paneSession(target string) (harness.Session, error) {
	pane, id, err := paneTarget(target)
	if err != nil {
		return harness.Session{}, err
	}
	harnessName, err := harnessForAgent(pane.Agent)
	if err != nil {
		return harness.Session{}, err
	}
	found, errs := harness.DiscoverOnly(harnessName)
	s, err := findSessionByID(found, harnessName, id)
	if err != nil {
		// The pane names a session the store has not seen: a just-started
		// agent's transcript can postdate its pane's id. Waiting cures it.
		return harness.Session{}, unresolvedf("%v%s", err, noteSuffix(errs))
	}
	fmt.Printf("pane %s\n", pane.PaneID)
	return s, nil
}

// paneRecheckInterval is how often a running watch re-resolves its pane.
const paneRecheckInterval = 5 * time.Second

// paneWaitInterval is how often an unresolved pane is re-probed.
const paneWaitInterval = 2 * time.Second

// paneSessionWait resolves a pane, waiting while its identity is unknowable.
//
// A pane's integration reports its session on the session's activity, and a
// just-started agent's transcript can lag its pane — both resolve on the
// pane's next activity, so waiting says so once and keeps probing. Only an
// error waiting cannot cure — no such pane, ambiguity, a missing command —
// is returned.
func paneSessionWait(ctx context.Context, interval time.Duration, resolve func() (harness.Session, error)) (harness.Session, error) {
	waiting := false
	for {
		s, err := resolve()
		if err == nil {
			return s, nil
		}
		if !isUnresolved(err) {
			return harness.Session{}, err
		}
		if !waiting {
			fmt.Printf("%v\nwaiting: identity arrives with the pane's next activity\n", err)
			waiting = true
		}
		select {
		case <-ctx.Done():
			return harness.Session{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// watchPaneChange ends the current watch when the pane's session changes.
//
// Only a successful resolution to a different id acts. Errors and empty ids
// change nothing: herdr may be briefly unreachable, and a pane between agent
// exits resolves to nothing — neither says the session changed, and a watch
// ended on either would turn every restart gap into a dead meter.
func watchPaneChange(ctx context.Context, cancel context.CancelFunc, resolve func() (string, error), current string, every time.Duration, changed *atomic.Bool) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			id, err := resolve()
			if err != nil || id == "" || id == current {
				continue
			}
			changed.Store(true)
			cancel()
			return
		}
	}
}
