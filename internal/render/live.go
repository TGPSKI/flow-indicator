package render

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
	"github.com/TGPSKI/flow-indicator/pkg/panel"
)

// can reports whether the classifier behind a snapshot could establish a fact.
//
// A snapshot carrying no capabilities answers no to everything. That is the
// right answer for a session stored before classifiers declared any: nothing
// recorded that the fact was reachable, and inventing the claim now would put a
// measurement's authority behind a build nobody can identify.
func can(s metrics.Snapshot, c classify.Capability) bool {
	return slices.Contains(s.Capabilities, string(c))
}

// Width bounds for the one-screen view, in total columns including the box
// rule. The view follows the terminal between them: narrower than the minimum
// and the metric rows stop being readable, wider than the maximum and the
// provenance column ends up an eye-movement away from the value it marks.
const (
	liveWidthMin = 46
	liveWidthMax = 96
)

// liveBox is the rule the one-screen view draws inside.
var liveBox = panel.Box{Title: "flow-indicator", Min: liveWidthMin, Max: liveWidthMax}

// activeWithin is how recently a record must have arrived for the stream to be
// reported as moving. Beyond it the status line says how long it has been
// quiet, which is a fact; it does not say the agent is finished, which is not
// in the record.
const activeWithin = 5 * time.Second

// highlightFor is how long a value stays marked after it moves. The metrics
// recompute once per operator turn, so this is a mark on what that turn
// changed, not an animation.
const highlightFor = 4 * time.Second

// spinnerFrames advance once per draw while records are landing.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// unknownGlyph stands in for a value the evidence does not establish. It is
// neither a zero nor a word: a reader scanning a column of numbers sees a gap
// where there is no measurement, which is what an unknown is.
const unknownGlyph = "—"

// idleGlyph marks a stream with nothing arriving. The spinner runs while the
// agent's records are landing; an open circle is the resting state.
const idleGlyph = "○"

// stoppedGlyph marks a source no longer being followed.
const stoppedGlyph = "■"

// Colour is an additional channel and never the only one. Every state is
// spelled out in words, every unknown is a glyph, and a degraded phase carries
// a severity glyph as well, so a terminal with attributes stripped loses
// emphasis and no information.
const (
	// Phase colours step from the resting state towards warning as the cost of
	// steering rises.
	ansiPhaseFlow     = "\033[38;2;163;112;247m" // #a370f7
	ansiPhaseDrift    = "\033[38;2;227;179;65m"  // #e3b341
	ansiPhaseRecovery = "\033[38;2;240;136;62m"  // #f0883e
	ansiPhaseThrash   = "\033[38;2;248;81;73m"   // #f85149
	ansiPhaseReset    = "\033[38;2;57;197;207m"  // #39c5cf

	// Movement colours mark a value that changed on the last operator turn.
	// Colour only: weight belongs to the phase alone. A change with no cost
	// direction carries no mark, because emphasis with nothing to say about
	// better or worse is noise competing with the lines that do.
	ansiWorse   = "\033[38;2;248;81;73m"
	ansiBetter  = "\033[38;2;86;211;100m" // #56d364
	ansiNeutral = ""

	// The temporal rows use muted variants of the same hues. They carry the
	// same meaning as the phase — cost rising or falling — so they must not
	// carry it at the same intensity, or the phase stops being the thing the
	// eye lands on first.
	ansiTrendWorse  = "\033[38;2;201;74;70m" // muted #c94a46
	ansiTrendBetter = "\033[38;2;63;185;80m" // muted #3fb950
)

// Age tiers for the temporal rows. A fact that just landed is worth the eye;
// the same fact twenty minutes later is context.
//
// Nothing graded by them is bold. Weight is reserved for the phase, which is
// the one fact that must be readable from across the room; a trend competing
// for it would make two things dominant and therefore neither.
const (
	ageFresh  = 6 * time.Second
	ageRecent = 90 * time.Second
	ageStale  = 30 * time.Minute
)

// liveDecay grades a temporal line by how long ago it happened.
var liveDecay = panel.Decay{Fresh: ageFresh, Recent: ageRecent}

// phaseColors maps a phase to its colour. A phase with no entry falls back to
// the resting colour rather than going unpainted.
var phaseColors = map[string]string{
	"FLOW":     ansiPhaseFlow,
	"DRIFT":    ansiPhaseDrift,
	"RECOVERY": ansiPhaseRecovery,
	"THRASH":   ansiPhaseThrash,
	"RESET":    ansiPhaseReset,
}

// Status is what the observer can see about the stream right now. Every field
// is an observation. Nothing here reports what the agent is doing, because the
// transcript does not carry that.
type Status struct {
	// Records is how many records have arrived since the last operator turn.
	Records int
	// Since is how long ago the most recent record arrived.
	Since time.Duration
	// Waiting is true when the operator's own turn was the last record to
	// arrive, so nothing has come back yet.
	Waiting bool
	// Seen is true once an operator turn has been observed. Until then there
	// is no turn for Records to be counted since, and the line says so rather
	// than reporting a count against a turn that has not happened.
	Seen bool
	// Elapsed is the span of the stream itself, from its first record to its
	// most recent: the session's own clock, not the observer's. A watch
	// started mid-session measures from where it started reading, which is the
	// span it can actually see.
	Elapsed time.Duration
	// Pulse increments on every draw and drives the spinner.
	Pulse int
	// Stopped is true once the source is no longer being followed, which makes
	// the final draw state that it is final rather than look merely quiet.
	Stopped bool
	// Semantic describes the bounded local-model lane when one is configured.
	// It is operational evidence about this instrument, not a domain metric.
	Semantic     *classify.Operational
	ModelDetails bool
}

// LiveView is what the one-screen renderer draws. It is a value: the renderer
// holds no state and computes nothing.
type LiveView struct {
	// Name is the harness's name for the session. Empty falls back to the
	// stream identifier.
	Name     string
	StreamID string
	// Turn is the operator turn ordinal, not the record ordinal.
	Turn       int
	Regime     string
	Snapshot   metrics.Snapshot
	Trail      []string
	Thresholds config.Thresholds
	// Width is the total width in columns. Zero lets the table's own natural
	// width decide, up to the maximum.
	Width  int
	Color  bool
	Status Status
	// Moves marks metrics that changed on the last operator turn, by the
	// direction of the change: worse, better, or moved without a direction.
	// The caller owns the memory this is derived from; see Highlights.
	Moves map[string]Move
	// Trend is the most recent band crossing the projector reported, or the
	// zero value when none has been reported yet.
	Trend TrendNote
	// Event is the most recent notable transition.
	Event EventNote
}

// TrendNote is one metric that crossed between quality bands and held there.
// It is the temporal reading the per-turn numbers cannot give: a single turn
// in a worse band is a turn, and this is a direction.
type TrendNote struct {
	Metric    string
	From      string
	To        string
	Degrading bool
	// Age is how long ago the crossing was reported. A trend stands until
	// another supersedes it, so age is what separates "just happened" from
	// "has been true for a while".
	Age time.Duration
}

// EventNote is the most recent notable transition, and how many came before
// it.
//
// Only the newest is spelled out. A chain of six joined by arrows is a
// sentence the eye has to parse; the count carries the rest, and `inspect`
// carries the detail.
type EventNote struct {
	Text    string
	Age     time.Duration
	Earlier int
}

// line renders the event with its backlog count.
func (e EventNote) line() string {
	if e.Text == "" {
		return ""
	}
	if e.Earlier > 0 {
		return fmt.Sprintf("%s · %d earlier", e.Text, e.Earlier)
	}
	return e.Text
}

// trendLabels shorten a metric identifier to the word the table already uses
// for it, so the note reads in the same vocabulary as the row it refers to.
var trendLabels = map[string]string{
	"serialization_inflation": "SI",
	"control_plane_burden":    "CPB",
	"forward_work_share":      "forward",
	"dereference":             "DRP",
	"repair_depth":            "depth",
}

// Text renders the note. An empty note renders nothing.
func (t TrendNote) Text() string {
	if t.Metric == "" {
		return ""
	}
	label, ok := trendLabels[t.Metric]
	if !ok {
		label = t.Metric
	}
	return label + " " + t.From + " → " + t.To
}

// Move is the direction a value went, in cost terms rather than arithmetic
// ones: a rising forward-work share and a falling control burden are both
// MoveBetter.
type Move = panel.Sense

const (
	MoveNone    = panel.SenseNone
	MoveNeutral = panel.SenseNeutral
	MoveBetter  = panel.SenseBetter
	MoveWorse   = panel.SenseWorse
)

// inflationMark labels serialization inflation against the configured
// thresholds. An unknown value carries no mark.
func inflationMark(v metrics.Value, t config.Thresholds) string {
	switch {
	case t.SerializationHigh > 0 && v.AtLeast(t.SerializationHigh):
		return " high"
	case t.SerializationWarn > 0 && v.AtLeast(t.SerializationWarn):
		return " warn"
	default:
		return ""
	}
}

// columnHeads name the cell columns. The provenance column is headed too: its
// letters are the least self-explaining thing on screen, and the footnote that
// used to carry that job is gone.
var columnHeads = []string{"value", "detail", "detail"}

const provHead = "class"

// splitValue formats a measurement into its figure and its unit, so the
// figures can be aligned without the units ragging them. The report's own
// helpers spell an unknown out in words, which is right for a document and
// wrong for a column.
func splitValue(v metrics.Value) (numeric, unit string) {
	if !v.Known {
		return unknownGlyph, ""
	}
	return fmt.Sprintf("%.0f", v.Num), ""
}

func splitRatio(v metrics.Value, suffix string) (numeric, unit string) {
	if !v.Known {
		return unknownGlyph, ""
	}
	return fmt.Sprintf("%.1f", v.Num), suffix
}

func splitPercent(v metrics.Value) (numeric, unit string) {
	if !v.Known {
		return unknownGlyph, ""
	}
	return fmt.Sprintf("%.0f", v.Num*100), "%"
}

// Live renders the one-screen view. Provenance markers close each row: O
// observed, C classified, D derived.
func Live(v LiveView) string {
	table := metricTable(v.Snapshot, v.Thresholds)
	inner := liveBox.Inner(v.Width, table.Natural())

	var lines []string
	add := func(s string) { lines = append(lines, s) }

	// Identity: the name the operator recognizes, at full width, with enough
	// of the identifier to tell two sessions of the same name apart. An
	// unnamed stream shows the identifier once rather than twice.
	add(panel.Ends(sessionName(v), panel.Paint(trailingID(v), panel.Dim, v.Color), inner))
	add("")

	// The hero: phase, turn and elapsed, each paired with the fact that
	// belongs with it — when the stream last moved, what has arrived since
	// your turn, and which way the session is trending.
	phaseLabel, phaseValue := phaseParts(v)
	turnLabel, turnValue := turnParts(v)
	elapsedLabel, elapsedValue := elapsedParts(v)
	add(panel.Ends(phaseLabel+phaseValue, statusTiming(v.Status, v.Color), inner))
	add(panel.Ends(turnLabel+turnValue, statusRecords(v.Status, v.Color), inner))
	add(panel.Ends(elapsedLabel+elapsedValue, "", inner))

	// The two temporal channels are hero facts, not a footer. They report over
	// different spans: a trend is the last durable band crossing and stands
	// until another supersedes it; an event is the newest transition and rolls.
	// Both lines are always drawn, so nothing below them moves.
	add(heroRow("TREND", trendNote(v), v))
	add(heroRow("EVENTS", eventNote(v), v))
	if v.Status.Semantic != nil {
		add(heroRow("MODEL", semanticUse(v.Status.Semantic), v))
		if failures := semanticFailures(v.Status.Semantic); failures != "" {
			add(heroRow("MODEL FAIL", failures, v))
		}
		if v.Status.ModelDetails {
			add(heroRow("MODEL JOBS", semanticStatus(v.Status.Semantic), v))
			if timing := semanticTiming(v.Status.Semantic); timing != "" {
				add(heroRow("MODEL TIME", timing, v))
			}
			if v.Status.Semantic.LastError != "" {
				add(heroRow("MODEL WHY", v.Status.Semantic.LastError, v))
			}
		}
	}
	add("")

	lines = append(lines, table.Draw(inner, panel.Options{Color: v.Color, Style: v.style})...)
	return liveBox.Render(inner, lines)
}

// style is the attribute a cell carries because it moved on the last operator
// turn. It is the seam between the table's layout and what the numbers mean.
func (v LiveView) style(id string) string { return moveANSI(v.Moves[id]) }

// sessionName is the name to show for the stream, falling back to the
// identifier when the harness has not named it.
func sessionName(v LiveView) string {
	if v.Name != "" {
		return v.Name
	}
	return sessionLabel(v.StreamID)
}

// sessionLabelMax is how long an identifier may be before the pane shortens it.
//
// A name an operator chose is worth reading in full. A generated key is not:
// past this length it is a machine's identifier, and all a reader needs is
// enough of it to know which session they are looking at.
const sessionLabelMax = 24

// sessionLabel is the identifier as the pane shows it.
//
// Harnesses generate long keys — a UUID, or opencode's `ses_` string — and in a
// thirty-column pane the identifier would be the only thing on screen. Short
// identifiers are left alone, because a stream someone named `recent-thrash`
// should read as `recent-thrash`.
func sessionLabel(id string) string {
	if len(id) <= sessionLabelMax {
		return id
	}
	// The leading segment of a dashed key is the handle `sessions` prints, so
	// the pane and the listing call one session by one name. A segment too
	// short to identify anything is not a handle, and the key is cut instead.
	if i := strings.IndexByte(id, '-'); i >= sessionHandleMin {
		return id[:i]
	}
	return panel.Fit(id, sessionHandleMin+4)
}

// sessionHandleMin is the shortest leading segment that still identifies a
// session. It matches the handle width the listing prints.
const sessionHandleMin = 8

// trailingID is the short identifier shown after the name. It is empty when
// the name is the identifier, because printing the same stream twice on one
// line tells the reader nothing.
func trailingID(v LiveView) string {
	if v.Name == "" {
		return ""
	}
	return shortID(v.StreamID)
}

// shortID is the leading segment of a stream identifier: enough to tell two
// sessions apart, short enough to sit beside the name.
func shortID(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	return panel.Fit(id, 8)
}

// heroLabelW is the width the hero labels share, so they end at the same
// column and their values start at the same one.
const heroLabelW = 7

// phaseParts is the top line of the hero: the label at rest, the phase word
// carrying the state in weight and colour.
//
// The data model calls the state a regime; the view says phase, which is the
// word an operator reading a meter already has.
func phaseParts(v LiveView) (label, value string) {
	phase := v.Regime
	if phase == "" {
		phase = "?"
	}
	color, ok := phaseColors[phase]
	if !ok {
		color = ansiPhaseFlow
	}
	value = panel.Paint(phase, panel.Bold+color, v.Color)

	// FLOW is the resting state and carries no glyph: a marker beside it says
	// nothing. Every other phase keeps one, so severity survives with colour
	// stripped.
	if st, found := microStyles[phase]; found && phase != "FLOW" && st.glyph != "" {
		value += " " + panel.Paint(st.glyph, color, v.Color)
	}
	return heroLabel("PHASE", v.Color), value
}

// turnParts is the second line of the hero: the operator turn ordinal.
func turnParts(v LiveView) (label, value string) {
	return heroLabel("TURN", v.Color), fmt.Sprintf("%d", v.Turn)
}

// elapsedParts is the third line of the hero: how long the session has run.
//
// The span is the stream's own, measured between the first and last record it
// carries, so a replayed transcript reports the session that happened rather
// than how long the replay took.
func elapsedParts(v LiveView) (label, value string) {
	text := unknownGlyph
	if v.Status.Elapsed > 0 {
		text = panel.ShortSpan(v.Status.Elapsed)
	}
	return heroLabel("ELAPSED", v.Color), text
}

// heroLabel rests a hero label back in its shared column.
func heroLabel(label string, color bool) string {
	return panel.Paint(panel.PadLeft(label, heroLabelW)+"  ", panel.Dim, color)
}

// heroRow is one labelled hero line: the label at rest, the value beside it.
func heroRow(label, value string, v LiveView) string {
	return heroLabel(label, v.Color) + orGlyph(value)
}

// trendNote renders the most recent band crossing, coloured by which way it
// went and graded by how long ago it went there. A crossing older than the
// stale threshold is dropped: it is no longer describing this session.
func trendNote(v LiveView) string {
	text := v.Trend.Text()
	if text == "" || v.Trend.Age >= ageStale {
		return ""
	}
	hue := ansiTrendBetter
	if v.Trend.Degrading {
		hue = ansiTrendWorse
	}
	return panel.Paint(text, liveDecay.Emphasis(v.Trend.Age, hue), v.Color)
}

// eventNote renders the newest transition, graded by age. The words never
// change with age; only the emphasis does.
func eventNote(v LiveView) string {
	text := v.Event.line()
	if text == "" {
		return ""
	}
	return panel.Paint(text, liveDecay.Emphasis(v.Event.Age, ""), v.Color)
}

func semanticStatus(s *classify.Operational) string {
	if s == nil {
		return ""
	}
	coverage := "no requests"
	if s.Requested > 0 {
		coverage = fmt.Sprintf("%d answers", s.Completed)
	}
	parts := []string{coverage}
	if active := s.Pending - s.CatchUp; active > 0 {
		parts = append(parts, fmt.Sprintf("%d active", active))
	}
	if s.CatchUp > 0 {
		parts = append(parts, fmt.Sprintf("%d catching up", s.CatchUp))
	}
	return strings.Join(parts, " · ")
}

func semanticFailures(s *classify.Operational) string {
	var parts []string
	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d errors", s.Failed))
	}
	if s.TimedOut > 0 {
		parts = append(parts, fmt.Sprintf("%d timed out", s.TimedOut))
	}
	if s.Dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d dropped", s.Dropped))
	}
	return strings.Join(parts, " · ")
}

func semanticUse(s *classify.Operational) string {
	return fmt.Sprintf("%d/%d validated · %d changed", s.Completed, s.Eligible, s.Changed)
}

func semanticTiming(s *classify.Operational) string {
	if s.P95MS > 0 {
		return fmt.Sprintf("95%% completed within %dms", s.P95MS)
	}
	if s.LastMS > 0 {
		return fmt.Sprintf("last request took %dms", s.LastMS)
	}
	return ""
}

// orGlyph substitutes the unknown glyph for an empty value, so a row that has
// nothing to say still occupies its line.
func orGlyph(s string) string {
	if s == "" {
		return unknownGlyph
	}
	return s
}

// statusTiming is when the stream last moved. It sits with the phase, because
// a phase is only as current as the record it was computed from.
func statusTiming(st Status, color bool) string {
	if st.Stopped {
		return panel.Paint("stopped", panel.Dim, color)
	}
	if st.Since < activeWithin {
		return panel.Paint("last "+panel.ShortSeconds(st.Since.Seconds())+" ago", panel.Dim, color)
	}
	return panel.Paint("quiet "+panel.ShortSeconds(st.Since.Seconds()), panel.Dim, color)
}

// statusRecords is what has arrived since the operator's last turn. It sits
// with the turn, which is what the count is measured against.
//
// The spinner runs while records are landing. At rest it is an open circle: a
// still glyph beside a quiet-for counter that keeps climbing, which is what
// carries liveness once nothing is arriving.
func statusRecords(st Status, color bool) string {
	if st.Stopped {
		return panel.Paint(stoppedGlyph+" source no longer followed", panel.Dim, color)
	}
	glyph := idleGlyph
	if st.Since < activeWithin {
		glyph = spinner(st.Pulse)
	}

	var what string
	switch {
	case st.Waiting:
		what = "your turn sent · nothing back yet"
	case !st.Seen:
		what = fmt.Sprintf("%d records · no operator turn yet", st.Records)
	case st.Records == 1:
		what = "1 record since your turn"
	default:
		what = fmt.Sprintf("%d records since your turn", st.Records)
	}
	return glyph + " " + panel.Paint(what, panel.Dim, color)
}

// spinner is the frame for a pulse count, which the caller may have driven
// negative.
func spinner(pulse int) string {
	n := len(spinnerFrames)
	return spinnerFrames[((pulse%n)+n)%n]
}

// metricTable builds every family's line. Cell counts differ by row on
// purpose; the layout pads them into shared columns.
func metricTable(s metrics.Snapshot, t config.Thresholds) panel.Table {
	return panel.Table{
		Rows:     metricRows(s, t),
		Heads:    columnHeads,
		TailHead: provHead,
	}
}

// metricRows builds every family's cells.
func metricRows(s metrics.Snapshot, t config.Thresholds) []panel.Row {
	siNum, siUnit := splitRatio(s.SerializationInfl, "x")
	baseNum, baseUnit := splitValue(s.BaselineChars)
	cpbNum, cpbUnit := splitPercent(s.ControlBurden)
	fwdNum, fwdUnit := splitPercent(s.ForwardShare)

	// Three different facts share one column here, and the row is only honest if
	// they read differently.
	//
	// A known status is a word and sits with the words. An unknown one — stated
	// by the classifier or absent — takes the glyph every other unknown takes.
	// The third is a build whose classifier cannot establish repair at all: the
	// glyph would then stand on every turn of the session and read as a
	// measurement that kept coming back unknown, when nothing was measured. That
	// case says so instead.
	pollution := panel.Cell{ID: "pollution", Detail: "unknown"}
	switch {
	case !can(s, classify.CapVerifiedRepair):
		pollution = panel.Cell{ID: "pollution", Detail: "not measured here"}
	case s.PollutionStatus != "" && s.PollutionStatus != metrics.PollutionUnknown:
		pollution = panel.Cell{ID: "pollution", Detail: s.PollutionStatus}
	}
	recovery := "latest " + orGlyph(s.RepairStatus)
	if s.RepairStatus == "open" {
		recovery = "active open"
	}
	if s.RepairID == "" {
		recovery = "none"
	}

	return []panel.Row{
		{Label: "Transmission", Tail: "D", Cells: []panel.Cell{
			{ID: "si", Numeric: siNum, Unit: siUnit, Detail: "SI" + inflationMark(s.SerializationInfl, t)},
			{ID: "baseline", Numeric: baseNum, Unit: baseUnit, Detail: "baseline"},
			{ID: "now", Numeric: panel.Compact(s.UserChars), Detail: "now"},
		}},
		{Label: "Control", Tail: "D", Cells: []panel.Cell{
			{ID: "cpb", Numeric: cpbNum, Unit: cpbUnit, Detail: "CPB"},
			{ID: "forward", Numeric: fwdNum, Unit: fwdUnit, Detail: "forward"},
		}},
		{Label: "Obligations", Tail: "D", Cells: []panel.Cell{
			{ID: "unresolved", Numeric: panel.Compact(s.UnresolvedObligations), Detail: "candidate inventory"},
			{ID: "new", Numeric: "+" + panel.Compact(s.NewObligations), Detail: "new"},
			{ID: "repeated", Numeric: panel.Compact(s.RepeatedObligations), Detail: "repeated"},
		}},
		{Label: "Dereference", Tail: "D", Cells: []panel.Cell{
			{ID: "drp", Numeric: fmt.Sprintf("%d/%d", s.PointerSuccess, s.PointerSuccess+s.PointerFailure), Detail: "resolved"},
			{ID: "unknown", Numeric: panel.Compact(s.PointerUnknown), Detail: "unknown"},
		}},
		{Label: "Recovery", Tail: "D", Cells: []panel.Cell{
			{ID: "depth", Numeric: panel.Compact(s.RepairDepth), Detail: "depth · " + recovery},
			{ID: "repair_chars", Numeric: panel.Compact(s.RepairChars), Detail: "chars"},
			{ID: "repair_records", Numeric: panel.Compact(s.RepairRecords), Detail: "records"},
		}},
		{Label: "Pollution", Tail: "C", Cells: []panel.Cell{
			pollution,
			{ID: "expansions", Numeric: panel.Compact(s.Expansions), Detail: "observed expansions"},
		}},
	}
}

// moveANSI is the attribute a changed value carries. Direction is in cost
// terms, not arithmetic ones.
func moveANSI(m Move) string {
	switch m {
	case MoveWorse:
		return ansiWorse
	case MoveBetter:
		return ansiBetter
	case MoveNeutral:
		return ansiNeutral
	default:
		return ""
	}
}

// Compact renders the single-line form for a stream with nothing to report.
func Compact(v LiveView) string {
	s := v.Snapshot
	repair := "-"
	if s.RepairDepth > 0 {
		repair = fmt.Sprintf("depth %d", s.RepairDepth)
	}
	return fmt.Sprintf("%-8s SI %-7s%s CPB %-5s OBL %-3d DRP %-6s REPAIR %s",
		v.Regime, ratioOf(s.SerializationInfl, "x"), inflationMark(s.SerializationInfl, v.Thresholds),
		percentOf(s.ControlBurden), s.UnresolvedObligations, percentOf(s.Dereference), repair)
}
