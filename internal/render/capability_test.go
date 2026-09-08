package render

import (
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/pkg/panel"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
)

// pollutionRow returns the Pollution row's cells for a snapshot.
func pollutionRow(t *testing.T, s metrics.Snapshot) []panel.Cell {
	t.Helper()
	for _, r := range metricRows(s, config.Default().Thresholds) {
		if r.Label == "Pollution" {
			return r.Cells
		}
	}
	t.Fatal("no Pollution row")
	return nil
}

// A build whose classifier cannot establish repair must not render the row the
// way an unknown measurement renders. The glyph would stand on every turn of
// the session and read as a measurement that kept coming back unknown, when
// nothing was measured at all.
func TestPollutionRowSeparatesUnmeasurableFromUnknown(t *testing.T) {
	unmeasurable := pollutionRow(t, metrics.Snapshot{
		Capabilities: classify.None{}.Capabilities().Strings(),
	})[0]
	unknown := pollutionRow(t, metrics.Snapshot{
		Capabilities:    []string{string(classify.CapVerifiedRepair)},
		PollutionStatus: metrics.PollutionUnknown,
	})[0]
	measured := pollutionRow(t, metrics.Snapshot{
		Capabilities:    []string{string(classify.CapVerifiedRepair)},
		PollutionStatus: metrics.PollutionPolluted,
	})[0]

	if unmeasurable.Numeric == unknown.Numeric && unmeasurable.Detail == unknown.Detail {
		t.Error("an unmeasurable family renders identically to an unknown measurement")
	}
	if unknown.Detail != "unknown" {
		t.Errorf("an unknown measurement renders %q, want unknown", unknown.Detail)
	}
	if unmeasurable.Numeric == unknownGlyph {
		t.Error("an unmeasurable family takes the unknown glyph")
	}
	if measured.Detail != metrics.PollutionPolluted {
		t.Errorf("a known status renders %q", measured.Detail)
	}
}

// A snapshot stored before classifiers declared capabilities claims none of
// them. Inventing the claim now would put a measurement's authority behind a
// build nobody can identify.
func TestSnapshotWithNoCapabilitiesEstablishesNothing(t *testing.T) {
	var s metrics.Snapshot
	for _, c := range []classify.Capability{
		classify.CapVerifiedRepair,
		classify.CapObligationRelease,
		classify.CapObligationSupersession,
		classify.CapObligationSatisfaction,
	} {
		if can(s, c) {
			t.Errorf("a snapshot recording no capabilities claims %s", c)
		}
	}
}

// A count of a fact the classifier could not reach is not a count. Printing the
// zero puts the session's authority behind a number nobody measured.
func TestReportSeparatesAZeroFromAnUnreachableFact(t *testing.T) {
	heuristic := metrics.Snapshot{Capabilities: classify.Heuristic{}.Capabilities().Strings()}
	none := metrics.Snapshot{Capabilities: classify.None{}.Capabilities().Strings()}

	if got := establishable(heuristic, classify.CapObligationSatisfaction, 0); !strings.Contains(got, "not established") {
		t.Errorf("an unreachable count renders %q", got)
	}
	if got := establishable(heuristic, classify.CapObligationRelease, 0); got != "0" {
		t.Errorf("a reachable count of zero renders %q, want 0", got)
	}
	if got := pollutionCell(none); !strings.Contains(got, "not established") {
		t.Errorf("the pollution status renders %q under a classifier that cannot verify repair", got)
	}
}
