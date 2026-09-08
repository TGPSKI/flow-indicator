package render

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/TGPSKI/flow-indicator/internal/metrics"
	"github.com/TGPSKI/flow-indicator/pkg/panel"
)

// The micro view is the side-pane meter: three to eight lines answering one
// question at a glance, "am I still conducting the work, or am I now
// maintaining the agent?"
//
// It is not a small copy of the one-screen view. That view lists every family
// so a reader can audit them; this one shows the regime, the two or three
// numbers that carry the current state, and the smallest deterministic reason.
// `inspect` remains the evidence path, and nothing here is a diagnosis.

// microWidth is the default inner width, in columns. A pane beside an agent
// IDE is narrow, and the content is laid out to survive it.
const microWidth = 30

// MicroDefaultWidth exposes that default, so a caller can tell whether the
// terminal it is drawing into is narrower still.
const MicroDefaultWidth = microWidth

// Severity ordering. The word is always present and always first: colour is an
// additional channel, never the only one.
const (
	glyphFlow     = "·"
	glyphDrift    = "!"
	glyphRecovery = "!!"
	glyphThrash   = "!!!"
	glyphReset    = "~"
)

// ANSI attributes. Emphasis rises with severity alongside the word and glyph.
const (
	ansiFlow      = "\033[2m"    // dim
	ansiDrift     = "\033[33m"   // yellow
	ansiRecovery  = "\033[1;33m" // bold yellow
	ansiThrash    = "\033[1;31m" // bold red
	ansiResetWord = "\033[36m"   // cyan
)

// MicroView is what the side-pane renderer draws.
type MicroView struct {
	Regime   string
	Snapshot metrics.Snapshot
	// Turn is the operator turn ordinal, not the record ordinal.
	Turn int
	// Status carries the session span and what has arrived since the last
	// operator turn. Its zero value draws neither line.
	Status Status
	// Trend is the most recent band crossing, or the zero value for none.
	Trend TrendNote
	// Event is the most recent notable transition.
	Event EventNote
	// Trail is the bounded regime history, oldest first, already deduplicated
	// by the caller.
	Trail []string
	// Width is the inner width in columns. Zero uses microWidth.
	Width int
	// Color enables ANSI attributes. The word and glyph carry the severity
	// without it.
	Color bool
}

// regimeStyle is the per-regime presentation.
type regimeStyle struct {
	glyph string
	ansi  string
}

var microStyles = map[string]regimeStyle{
	"FLOW":     {glyphFlow, ansiFlow},
	"DRIFT":    {glyphDrift, ansiDrift},
	"RECOVERY": {glyphRecovery, ansiRecovery},
	"THRASH":   {glyphThrash, ansiThrash},
	"RESET":    {glyphReset, ansiResetWord},
}

// microLabelW is the width the side pane's labels share, so every value in the
// pane starts at one column. It matches the one-screen view's grammar: the
// label rests back on the left, the value carries the weight.
const microLabelW = 7

// Micro renders the side-pane meter.
//
// It is the one-screen view's layout at one column: the same labelled rows,
// the same right-aligned figures with their units attached, the same glyph for
// an unmeasured value, and the same phase palette. A reader moving between the
// two views is reading one instrument, not two.
func Micro(v MicroView) string {
	width := v.Width
	if width <= 0 {
		width = microWidth
	}
	pane := panel.Pane{LabelW: microLabelW, Width: width, Color: v.Color}
	valueW := pane.ValueW()

	var b strings.Builder
	row := func(label, value, ansi string) {
		b.WriteString(pane.Row(label, value, ansi))
		b.WriteString("\n")
	}
	blank := func() { b.WriteString("\n") }

	st, ok := microStyles[v.Regime]
	if !ok {
		st = regimeStyle{glyph: "?", ansi: ansiFlow}
	}

	// The hero, in the same order and with the same labels the one-screen view
	// uses. FLOW is the resting state and carries no glyph; every other phase
	// keeps one, so severity survives with colour stripped.
	phase := v.Regime
	if st.glyph != "" && v.Regime != "FLOW" {
		phase += " " + st.glyph
	}
	row("PHASE", phase, phaseANSI(v.Regime))
	row("TURN", fmt.Sprintf("%d", v.Turn), "")
	row("ELAPSED", orGlyph(panel.ShortSpan(v.Status.Elapsed)), "")
	row("STATUS", orGlyph(microStatus(v.Status, valueW)), "")
	// The two temporal channels sit with the hero facts, above the numbers.
	row("TREND", orGlyph(trendNote(LiveView{Trend: v.Trend, Color: v.Color})), "")
	row("EVENTS", orGlyph(eventNote(LiveView{Event: v.Event, Color: v.Color})), "")
	if v.Status.Semantic != nil {
		row("MODEL", semanticUse(v.Status.Semantic), "")
	}
	blank()

	// The measurements, one per line, figures right-aligned against a shared
	// column with their units attached.
	for _, m := range microMetrics(v.Snapshot) {
		row(m.label, m.value(microFigureW(v.Snapshot)), "")
	}
	row("REPAIR", microRepair(v.Snapshot), "")

	// The reason the state is what it is, at most two lines, only when the
	// state is not FLOW. `inspect` remains the evidence path. Both lines are
	// always drawn so nothing below them moves.
	blank()
	var reasons []string
	if v.Regime != "FLOW" {
		reasons = microReasons(v.Snapshot)
	}
	for i := range 2 {
		label, text := "", ""
		if i == 0 {
			label = "WHY"
		}
		if i < len(reasons) {
			text = reasons[i]
		}
		if i == 0 {
			text = orGlyph(text)
		}
		row(label, text, st.ansi)
	}
	row("TRAIL", orGlyph(microTrail(v.Trail, valueW)), panel.Dim)
	return b.String()
}

// microMetric is one labelled measurement in the pane.
type microMetric struct {
	label   string
	numeric string
	unit    string
}

// value renders the metric with its figure right-aligned in a shared column
// and its unit attached, the way the one-screen view lays a cell out.
func (m microMetric) value(figureW int) string {
	return panel.PadLeft(m.numeric, figureW) + m.unit
}

// microMetrics are the measurements the pane carries. SI is the headline
// number; CPB is the share of operator characters spent steering rather than
// advancing; DRP is the dereference proxy. The fourth is the count the drift
// rules read, named for whichever rule decided the state, so a warning is
// never sat beside a zero it had nothing to do with.
func microMetrics(s metrics.Snapshot) []microMetric {
	siNum, siUnit := splitRatio(s.SerializationInfl, "x")
	cpbNum, cpbUnit := splitPercent(s.ControlBurden)
	drpNum, drpUnit := splitPercent(s.Dereference)

	fourth := microMetric{"ctl", fmt.Sprintf("%d/%d",
		s.RecentCorrections+s.RecentInterrupts, s.DriftWindowTurns), ""}
	if s.RegimeRule == metrics.RuleDriftDereference {
		fourth = microMetric{"ref", fmt.Sprintf("%d/%d",
			s.RecentDereferenceMisses, s.DriftWindowTurns), ""}
	}
	return []microMetric{
		{"SI", siNum, siUnit},
		{"CPB", cpbNum, cpbUnit},
		{"DRP", drpNum, drpUnit},
		fourth,
	}
}

// microFigureW is the width the figures share, so they line up down the pane.
func microFigureW(s metrics.Snapshot) int {
	w := 0
	for _, m := range microMetrics(s) {
		w = max(w, utf8.RuneCountInString(m.numeric))
	}
	return w
}

// Status precedes cost so a narrow pane cannot describe a closed repair as active.
func microRepair(s metrics.Snapshot) string {
	if s.RepairDepth == 0 {
		return unknownGlyph
	}
	status := "latest " + orUnknown(s.RepairStatus)
	if s.RepairStatus == "open" {
		status = "active"
	}
	out := fmt.Sprintf("%s · depth %d · %dc / %dt", status, s.RepairDepth, s.RepairChars, s.RepairRecords)
	if s.RepairSeconds.Known {
		out += " / " + panel.ShortSeconds(s.RepairSeconds.Num)
	}
	return out
}

// phaseANSI is the phase colour, bold, so the side pane and the one-screen
// view carry the same palette.
func phaseANSI(phase string) string {
	color, ok := phaseColors[phase]
	if !ok {
		return ansiFlow
	}
	return panel.Bold + color
}

// microStatus is the liveness line: the spinner while records land, an open
// circle at rest, and the count they are measured against.
//
// The zero Status draws nothing. A caller that has no liveness to report
// should not have a line claiming the stream is quiet.
func microStatus(st Status, width int) string {
	if st.Stopped {
		return panel.Fit(stoppedGlyph+" stopped", width)
	}
	if st.Since == 0 && st.Records == 0 && !st.Seen && !st.Waiting {
		return ""
	}
	glyph := idleGlyph
	if st.Since < activeWithin {
		glyph = spinnerFrames[((st.Pulse%len(spinnerFrames))+len(spinnerFrames))%len(spinnerFrames)]
	}
	when := "quiet " + panel.ShortSeconds(st.Since.Seconds())
	if st.Since < activeWithin {
		when = panel.ShortSeconds(st.Since.Seconds()) + " ago"
	}
	if st.Waiting {
		return panel.Fit(glyph+" sent · "+when, width)
	}
	return panel.Fit(fmt.Sprintf("%s %d rec · %s", glyph, st.Records, when), width)
}

// microReasons states the rule that fired, capped at two lines.
//
// The reason is formatted from the rule the projector recorded, never guessed
// from the values on the snapshot. Several metrics are known on any given turn
// and only one comparison decided the state; naming a known metric that no rule
// read tells the operator something false about why the meter moved.
func microReasons(s metrics.Snapshot) []string {
	var out []string
	add := func(format string, args ...any) {
		if len(out) < 2 {
			out = append(out, fmt.Sprintf(format, args...))
		}
	}

	switch s.RegimeRule {
	case metrics.RuleThrashRepairDepth:
		add("repair depth %d", s.RepairDepth)
	case metrics.RuleThrashRecoveryChars:
		// The rule compared repair characters against the control baseline, so
		// the line reports that ratio and not repair magnification, which is a
		// different comparison against the pointer that opened the episode.
		if s.BaselineChars.Known && s.BaselineChars.Num > 0 {
			add("repair %.1fx baseline", float64(s.RepairChars)/s.BaselineChars.Num)
		} else {
			add("repair cost over baseline")
		}
	case metrics.RuleThrashObligationInflation:
		add("%s repeated", panel.Plural(s.RepeatedObligations, "obligation"))
		num, unit := splitPercent(s.ForwardShare)
		add("forward work %s%s", num, unit)
	case metrics.RuleRecoveryOpen:
		// The cost line above already carries characters and records, so the
		// reason names why the state is RECOVERY rather than restating them.
		add("repair open · depth %d", s.RepairDepth)
		if s.Expansions > 0 {
			add("%d repair expansions", s.Expansions)
		}
	case metrics.RuleDriftDereference:
		add("%s missed in %d turns", panel.Plural(s.RecentDereferenceMisses, "reference"), s.DriftWindowTurns)
	case metrics.RuleDriftControlActions:
		// The rule counts corrections and interruptions together, so the line
		// names whichever kinds the window actually holds.
		switch {
		case s.RecentCorrections > 0 && s.RecentInterrupts > 0:
			// The window total is already on the metric row, so naming the two
			// kinds is what this line adds.
			add("%s + %s", panel.Plural(s.RecentCorrections, "correction"),
				panel.Plural(s.RecentInterrupts, "interrupt"))
		case s.RecentInterrupts > 0:
			add("%s within %d turns", panel.Plural(s.RecentInterrupts, "interrupt"), s.DriftWindowTurns)
		default:
			add("%s within %d turns", panel.Plural(s.RecentCorrections, "correction"), s.DriftWindowTurns)
		}
	case metrics.RuleDriftSerializationObligation:
		add("obligation repeat at high SI")
	case metrics.RuleReset:
		add("session reset marker")
	}
	return out
}

// microTrail renders the bounded history, trimming from the left until it fits.
func microTrail(trail []string, width int) string {
	// One entry is not a trajectory. The trail earns its line only once the
	// stream has actually moved between states.
	if len(trail) < 2 {
		return ""
	}
	for i := range trail {
		s := strings.Join(short(trail[i:]), " ")
		if utf8.RuneCountInString(s) <= width {
			return s
		}
	}
	return ""
}

// short abbreviates regime names to initials for the trail: the trajectory is
// what matters there, not the spelling.
func short(names []string) []string {
	out := make([]string, 0, len(names)*2)
	for i, n := range names {
		if i > 0 {
			out = append(out, "→")
		}
		if n == "" {
			out = append(out, "?")
			continue
		}
		out = append(out, n[:1])
	}
	return out
}

// microTrailMax bounds the regime history the side pane shows. Enough to make
// worsening obvious; not a timeline.
const microTrailMax = 4

// MicroAt renders the side-pane meter as it stood at one turn, rebuilt from
// the stored snapshots.
//
// It is the same renderer and the same view value the live path draws, so a
// replayed checkpoint shows what the operator would have seen rather than a
// reconstruction of it.
func (s *Session) MicroAt(seq uint64, width int, color bool) (string, error) {
	var at *metrics.Snapshot
	var trail []string
	for i := range s.Snapshots {
		snap := s.Snapshots[i]
		if snap.Seq > seq {
			break
		}
		if r := snap.Regime; r != "" && (len(trail) == 0 || trail[len(trail)-1] != r) {
			trail = append(trail, r)
		}
		at = &s.Snapshots[i]
	}
	if at == nil {
		return "", fmt.Errorf("render: no snapshot at or before turn %d in session %s", seq, s.StreamID)
	}
	if len(trail) > microTrailMax {
		trail = trail[len(trail)-microTrailMax:]
	}
	return Micro(MicroView{
		Regime:   at.Regime,
		Snapshot: *at,
		Trail:    trail,
		Width:    width,
		Color:    color,
	}), nil
}
