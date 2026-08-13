package state

import (
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Seq is a record ordinal and TurnIndex is a turn ordinal. A live view that
// labels the first as the turn climbs by the size of the agent's reply, which
// is not a number the operator has any use for.
func TestTurnIndexCountsOperatorTurnsNotRecords(t *testing.T) {
	p := newProjector()

	// Three operator turns, each answered by several agent records.
	push(p, operator("Add the retry loop. Stop after CI passes.", 0))
	if got := p.Snapshot().TurnIndex; got != 1 {
		t.Fatalf("after one operator turn TurnIndex = %d, want 1", got)
	}

	push(p, agent("Working on it.", 1), agent("Edited the worker.", 2), agent("Ran the tests.", 3))
	if got := p.Snapshot().TurnIndex; got != 1 {
		t.Errorf("agent records moved TurnIndex to %d, want it held at 1", got)
	}

	push(p, operator("Now add the metric.", 4))
	push(p, agent("Added.", 5), agent("Tests pass.", 6))
	push(p, operator("Ship it.", 7))

	snap := p.Snapshot()
	if snap.TurnIndex != 3 {
		t.Errorf("TurnIndex = %d, want 3 operator turns", snap.TurnIndex)
	}
	// Every record advanced the record ordinal, so the two numbers must differ
	// here or the test is not proving anything.
	if snap.Seq <= uint64(snap.TurnIndex) {
		t.Errorf("Seq = %d and TurnIndex = %d: the record ordinal should be the larger",
			snap.Seq, snap.TurnIndex)
	}
}

// Records the operator did not author do not advance the turn, whichever
// speaker the harness filed them under.
func TestTurnIndexIgnoresNonOperatorRecords(t *testing.T) {
	p := newProjector()
	push(p, operator("Start the work.", 0))

	sidechain := operator("agent-authored text inside a nested stream", 1)
	sidechain.Metadata[stream.MetaSidechain] = "true"

	shell := operator("git status", 2)
	shell.Metadata[stream.MetaInputMode] = stream.InputShellEscape

	push(p, sidechain, shell)
	if got := p.Snapshot().TurnIndex; got != 1 {
		t.Errorf("TurnIndex = %d after sidechain and shell records, want 1", got)
	}
}
