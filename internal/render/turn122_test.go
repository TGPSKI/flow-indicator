package render

import (
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
)

func TestTurn122StatesCoverageAndUnknowns(t *testing.T) {
	v := sampleView(96)
	v.Snapshot.TurnIndex = 122
	v.Snapshot.PointerSuccess, v.Snapshot.PointerFailure, v.Snapshot.PointerUnknown = 2, 2, 26
	v.Snapshot.RepairID, v.Snapshot.RepairStatus = "rep-4", "unknown"
	v.Snapshot.Capabilities = []string{string(classify.CapVerifiedRepair)}
	v.Snapshot.PollutionStatus = metrics.PollutionUnknown
	v.Status.Semantic = &classify.Operational{Eligible: 122, Completed: 44, Failed: 20, Dropped: 58}
	out := Live(v)
	for _, want := range []string{"44/122 validated", "58", "candidate inventory", "2/4", "26", "unknown", "latest unknown", "observed expansions"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}
