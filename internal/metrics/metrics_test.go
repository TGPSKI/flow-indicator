package metrics

import (
	"encoding/json"
	"testing"
	"time"
)

func TestUnknownMarshalsToNull(t *testing.T) {
	raw, err := json.Marshal(Unknown())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "null" {
		t.Fatalf("unknown marshalled as %s, want null", raw)
	}
	var back Value
	if err := json.Unmarshal([]byte("null"), &back); err != nil {
		t.Fatal(err)
	}
	if back.Known {
		t.Fatal("null decoded as a known value")
	}
}

func TestUnknownNeverSatisfiesAThreshold(t *testing.T) {
	if Unknown().AtLeast(0) {
		t.Error("unknown satisfied a lower bound of zero")
	}
	if Unknown().AtMost(1e9) {
		t.Error("unknown satisfied an upper bound")
	}
}

func TestBaselineUnknownBelowMinimum(t *testing.T) {
	recent := []int{10, 20, 30, 40, 50, 60, 70}
	if got := BaselineChars(recent, 8); got.Known {
		t.Fatalf("baseline known with %d turns and a minimum of 8: %v", len(recent), got)
	}
	recent = append(recent, 80)
	got := BaselineChars(recent, 8)
	if !got.Known || got.Num != 45 {
		t.Fatalf("baseline = %v, want 45", got)
	}
}

func TestSerializationInflationUnknownWithoutBaseline(t *testing.T) {
	if got := SerializationInflation(300, Unknown()); got.Known {
		t.Fatalf("inflation computed without a baseline: %v", got)
	}
	got := SerializationInflation(300, KnownValue(60))
	if !got.Known || got.Num != 5 {
		t.Fatalf("inflation = %v, want 5", got)
	}
}

func TestControlSharesUseEveryOperatorCharacter(t *testing.T) {
	b := Buckets{Forward: 20, Control: 30, Recovery: 40, Restate: 10, Other: 100}
	if got := b.ForwardWorkShare(); got.Num != 0.10 {
		t.Errorf("forward share = %v, want 0.10: unlabelled text must stay in the denominator", got)
	}
	if got := b.ControlPlaneBurden(); got.Num != 0.40 {
		t.Errorf("control burden = %v, want 0.40", got)
	}
	if got := b.RestateBurden(); got.Num != 0.05 {
		t.Errorf("restate burden = %v, want 0.05", got)
	}
	if got := (Buckets{}).ForwardWorkShare(); got.Known {
		t.Error("forward share known with no operator characters")
	}
}

func TestObligationRepeatRatioZeroDenominator(t *testing.T) {
	got := ObligationRepeatRatio(0, 0)
	if !got.Known || got.Num != 0 {
		t.Fatalf("repeat ratio with no active obligations = %v, want a known 0", got)
	}
}

func TestDereferenceUnknownWithoutResolvedOutcomes(t *testing.T) {
	if got := DereferenceReliability(0, 0); got.Known {
		t.Fatalf("proxy known with no resolved outcomes: %v", got)
	}
	got := DereferenceReliability(3, 1)
	if !got.Known || got.Num != 0.75 {
		t.Fatalf("proxy = %v, want 0.75", got)
	}
}

func TestRepairMagnificationNeedsAPointer(t *testing.T) {
	if got := RepairMagnification(500, 0); got.Known {
		t.Fatalf("magnification computed without a pointer: %v", got)
	}
	if got := RepairMagnification(500, 25); got.Num != 20 {
		t.Fatalf("magnification = %v, want 20", got)
	}
}

func TestRecoveryDurationNeedsBothEndpoints(t *testing.T) {
	start := time.Date(2026, 3, 4, 13, 0, 0, 0, time.UTC)
	if got := RecoveryDuration(start, time.Time{}); got.Known {
		t.Fatal("duration computed with a missing end")
	}
	if got := RecoveryDuration(start, start.Add(90*time.Second)); got.Num != 90 {
		t.Fatalf("duration = %v, want 90", got)
	}
}

func TestPollutionStatusRules(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name       string
		repaired   *bool
		expansions int
		corrected  bool
		want       string
	}{
		{"clean", &yes, 0, false, PollutionClean},
		{"polluted", &yes, 2, false, PollutionPolluted},
		{"polluted even when corrected next", &yes, 1, true, PollutionPolluted},
		{"failed", &no, 0, false, PollutionFailed},
		{"no evidence", nil, 0, false, PollutionUnknown},
		{"repaired but corrected anyway", &yes, 0, true, PollutionUnknown},
	}
	for _, c := range cases {
		if got := PollutionStatus(c.repaired, c.expansions, c.corrected); got != c.want {
			t.Errorf("%s: status = %s, want %s", c.name, got, c.want)
		}
	}
}
