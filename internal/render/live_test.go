package render

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
	"github.com/TGPSKI/flow-indicator/pkg/panel"
)

// boxLines returns the lines of a rendered view that are inside the box rule.
func boxLines(t *testing.T, out string) []string {
	t.Helper()
	var in []string
	for l := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(l, "│") {
			in = append(in, l)
		}
	}
	if len(in) == 0 {
		t.Fatalf("no box lines in output:\n%s", out)
	}
	return in
}

func sampleView(width int) LiveView {
	return LiveView{
		Name:     "a session with a name",
		StreamID: "e9e1d3d5-b9ff-4419-99f0-1cd3f15dcaba",
		Turn:     14,
		Regime:   "FLOW",
		Width:    width,
		Snapshot: metrics.Snapshot{
			TurnIndex:             14,
			UserChars:             33,
			BaselineChars:         metrics.KnownValue(49),
			SerializationInfl:     metrics.KnownValue(0.7),
			ControlBurden:         metrics.KnownValue(0.07),
			ForwardShare:          metrics.KnownValue(0.8),
			UnresolvedObligations: 2,
			PointerSuccess:        6,
		},
	}
}

// Every line must be exactly as wide as every other. A row that is one column
// short is the misalignment this layout exists to remove.
func TestLiveRowsAreOneWidth(t *testing.T) {
	for _, width := range []int{40, 46, 56, 62, 72, 96, 200} {
		out := Live(sampleView(width))
		var want int
		for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if strings.HasPrefix(line, "*") {
				continue // the footnote is outside the rule
			}
			n := utf8.RuneCountInString(line)
			if i == 0 {
				want = n
				continue
			}
			if n != want {
				t.Errorf("width %d: line %d is %d columns, want %d:\n%s", width, i, n, want, out)
			}
		}
	}
}

func TestLiveOmitsProvenanceColumn(t *testing.T) {
	v := sampleView(72)
	if strings.Contains(Live(v), "class") {
		t.Fatal("live view still has a class heading")
	}
	for _, line := range boxLines(t, Live(v)) {
		trimmed := strings.TrimRight(strings.TrimSuffix(line, "│"), " ")
		if strings.HasSuffix(trimmed, " D") || strings.HasSuffix(trimmed, " C") {
			t.Errorf("live view still has a provenance marker:\n%s", line)
		}
	}
}

// A terminal wider than the content is a ceiling, not a target.
func TestLiveDoesNotStretchToFillWideTerminal(t *testing.T) {
	wide := utf8.RuneCountInString(strings.Split(Live(sampleView(200)), "\n")[0])
	if also := utf8.RuneCountInString(strings.Split(Live(sampleView(96)), "\n")[0]); wide != also {
		t.Errorf("view at width 200 is %d columns and at 96 is %d; both exceed the natural width, so both should be it", wide, also)
	}
	if wide > 96 {
		t.Errorf("view grew to %d columns, past the maximum", wide)
	}
}

func TestLiveCompactLabelsRetainSharedTable(t *testing.T) {
	v := sampleView(200)
	v.Snapshot.Capabilities = []string{string(classify.CapVerifiedRepair)}
	v.Status.Semantic = &classify.Operational{Eligible: 1, Dropped: 1}
	out := Live(v)
	width := panel.VisibleLen(strings.Split(out, "\n")[0])
	if width > 80 {
		t.Fatalf("full view needs %d columns, want at most 80:\n%s", width, out)
	}
	for _, want := range []string{"unresolved", "repeated", "resolved", "unknown", "expansions", "0/1 validated", "1 dropped"} {
		if !strings.Contains(out, want) {
			t.Errorf("compact full view lost %q:\n%s", want, out)
		}
	}
	t.Log("\n" + out)
	if lines := len(strings.Split(strings.TrimSuffix(out, "\n"), "\n")); lines != 18 {
		t.Errorf("full view has %d lines, want the reference layout plus one MODEL row (18)", lines)
	}
	column := -1
	for _, pair := range [][2]string{{"Transmission", "baseline"}, {"Dereference", "resolved"}, {"Recovery", "chars"}, {"Pollution", "expansions"}} {
		for _, line := range strings.Split(out, "\n") {
			if !strings.Contains(line, pair[0]) {
				continue
			}
			at := panel.VisibleLen(line[:strings.Index(line, pair[1])])
			if column >= 0 && at != column {
				t.Errorf("%s detail starts at %d, want shared column %d", pair[0], at, column)
			}
			column = at
		}
	}
}

// A narrow terminal keeps the primary metric and drops whole detail columns.
func TestLiveNarrowKeepsPrimaryMetric(t *testing.T) {
	out := Live(sampleView(46))
	// "repeated" belongs to the third column only. "resolved" would also match
	// the first column's "unresolved".
	if strings.Contains(out, "repeated") {
		t.Errorf("third column survived at width 46:\n%s", out)
	}
	found := false
	for _, line := range boxLines(t, out) {
		if !strings.Contains(line, "Dereference") {
			continue
		}
		found = true
		if !strings.Contains(line, "DRP") {
			t.Errorf("DRP lost on a narrow row:\n%s", line)
		}
	}
	if !found {
		t.Fatal("no Dereference row found")
	}
}

// Family labels are title case and flush right against their values.
func TestLiveLabelsAreTitleCaseAndRightAligned(t *testing.T) {
	lines := boxLines(t, Live(sampleView(72)))
	if strings.Contains(strings.Join(lines, "\n"), "TRANSMISSION") {
		t.Error("labels are still upper case")
	}

	// Every family label must end at the same column.
	want := -1
	for _, name := range []string{"Transmission", "Control", "Obligations", "Dereference", "Recovery", "Pollution"} {
		var line string
		for _, l := range lines {
			if strings.Contains(l, name) {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("no row for %s", name)
		}
		end := utf8.RuneCountInString(line[:strings.Index(line, name)]) + utf8.RuneCountInString(name)
		if want == -1 {
			want = end
			continue
		}
		if end != want {
			t.Errorf("%s ends at column %d, want %d (labels are not right-aligned)", name, end, want)
		}
	}
}

// The shared metric columns retain their headings.
func TestLiveHasColumnHeadings(t *testing.T) {
	out := Live(sampleView(96))
	for _, want := range []string{"value", "detail"} {
		if !strings.Contains(out, want) {
			t.Errorf("heading %q missing:\n%s", want, out)
		}
	}
}

// The footnote is gone, and with it the asterisk that referred to it.
func TestLiveHasNoFootnote(t *testing.T) {
	out := Live(sampleView(72))
	if strings.Contains(out, "proxy:") {
		t.Errorf("footnote still rendered:\n%s", out)
	}
	if strings.Contains(out, "D*") {
		t.Errorf("asterisk survived its footnote:\n%s", out)
	}
}

// An unmeasured value is a glyph, never a word and never a zero.
func TestLiveUnknownIsAGlyph(t *testing.T) {
	v := sampleView(72)
	v.Snapshot.SerializationInfl = metrics.Value{}
	v.Snapshot.BaselineChars = metrics.Value{}
	v.Snapshot.Dereference = metrics.Value{}
	v.Snapshot.PollutionStatus = metrics.PollutionUnknown

	out := Live(v)
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Transmission") && strings.Contains(line, "unknown") {
			t.Errorf("unknown numeric value still spelled out:\n%s", line)
		}
	}
	if !strings.Contains(out, unknownGlyph) {
		t.Errorf("no unknown glyph drawn for unmeasured values:\n%s", out)
	}

	// A known span replaces its glyph and leaves the table's four alone.
	v.Status.Elapsed = 3*time.Hour + 12*time.Minute
	withSpan := Live(v)
	if !strings.Contains(withSpan, "3h12m") {
		t.Errorf("elapsed span not rendered:\n%s", withSpan)
	}
	if strings.Count(withSpan, unknownGlyph) >= strings.Count(out, unknownGlyph) {
		t.Errorf("a known span did not replace its glyph:\n%s", withSpan)
	}
}

// The session span is the stream's own clock, and it is a hero primitive
// alongside the phase and the turn.
func TestLiveElapsedIsAHeroLine(t *testing.T) {
	v := sampleView(72)
	v.Status.Elapsed = 53*time.Minute + 40*time.Second
	lines := boxLines(t, Live(v))

	var elapsed, turn string
	for _, l := range lines {
		if strings.Contains(l, "ELAPSED") {
			elapsed = l
		}
		if strings.Contains(l, "TURN") {
			turn = l
		}
	}
	if elapsed == "" {
		t.Fatalf("no elapsed line:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(elapsed, "53m40s") {
		t.Errorf("span not rendered: %q", elapsed)
	}
	// The three hero labels are right-aligned together.
	if strings.Index(elapsed, "53m40s") != strings.Index(turn, "14") {
		t.Errorf("hero values do not start at one column:\n%q\n%q", elapsed, turn)
	}
}

func TestLiveShowsSemanticOperationalStatus(t *testing.T) {
	v := sampleView(140)
	v.Status.Semantic = &classify.Operational{Eligible: 8, Requested: 8, Completed: 3, Pending: 3, CatchUp: 2, Failed: 1, TimedOut: 1, Applied: 3, Changed: 1, LastError: "endpoint returned status 500", P95MS: 184}
	v.Status.ModelDetails = true
	out := Live(v)
	for _, want := range []string{"MODEL", "3/8 validated", "1 changed", "MODEL JOBS", "3 answers", "1 active", "2 catching up", "1 errors", "1 timed out", "MODEL TIME", "95% completed within 184ms", "MODEL WHY", "status 500"} {
		if !strings.Contains(out, want) {
			t.Errorf("semantic status misses %q:\n%s", want, out)
		}
	}
}

func TestLiveHidesModelDetailsByDefault(t *testing.T) {
	v := sampleView(96)
	v.Status.Semantic = &classify.Operational{Eligible: 33, Completed: 7, Applied: 7, Changed: 6, Failed: 2, CatchUp: 24}
	out := Live(v)
	if !strings.Contains(out, "7/33 validated") || strings.Contains(out, "MODEL JOBS") || !strings.Contains(out, "2 failed") {
		t.Fatalf("default model row is not compact:\n%s", out)
	}
}

// A durable band crossing rides the elapsed line, coloured by direction and
// carrying the bands as words.
func TestLiveTrendNote(t *testing.T) {
	v := sampleView(72)
	if strings.Contains(Live(v), "→ mid") {
		t.Error("a trend was drawn with none reported")
	}

	v.Trend = TrendNote{Metric: "dereference", From: "good", To: "mid", Degrading: true}
	out := Live(v)
	if !strings.Contains(out, "DRP good → mid") {
		t.Errorf("trend note missing or not in the table's vocabulary:\n%s", out)
	}

	// It has its own labelled row, separate from the rolling event line.
	for _, l := range boxLines(t, out) {
		if strings.Contains(l, "DRP good → mid") && !strings.Contains(l, "TREND") {
			t.Errorf("trend note is not on the TREND row: %q", l)
		}
	}

	// Direction is carried in colour, not in the text.
	v.Color = true
	degrading := Live(v)
	v.Trend.Degrading = false
	if improving := Live(v); improving == degrading {
		t.Error("improving and degrading trends render identically")
	}
}

// Every figure in a column must end at the same offset, whatever its width.
func TestLiveFigureColumnEndsAtOneOffset(t *testing.T) {
	lines := boxLines(t, Live(sampleView(72)))
	want := -1
	for _, probe := range []struct{ row, figure string }{
		{"Transmission", "0.7"},
		{"Control", "7"},
		{"Obligations", "2"},
		{"Recovery", "0"},
	} {
		var line string
		for _, l := range lines {
			if strings.Contains(l, probe.row) {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("no %s row", probe.row)
		}
		// The first figure of the row ends where its column ends.
		at := strings.Index(line, probe.figure) + len(probe.figure)
		if want == -1 {
			want = at
			continue
		}
		if at != want {
			t.Errorf("%s figure ends at column %d, want %d:\n%s", probe.row, at, want, line)
		}
	}
}

// Column headings are centred over their columns.
func TestLiveHeadingsAreCentred(t *testing.T) {
	lines := boxLines(t, Live(sampleView(72)))
	var head, first string
	for i, l := range lines {
		if strings.Contains(l, "value") && strings.Contains(l, "detail") {
			head, first = l, lines[i+1]
			break
		}
	}
	if head == "" {
		t.Fatalf("no heading row:\n%s", strings.Join(lines, "\n"))
	}
	// "value" must start after the column does, which is what centring means.
	col := strings.Index(first, "0.7")
	if at := strings.Index(head, "value"); at <= col {
		t.Errorf("heading starts at %d, column at %d: not centred\n%q\n%q", at, col, head, first)
	}
}

// Marks are attributes only: with colour off the frame is byte-identical, so
// nothing is carried by colour alone.
func TestLiveMovesAreColourOnly(t *testing.T) {
	plain := sampleView(72)
	marked := sampleView(72)
	marked.Moves = map[string]Move{"si": MoveWorse, "forward": MoveBetter, "now": MoveNeutral}
	if Live(plain) != Live(marked) {
		t.Error("movement marks changed the text of the frame with colour off")
	}

	plain.Color, marked.Color = true, true
	if Live(plain) == Live(marked) {
		t.Error("movement marks had no effect with colour on")
	}
	for i, line := range strings.Split(Live(marked), "\n") {
		if got, want := panel.VisibleLen(line), panel.VisibleLen(strings.Split(Live(plain), "\n")[i]); got != want {
			t.Errorf("marked line %d is %d visible columns, want %d", i, got, want)
		}
	}
}

// The spinner runs while records are landing and rests as an open circle once
// they stop. The quiet-for counter carries liveness from there.
func TestStatusGlyphRestsWhenIdle(t *testing.T) {
	active := statusRecords(Status{Seen: true, Records: 3, Since: time.Second}, false)
	if strings.HasPrefix(active, idleGlyph) {
		t.Errorf("idle glyph used while records were landing: %q", active)
	}
	for _, pulse := range []int{0, 3, 7} {
		idle := statusRecords(Status{Seen: true, Records: 3, Since: 30 * time.Second, Pulse: pulse}, false)
		if !strings.HasPrefix(idle, idleGlyph) {
			t.Errorf("pulse %d: idle status does not rest on the open circle: %q", pulse, idle)
		}
	}
}

// The spinner advances every draw while the stream is moving.
func TestStatusSpinnerAdvances(t *testing.T) {
	seen := map[string]bool{}
	for pulse := range len(spinnerFrames) {
		seen[statusRecords(Status{Seen: true, Records: 1, Since: time.Second, Pulse: pulse}, false)] = true
	}
	if len(seen) != len(spinnerFrames) {
		t.Errorf("got %d distinct frames over %d pulses, want %d", len(seen), len(spinnerFrames), len(spinnerFrames))
	}
	// A stopped view has no spinner to advance.
	if a, b := statusRecords(Status{Stopped: true, Pulse: 1}, false), statusRecords(Status{Stopped: true, Pulse: 2}, false); a != b {
		t.Errorf("stopped status animates: %q vs %q", a, b)
	}
}

// A coloured view still lines up. The measurement itself is panel's; this is
// that every attribute the view adds is one panel can see past.
func TestColouredViewIsOneWidth(t *testing.T) {
	v := sampleView(72)
	v.Color = true
	v.Regime = "THRASH"
	out := boxLines(t, Live(v))
	for _, line := range out {
		if got := panel.VisibleLen(line); got != panel.VisibleLen(out[0]) {
			t.Errorf("coloured line is %d visible columns, want %d:\n%q",
				got, panel.VisibleLen(out[0]), line)
		}
	}
}

// A pane thirty columns wide cannot spend all of them on a generated key. The
// identifier is shortened to the handle the listing prints, so the pane and
// `flow-indicator sessions` call one session by one name.
func TestSessionLabelShortensGeneratedKeys(t *testing.T) {
	for _, tc := range []struct {
		name, id, want string
	}{
		{"claude uuid", "c0d33334-1d9b-4d14-81be-8c7a64fbdc15", "c0d33334"},
		{"codex uuid", "019ffd5a-f531-7fb3-bcc8-c4ec95b8fd8f", "019ffd5a"},
		{"opencode key", "ses_002d13d18ffeO1gjzgk2TEOWQ1", "ses_002d13d\u2026"},
		{"a name someone chose", "recent-thrash", "recent-thrash"},
		{"short enough to read", "flow-indicator-demo", "flow-indicator-demo"},
		{"leading segment too short to identify", "rollout-2026-08-13T16-00-24-019ffd5a", "rollout-202\u2026"},
	} {
		if got := sessionLabel(tc.id); got != tc.want {
			t.Errorf("%s: sessionLabel(%q) = %q, want %q", tc.name, tc.id, got, tc.want)
		}
	}
}

// Whatever the pane shows has to fit the pane. A thirty-column side pane is the
// narrowest this draws into.
func TestSessionLabelFitsTheNarrowestPane(t *testing.T) {
	const narrowest = 30
	for _, id := range []string{
		"c0d33334-1d9b-4d14-81be-8c7a64fbdc15",
		"ses_002d13d18ffeO1gjzgk2TEOWQ1",
		"rollout-2026-08-13T16-00-24-019ffd5a-f531-7fb3-bcc8-c4ec95b8fd8f",
	} {
		// Columns, not bytes: the ellipsis Fit appends is three bytes wide and
		// one column wide.
		if got := panel.VisibleLen(sessionLabel(id)); got > narrowest {
			t.Errorf("sessionLabel(%q) is %d columns, wider than a %d-column pane", id, got, narrowest)
		}
	}
}
