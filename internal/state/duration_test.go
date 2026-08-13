package state

import (
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Recovery duration. An episode has two endpoints and they are not the same
// fact: `last_activity` is the last record attributed to the episode while it
// was open, `closed` is the moment it left the open state. A closed episode
// measured to `last_activity` reports the span up to some record inside it, not
// the span of the episode.

func at(minute int) time.Time {
	return time.Date(2026, 3, 4, 13, minute, 0, 0, time.UTC)
}

func tool(text string, minute int) stream.Record {
	return stream.Record{
		SpeakerClass: stream.SpeakerTool,
		Text:         text,
		Timestamp:    at(minute),
		Metadata:     stream.Metadata{},
	}
}

// A closed episode is measured from start to close. The agent's repair work at
// 13:10 and the closure at 13:20 are both inside the episode's wall clock; a
// duration of 60 seconds reports only the correction turn.
func TestClosedRepairIsMeasuredToItsClosingTimestamp(t *testing.T) {
	p := newProjector()
	push(p,
		agent("Added the retry loop. I also updated the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go. Revert the rest.", 2),
		agent("Reverted the workflow change.", 10),
	)
	r := p.Repairs()[0]
	if r.Closed != nil {
		t.Errorf("an open episode carries closed=%s", r.Closed)
	}
	if !r.LastActivity.Equal(at(10)) {
		t.Errorf("last activity = %s after the agent's repair response, want %s", r.LastActivity, at(10))
	}

	push(p, operator("Good. Run the suite.", 20))
	snapshot := p.Snapshot()
	if r.Closed == nil {
		t.Fatal("a closed episode carries no closing timestamp")
	}
	if !r.Closed.Equal(at(20)) {
		t.Errorf("closed = %s, want %s", r.Closed, at(20))
	}
	// The closing operator turn is not attributed to the episode: it spends no
	// repair characters or records inside it either.
	if !r.LastActivity.Equal(at(10)) {
		t.Errorf("last activity = %s after closure, want the last record inside the episode, %s", r.LastActivity, at(10))
	}
	if !snapshot.RepairSeconds.Known {
		t.Fatal("repair_seconds is unknown for an episode with both endpoints")
	}
	if got := snapshot.RepairSeconds.Num; got != 19*60 {
		t.Errorf("repair_seconds = %v for an episode open 13:01 → 13:20, want %v", got, 19*60)
	}
}

// While the episode is open there is no closing timestamp to measure to, so the
// duration runs to the last record attributed to it. Every record inside the
// episode moves it, not only operator turns.
func TestOpenRepairIsMeasuredToItsLastAttributedRecord(t *testing.T) {
	p := newProjector()
	push(p,
		agent("Added the retry loop. I also updated the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go. Revert the rest.", 2),
		agent("Reverting it.", 10),
		tool("git revert 4f2c1ab", 12),
	)
	r := p.Repairs()[0]
	if !r.LastActivity.Equal(at(12)) {
		t.Errorf("last activity = %s after a tool record inside the episode, want %s", r.LastActivity, at(12))
	}

	push(p, operator("continue", 14))
	snapshot := p.Snapshot()
	if r.Status != RepairOpen {
		t.Fatalf("episode status = %s after a continuation, want %s", r.Status, RepairOpen)
	}
	if r.Closed != nil {
		t.Errorf("an open episode carries closed=%s", r.Closed)
	}
	if !r.LastActivity.Equal(at(14)) {
		t.Errorf("last activity = %s after an operator turn inside the episode, want %s", r.LastActivity, at(14))
	}
	if got := snapshot.RepairSeconds.Num; got != 13*60 {
		t.Errorf("repair_seconds = %v for an episode open 13:01 → 13:14, want %v", got, 13*60)
	}
}

// A record outside the episode says nothing about it. The episode's own
// endpoints stay where its last attributed record left them.
func TestRecordsOutsideTheEpisodeDoNotMoveItsEndpoints(t *testing.T) {
	p := newProjector()
	push(p,
		agent("Added the retry loop. I also updated the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go. Revert the rest.", 2),
		agent("Reverted it.", 10),
		operator("Good. Run the suite.", 20),
	)
	r := p.Repairs()[0]
	push(p, agent("Suite is green.", 30), tool("go test ./...", 31), operator("Ship it.", 40))
	if !r.LastActivity.Equal(at(10)) {
		t.Errorf("last activity = %s after records outside the episode, want %s", r.LastActivity, at(10))
	}
	if r.Closed == nil || !r.Closed.Equal(at(20)) {
		t.Errorf("closed = %v after records outside the episode, want %s", r.Closed, at(20))
	}
}

// A recurrence reopens the episode: the closing timestamp is dropped, and the
// duration goes back to running against the records still arriving.
func TestReopenedRepairMeasuresToActivityAgain(t *testing.T) {
	p := newProjector()
	push(p,
		agent("Added the retry loop. I also updated the release workflow.", 1),
		operator("No. The scope is internal/worker/retry.go. Revert the rest.", 2),
		agent("Reverted it.", 10),
		operator("Good. Run the suite.", 20),
		agent("Suite is green.", 25),
	)
	r := p.Repairs()[0]
	push(p, operator("No. The scope is internal/worker/retry.go. You touched the workflow again.", 30))
	snapshot := p.Snapshot()
	if r.Closed != nil {
		t.Errorf("a reopened episode still carries closed=%s", r.Closed)
	}
	if !r.LastActivity.Equal(at(30)) {
		t.Errorf("last activity = %s after the recurrence, want %s", r.LastActivity, at(30))
	}
	if got := snapshot.RepairSeconds.Num; got != 29*60 {
		t.Errorf("repair_seconds = %v for an episode reopened at 13:30, want %v", got, 29*60)
	}
}
