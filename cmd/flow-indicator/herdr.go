package main

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Reporting the meter into a herdr sidebar row.
//
// herdr renders metadata tokens in its own sidebar, so the phase does not
// need a pane of its own to be visible. This pushes four tokens and nothing
// else: it never reads herdr's state, never changes a pane, and never touches
// the running server's lifecycle.
//
// Two targets exist because they display under different conditions. Pane
// tokens render only on an agents-panel row, which herdr draws only for a
// pane it has promoted to an agent; workspace tokens render on the space row
// every workspace already has. Either way the tokens are invisible until the
// herdr sidebar config names them ($flow_phase and friends) in a row.
//
// Every push carries a TTL. If this process dies, the row expires instead of
// showing a phase that stopped being true.

const (
	// herdrSource identifies these tokens as this program's, so herdr can
	// attribute and expire them as a group.
	herdrSource = "flow-indicator"

	// herdrTTL is how long a pushed row stays valid without a refresh. It is
	// comfortably longer than the refresh interval, so an ordinary slow turn
	// never blanks the row.
	herdrTTL = 30 * time.Second

	// herdrRefresh is how often an unchanged row is pushed again to hold its
	// TTL open. Operator turns can be minutes apart; the row must outlive the
	// gap without one subprocess per draw.
	herdrRefresh = 10 * time.Second

	// herdrTimeout bounds one push. The meter's own loop must never wait on
	// another program.
	herdrTimeout = 3 * time.Second
)

// herdrRow is the state one sidebar row shows.
type herdrRow struct {
	phase   string
	turn    string
	elapsed string
	trend   string
}

// tokens renders the row as herdr --token arguments. An empty field is cleared
// rather than pushed blank, so a row never keeps a value that no longer holds.
func (r herdrRow) tokens() []string {
	var args []string
	for _, t := range []struct{ name, value string }{
		{"flow_phase", r.phase},
		{"flow_turn", r.turn},
		{"flow_elapsed", r.elapsed},
		{"flow_trend", r.trend},
	} {
		if t.value == "" {
			args = append(args, "--clear-token", t.name)
			continue
		}
		args = append(args, "--token", t.name+"="+t.value)
	}
	return args
}

// herdrArgs is the full command line for one push. scope is "pane" or
// "workspace" — the two report-metadata surfaces herdr exposes.
func herdrArgs(scope, id string, seq int64, r herdrRow) []string {
	args := []string{
		scope, "report-metadata", id,
		"--source", herdrSource,
		"--seq", fmt.Sprintf("%d", seq),
		"--ttl-ms", fmt.Sprintf("%d", herdrTTL.Milliseconds()),
	}
	return append(args, r.tokens()...)
}

// herdrReporter pushes rows to herdr without ever blocking the draw loop.
//
// One worker owns the subprocess. The loop offers a row and moves on: if a
// push is still running, the offer is dropped, because the next draw is a
// better row than the one that could not be sent.
type herdrReporter struct {
	scope string
	id    string
	rows  chan herdrRow
	done  chan struct{}
}

// newHerdrReporter starts a reporter for one pane or workspace. An empty id
// disables it, and every method on a nil reporter is a no-op.
func newHerdrReporter(ctx context.Context, scope, id string) *herdrReporter {
	if id == "" {
		return nil
	}
	h := &herdrReporter{
		scope: scope,
		id:    id,
		rows:  make(chan herdrRow, 1),
		done:  make(chan struct{}),
	}
	go h.run(ctx)
	return h
}

// seq is the pane's per-source freshness mark, and herdr keeps the highest
// value it has ever accepted for a source, outliving this process. A counter
// restarting at 1 would sit below the previous run's mark and every push
// would be dropped as stale, so seq is wall-clock nanoseconds — above any
// earlier run's mark, the same scheme herdr's own hook integrations use.
func (h *herdrReporter) run(ctx context.Context) {
	defer close(h.done)
	for {
		select {
		case <-ctx.Done():
			// The row outlives this process only until its TTL runs out, but
			// clearing it now means the sidebar never shows a stopped meter as
			// a live one. The context is already cancelled, so the clear gets
			// its own bounded one.
			h.push(context.Background(), time.Now().UnixNano(), herdrRow{})
			return
		case row := <-h.rows:
			h.push(ctx, time.Now().UnixNano(), row)
		}
	}
}

// push runs one report. Failures are ignored: herdr may not be running, and a
// meter that cannot draw a sidebar row is still a working meter.
func (h *herdrReporter) push(ctx context.Context, seq int64, row herdrRow) {
	ctx, cancel := context.WithTimeout(ctx, herdrTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, "herdr", herdrArgs(h.scope, h.id, seq, row)...).Run()
}

// Report offers a row. It never blocks and never fails.
func (h *herdrReporter) Report(row herdrRow) {
	if h == nil {
		return
	}
	select {
	case h.rows <- row:
	default:
	}
}

// Close waits for the worker to finish its final clear.
func (h *herdrReporter) Close() {
	if h == nil {
		return
	}
	<-h.done
}
