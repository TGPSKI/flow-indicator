package render

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/TGPSKI/flow-indicator/internal/metrics"
)

func lines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// flat collapses runs of whitespace so an assertion states what the pane says
// rather than how it was spaced. The layout is still being iterated on; the
// facts it carries are what these tests are for.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// Severity must survive a terminal that drops attributes, so colour is never
// the only channel.
//
// The state word is always present and is itself distinguishing. FLOW is the
// resting state and carries no glyph, because a marker beside "nothing is
// wrong" is not a severity signal. Every state that is not FLOW carries a
// distinct one.
func TestSeverityIsCarriedWithoutColor(t *testing.T) {
	seen := map[string]string{}
	for _, regime := range []string{"FLOW", "DRIFT", "RECOVERY", "THRASH", "RESET"} {
		head := lines(Micro(MicroView{Regime: regime, Snapshot: metrics.Snapshot{DriftWindowTurns: 8}}))[0]
		if strings.Contains(head, "\033") {
			t.Errorf("%s: colour written while Color is false: %q", regime, head)
		}
		if !strings.Contains(head, regime) {
			t.Errorf("%s: state word missing from %q", regime, head)
		}
		_, after, _ := strings.Cut(flat(head), regime)
		glyph := strings.TrimSpace(after)
		if regime == "FLOW" {
			if glyph != "" {
				t.Errorf("FLOW carries glyph %q; the resting state needs no marker", glyph)
			}
			continue
		}
		if glyph == "" {
			t.Errorf("%s: no glyph to carry severity without colour", regime)
		}
		if prior, ok := seen[glyph]; ok {
			t.Errorf("%s shares glyph %q with %s", regime, glyph, prior)
		}
		seen[glyph] = regime
	}
}

// Colour is an additional channel when it is on, and the word stays first.
func TestColorWrapsButDoesNotReplaceTheWord(t *testing.T) {
	out := Micro(MicroView{Regime: "THRASH", Color: true, Snapshot: metrics.Snapshot{DriftWindowTurns: 8}})
	if !strings.Contains(out, "\033[") {
		t.Fatal("no ANSI attribute written while Color is true")
	}
	if !strings.Contains(out, "THRASH") {
		t.Fatal("the regime word is missing from the coloured output")
	}
}

// Missing evidence must never read as a reassuring zero.
func TestUnknownMetricsRenderAsUnknownNotZero(t *testing.T) {
	out := Micro(MicroView{Regime: "FLOW", Snapshot: metrics.Snapshot{
		SerializationInfl: metrics.Unknown(),
		ControlBurden:     metrics.Unknown(),
		DriftWindowTurns:  8,
	}})
	got := flat(out)
	if !strings.Contains(got, "SI "+unknownGlyph) {
		t.Errorf("unknown inflation did not render as unknown:\n%s", out)
	}
	if !strings.Contains(got, "CPB "+unknownGlyph) {
		t.Errorf("unknown burden did not render as unknown:\n%s", out)
	}
	if strings.Contains(got, "SI 0") || strings.Contains(got, "CPB 0%") {
		t.Errorf("unknown metric rendered as zero:\n%s", out)
	}
}

// Every rule that puts the stream in a non-FLOW state owes the reader a
// deterministic reason, and at most two.
func TestEveryNonFlowRuleShowsOneOrTwoReasons(t *testing.T) {
	// One snapshot with every value known, so a renderer that reads values
	// instead of the rule has every opportunity to name the wrong one.
	base := metrics.Snapshot{
		RepairDepth: 3, RepairChars: 900, RepairRecords: 5,
		BaselineChars:           metrics.KnownValue(120),
		RepairMagnify:           metrics.KnownValue(5.4),
		ForwardShare:            metrics.KnownValue(0.18),
		RecentCorrections:       2,
		RecentInterrupts:        1,
		RecentDereferenceMisses: 2,
		RepeatedObligations:     2,
		Expansions:              1,
		DriftWindowTurns:        8,
	}
	for _, rule := range []metrics.RegimeRule{
		metrics.RuleReset,
		metrics.RuleRecoveryOpen,
		metrics.RuleThrashRepairDepth,
		metrics.RuleThrashRecoveryChars,
		metrics.RuleThrashObligationInflation,
		metrics.RuleDriftDereference,
		metrics.RuleDriftControlActions,
		metrics.RuleDriftSerializationObligation,
	} {
		snap := base
		snap.RegimeRule = rule
		reasons := microReasons(snap)
		if len(reasons) == 0 {
			t.Errorf("%s: no reason given", rule)
		}
		if len(reasons) > 2 {
			t.Errorf("%s: %d reasons, want at most 2", rule, len(reasons))
		}
	}
	flow := base
	flow.RegimeRule = metrics.RuleFlow
	if got := microReasons(flow); len(got) != 0 {
		t.Errorf("FLOW carries reasons %v, want none", got)
	}
}

// The reason states the comparison the projector recorded. Reading the values
// instead lets a renderer name a metric that is merely known: repair
// magnification beside a depth-driven THRASH, or a correction count beside a
// DRIFT that two missed references decided.
func TestReasonNamesTheRuleThatFired(t *testing.T) {
	base := metrics.Snapshot{
		RepairDepth: 3, RepairChars: 900,
		BaselineChars:           metrics.KnownValue(120),
		RepairMagnify:           metrics.KnownValue(5.4),
		ForwardShare:            metrics.KnownValue(0.18),
		RecentCorrections:       0,
		RecentInterrupts:        0,
		RecentDereferenceMisses: 2,
		RepeatedObligations:     2,
		DriftWindowTurns:        8,
	}
	cases := []struct {
		rule   metrics.RegimeRule
		want   string
		unwant string
	}{
		{metrics.RuleThrashRepairDepth, "repair depth 3", "5.4"},
		{metrics.RuleThrashRecoveryChars, "repair 7.5x baseline", "5.4"},
		{metrics.RuleThrashObligationInflation, "2 obligations repeated", "repair depth"},
		{metrics.RuleDriftDereference, "2 references missed in 8 turns", "correction"},
		{metrics.RuleDriftSerializationObligation, "obligation repeat at high SI", "correction"},
		{metrics.RuleRecoveryOpen, "repair open · depth 3", "baseline"},
		{metrics.RuleReset, "session reset marker", "repair"},
	}
	for _, c := range cases {
		snap := base
		snap.RegimeRule = c.rule
		got := strings.Join(microReasons(snap), " | ")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: reasons = %q, want to contain %q", c.rule, got, c.want)
		}
		if strings.Contains(got, c.unwant) {
			t.Errorf("%s: reasons = %q, must not name %q, which no rule read", c.rule, got, c.unwant)
		}
	}
}

// A DRIFT that missed references decided must not put a zero control-action
// count on the metric row either: the pane would report the absence of evidence
// the rule never consulted.
func TestDereferenceDriftDoesNotShowAZeroCorrectionCount(t *testing.T) {
	out := Micro(MicroView{Regime: "DRIFT", Snapshot: metrics.Snapshot{
		RegimeRule:              metrics.RuleDriftDereference,
		SerializationInfl:       metrics.KnownValue(1.1),
		ControlBurden:           metrics.KnownValue(0.2),
		RecentDereferenceMisses: 2,
		DriftWindowTurns:        8,
	}})
	if strings.Contains(flat(out), "ctl 0/8") {
		t.Errorf("dereference-driven DRIFT reports a zero control-action count:\n%s", out)
	}
	if !strings.Contains(flat(out), "ref 2/8") {
		t.Errorf("dereference-driven DRIFT does not report the count its rule read:\n%s", out)
	}
}

// The reason names the evidence the drift rule actually counted, so a pane
// showing DRIFT after a run of interruptions says so rather than reporting
// corrections that did not happen.
func TestDriftReasonNamesWhatTheWindowHolds(t *testing.T) {
	cases := []struct {
		corrections, interrupts int
		want                    string
	}{
		{2, 0, "2 corrections within 8 turns"},
		{1, 0, "1 correction within 8 turns"},
		{0, 3, "3 interrupts within 8 turns"},
		{1, 1, "1 correction + 1 interrupt"},
		{2, 3, "2 corrections + 3 interrupts"},
	}
	for _, c := range cases {
		got := microReasons(metrics.Snapshot{
			RegimeRule:        metrics.RuleDriftControlActions,
			RecentCorrections: c.corrections,
			RecentInterrupts:  c.interrupts,
			DriftWindowTurns:  8,
		})
		if len(got) == 0 || got[0] != c.want {
			t.Errorf("corrections %d interrupts %d: reason = %v, want %q",
				c.corrections, c.interrupts, got, c.want)
		}
	}
}

// A single state is not a trajectory, and a trail must stay inside the pane.
func TestTrailIsBoundedAndEarnsItsLine(t *testing.T) {
	if got := microTrail([]string{"FLOW"}, microWidth); got != "" {
		t.Errorf("single-state trail rendered %q, want nothing", got)
	}
	got := microTrail([]string{"FLOW", "DRIFT", "RECOVERY"}, microWidth)
	if got != "F → D → R" {
		t.Errorf("trail = %q, want %q", got, "F → D → R")
	}
	long := []string{"FLOW", "DRIFT", "RECOVERY", "THRASH", "RESET", "FLOW", "DRIFT", "RECOVERY"}
	if n := utf8.RuneCountInString(microTrail(long, 12)); n > 12 {
		t.Errorf("trail is %d columns, want at most 12", n)
	}
}
