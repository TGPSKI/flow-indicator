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
	v.Snapshot.Dereference = metrics.KnownValue(0.5)
	v.Snapshot.RepairID, v.Snapshot.RepairStatus = "rep-4", "unknown"
	v.Snapshot.Capabilities = []string{string(classify.CapVerifiedRepair)}
	v.Snapshot.PollutionStatus = metrics.PollutionUnknown
	v.Status.Semantic = &classify.Operational{Eligible: 122, Completed: 44, Failed: 20, Dropped: 58}
	v.Status.ModelDetails = true
	out := Live(v)
	for _, want := range []string{"44/122 validated", "58", "unresolved", "50% DRP", "2/4", "26", "unknown", "latest unknown", "expansions"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	v.Snapshot.Dereference = metrics.Value{}
	v.Snapshot.PointerSuccess, v.Snapshot.PointerFailure = 0, 0
	out = Live(v)
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Dereference") && (!strings.Contains(line, "—") || strings.Contains(line, "0%")) {
			t.Errorf("unknown DRP must remain unknown:\n%s", line)
		}
	}
}
