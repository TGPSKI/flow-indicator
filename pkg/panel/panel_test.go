package panel

import (
	"strings"
	"testing"
	"time"
)

// lines returns the rendered lines of a frame.
func lines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// sample is a table with rows of differing cell counts, which is the case the
// shared-column measurement exists for.
func sample() Table {
	return Table{
		Heads:    []string{"value", "detail"},
		TailHead: "class",
		Rows: []Row{
			{Label: "Transmission", Tail: "D", Cells: []Cell{
				{ID: "si", Numeric: "3.4", Unit: "x", Detail: "SI"},
				{ID: "base", Numeric: "1200", Detail: "baseline"},
				{ID: "now", Numeric: "88", Detail: "now"},
			}},
			{Label: "Control", Tail: "D", Cells: []Cell{
				{ID: "cpb", Numeric: "41", Unit: "%", Detail: "CPB"},
			}},
			{Label: "Recovery", Tail: "C", Cells: []Cell{
				{ID: "depth", Numeric: "2", Detail: "depth"},
				{ID: "chars", Numeric: "140000", Detail: "chars"},
			}},
		},
	}
}

// Padding and truncation measure visible columns, so a coloured line is not
// several columns short of the rule it is padded to.
func TestWidthIgnoresAttributes(t *testing.T) {
	colored := "\033[33mDRIFT" + Reset
	if got := VisibleLen(colored); got != 5 {
		t.Errorf("VisibleLen(%q) = %d, want 5", colored, got)
	}
	if got := VisibleLen(Pad(colored, 10)); got != 10 {
		t.Errorf("padded visible length = %d, want 10", got)
	}
	if got := VisibleLen(PadLeft(colored, 10)); got != 10 {
		t.Errorf("left-padded visible length = %d, want 10", got)
	}
	if got := Fit(colored, 10); got != colored {
		t.Errorf("Fit shortened a line that already fits: %q", got)
	}
}

// A line cut in the middle of an attribute must close it, or the colour runs
// into the rest of the terminal.
func TestFitClosesAnAttributeItCut(t *testing.T) {
	cut := Fit("\033[33mDRIFT"+Reset, 3)
	if got := VisibleLen(cut); got != 3 {
		t.Errorf("cut line has visible length %d, want 3", got)
	}
	if !strings.HasSuffix(cut, Reset) {
		t.Errorf("truncated coloured line does not reset the attribute: %q", cut)
	}
	if plain := Fit("DRIFT", 3); strings.Contains(plain, Reset) {
		t.Errorf("uncoloured line gained a reset it never opened: %q", plain)
	}
}

// Ends holds both sides on one line of exactly the given width, and gives up
// the left before the right.
func TestEndsKeepsTheRightFlush(t *testing.T) {
	got := Ends("a long left side that will not fit", "right", 20)
	if VisibleLen(got) != 20 {
		t.Errorf("Ends produced %d columns, want 20: %q", VisibleLen(got), got)
	}
	if !strings.HasSuffix(got, "right") {
		t.Errorf("right side was trimmed before the left: %q", got)
	}
}

// Every line of a box is the same width, whatever the content did.
func TestBoxLinesAreOneWidth(t *testing.T) {
	b := Box{Title: "meter", Min: 20, Max: 60}
	out := lines(b.Render(30, []string{
		"short",
		strings.Repeat("x", 90),
		"",
		"\033[2mcoloured" + Reset,
	}))
	want := VisibleLen(out[0])
	for i, line := range out {
		if got := VisibleLen(line); got != want {
			t.Errorf("line %d is %d columns, want %d: %q", i, got, want, line)
		}
	}
}

// A box with no title still closes: the top rule matches the bottom.
func TestBoxWithoutATitle(t *testing.T) {
	out := lines(Box{Min: 20, Max: 60}.Render(24, []string{"row"}))
	if got, want := VisibleLen(out[0]), VisibleLen(out[len(out)-1]); got != want {
		t.Errorf("top rule is %d columns, bottom is %d", got, want)
	}
	if strings.Contains(out[0], " ") {
		t.Errorf("untitled top rule carries a gap: %q", out[0])
	}
}

// The terminal is a ceiling, not a target: a wide window does not spread a
// narrow table across it, and a narrow one does not shrink it below the floor.
func TestBoxInnerFollowsContentUpToTheCeiling(t *testing.T) {
	b := Box{Min: 46, Max: 96}
	for _, c := range []struct {
		name               string
		requested, natural int
		want               int
	}{
		{"no request takes the natural width", 0, 50, 50},
		{"a wide terminal is not filled", 200, 50, 50},
		{"a narrow terminal cuts the table", 60, 90, 60 - Chrome},
		{"below the floor holds the floor", 10, 90, 46 - Chrome},
		{"above the ceiling holds the ceiling", 300, 200, 96 - Chrome},
	} {
		if got := b.Inner(c.requested, c.natural); got != c.want {
			t.Errorf("%s: Inner(%d, %d) = %d, want %d", c.name, c.requested, c.natural, got, c.want)
		}
	}
}

// The table draws with no Style, no heads and no tail head, which is the
// shape a caller reaches for first.
func TestTableDrawsWithNothingDeclared(t *testing.T) {
	bare := Table{Rows: []Row{
		{Label: "one", Cells: []Cell{{ID: "a", Numeric: "1"}}},
		{Label: "two", Cells: []Cell{{ID: "b", Numeric: "2"}}},
	}}
	out := bare.Draw(bare.Natural(), Options{})
	if len(out) != len(bare.Rows)+1 {
		t.Fatalf("drew %d lines, want %d rows and a heading", len(out), len(bare.Rows))
	}
	for _, line := range out[1:] {
		if strings.Contains(line, "\033") {
			t.Errorf("a nil Style produced an attribute: %q", line)
		}
	}
}

// Columns are dropped from the right when the table cannot fit. The
// alternative is per-row truncation, which cuts the marker off the longest
// rows and leaves the table looking ragged rather than narrow.
func TestNarrowTableDropsColumnsAndKeepsTheMarker(t *testing.T) {
	tbl := sample()
	narrow := tbl.Natural() - 20
	out := tbl.Draw(narrow, Options{})
	for i, line := range out {
		if VisibleLen(line) > narrow {
			t.Errorf("line %d is %d columns, over the %d given: %q", i, VisibleLen(line), narrow, line)
		}
	}
	for i, r := range tbl.Rows {
		if !strings.HasSuffix(out[i+1], r.Tail) {
			t.Errorf("row %q lost its marker %q: %q", r.Label, r.Tail, out[i+1])
		}
	}
}

// The figure subcolumn holds a floor, so the table is the same width whatever
// this update's numbers happen to be.
func TestTableWidthDoesNotFollowTheDigits(t *testing.T) {
	small := Table{Rows: []Row{{Label: "x", Cells: []Cell{{ID: "a", Numeric: "1"}}}}}
	large := Table{Rows: []Row{{Label: "x", Cells: []Cell{{ID: "a", Numeric: "9999"}}}}}
	if small.Natural() != large.Natural() {
		t.Errorf("width moved with the digits: %d then %d", small.Natural(), large.Natural())
	}
}

// Style is the seam between the layout and what the values mean. The table
// applies it by cell ID and to nothing else.
func TestStyleReachesOnlyTheNamedCell(t *testing.T) {
	const attr = "\033[31m"
	out := sample().Draw(80, Options{Color: true, Style: func(id string) string {
		if id == "si" {
			return attr
		}
		return ""
	}})
	if !strings.Contains(out[1], attr) {
		t.Errorf("styled cell carries no attribute: %q", out[1])
	}
	if strings.Contains(out[2], attr) {
		t.Errorf("attribute leaked onto an unnamed row: %q", out[2])
	}
	// Colour off drops the attribute without changing the layout.
	plain := sample().Draw(80, Options{Style: func(string) string { return attr }})
	if strings.Contains(plain[1], attr) {
		t.Errorf("colour was off and an attribute was drawn: %q", plain[1])
	}
	if VisibleLen(plain[1]) != VisibleLen(out[1]) {
		t.Errorf("colour changed the layout: %d columns plain, %d coloured",
			VisibleLen(plain[1]), VisibleLen(out[1]))
	}
}

// A pane row is the label at rest and the value beside it, bounded by the
// pane's width.
func TestPaneRowIsBoundedByItsWidth(t *testing.T) {
	p := Pane{LabelW: 7, Width: 30}
	if got := VisibleLen(p.Row("PHASE", strings.Repeat("x", 100), "")); got != p.Width {
		t.Errorf("row is %d columns, want %d", got, p.Width)
	}
	if !strings.HasPrefix(p.Row("PHASE", "FLOW", ""), "  PHASE  ") {
		t.Errorf("label is not right-aligned in its column: %q", p.Row("PHASE", "FLOW", ""))
	}
}

// The first observation marks nothing: there is no earlier value for it to
// have moved from, and lighting everything up would say it had all changed.
func TestTrackerFirstObservationMarksNothing(t *testing.T) {
	now := time.Now()
	tr := NewTracker(time.Minute, Directions{RisingWorse: []string{"a"}})
	tr.Observe(map[string]float64{"a": 1}, now)
	if got := tr.Active(now); len(got) != 0 {
		t.Errorf("first observation marked %v", got)
	}
}

// Direction is the caller's, not arithmetic: a rise is worse for one
// identifier and better for another, and neither for one left undeclared.
func TestTrackerDirectionIsDeclaredNotDerived(t *testing.T) {
	now := time.Now()
	tr := NewTracker(time.Minute, Directions{
		RisingWorse:  []string{"cost"},
		RisingBetter: []string{"forward"},
	})
	tr.Observe(map[string]float64{"cost": 1, "forward": 1, "other": 1, "still": 1}, now)
	tr.Observe(map[string]float64{"cost": 2, "forward": 2, "other": 2, "still": 1}, now)

	want := map[string]Sense{"cost": SenseWorse, "forward": SenseBetter, "other": SenseNeutral}
	got := tr.Active(now)
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s marked %v, want %v", id, got[id], w)
		}
	}
	if _, marked := got["still"]; marked {
		t.Errorf("a value that did not move was marked")
	}
}

// A measurement that is unknown this time is left out rather than passed as
// zero, so becoming known later reads as a first value and not as a move.
func TestTrackerAbsentValueIsNotAChange(t *testing.T) {
	now := time.Now()
	tr := NewTracker(time.Minute, Directions{RisingWorse: []string{"a"}})
	tr.Observe(map[string]float64{}, now)
	tr.Observe(map[string]float64{"a": 5}, now)
	if _, marked := tr.Active(now)["a"]; marked {
		t.Errorf("becoming known was marked as a move")
	}
}

// Marks are held for a window and then let go, so the table stops claiming a
// value just changed.
func TestTrackerMarksExpire(t *testing.T) {
	now := time.Now()
	tr := NewTracker(4*time.Second, Directions{RisingWorse: []string{"a"}})
	tr.Observe(map[string]float64{"a": 1}, now)
	tr.Observe(map[string]float64{"a": 2}, now)
	if len(tr.Active(now.Add(3*time.Second))) != 1 {
		t.Errorf("mark expired inside its window")
	}
	if len(tr.Active(now.Add(5*time.Second))) != 0 {
		t.Errorf("mark outlived its window")
	}
}

// Emphasis decays with age; the words a caller passes never change.
func TestDecayGradesByAge(t *testing.T) {
	d := Decay{Fresh: 6 * time.Second, Recent: 90 * time.Second}
	const hue = "\033[31m"
	if got := d.Emphasis(time.Second, hue); got != hue {
		t.Errorf("fresh line is %q, want the hue alone", got)
	}
	if got := d.Emphasis(30*time.Second, hue); got != Dim+hue {
		t.Errorf("recent line is %q, want the hue dimmed", got)
	}
	if got := d.Emphasis(time.Hour, hue); got != Dim {
		t.Errorf("old line is %q, want dim alone", got)
	}
}
