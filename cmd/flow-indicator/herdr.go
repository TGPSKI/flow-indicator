package main

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Reporting the meter into a herdr sidebar row.
//
// herdr renders pane metadata tokens in its own sidebar, so the phase does not
// need a pane of its own to be visible. This pushes four tokens and nothing
// else: it never reads herdr's state, never changes a pane, and never touches
// the running server's lifecycle.
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

// herdrArgs is the full command line for one push.
func herdrArgs(pane string, seq int, r herdrRow) []string {
	args := []string{
		"pane", "report-metadata", pane,
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
	pane string
	rows chan herdrRow
	done chan struct{}
}

// newHerdrReporter starts a reporter for a pane. An empty pane disables it,
// and every method on a nil reporter is a no-op.
func newHerdrReporter(ctx context.Context, pane string) *herdrReporter {
	if pane == "" {
		return nil
	}
	h := &herdrReporter{
		pane: pane,
		rows: make(chan herdrRow, 1),
		done: make(chan struct{}),
	}
	go h.run(ctx)
	return h
}

func (h *herdrReporter) run(ctx context.Context) {
	defer close(h.done)
	seq := 0
	for {
		select {
		case <-ctx.Done():
			// The row outlives this process only until its TTL runs out, but
			// clearing it now means the sidebar never shows a stopped meter as
			// a live one. The context is already cancelled, so the clear gets
			// its own bounded one.
			h.push(context.Background(), seq+1, herdrRow{})
			return
		case row := <-h.rows:
			seq++
			h.push(ctx, seq, row)
		}
	}
}

// push runs one report. Failures are ignored: herdr may not be running, and a
// meter that cannot draw a sidebar row is still a working meter.
func (h *herdrReporter) push(ctx context.Context, seq int, row herdrRow) {
	ctx, cancel := context.WithTimeout(ctx, herdrTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, "herdr", herdrArgs(h.pane, seq, row)...).Run()
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
