package panel

import (
	"fmt"
	"strings"
)

// Chrome is the columns a box spends on itself: the two rules and the single
// space inside each. A caller sizing content works in inner columns; a caller
// sizing against a terminal works in total columns, and this is the difference.
const Chrome = 4

// Box is the rule around a one-screen view: a top line carrying an inline
// title, content lines padded to one width, and a closing line.
//
// Min and Max bound the total width. Below the minimum the rows stop being
// readable; above the maximum a right-flushed column ends up an eye-movement
// away from the value it marks.
type Box struct {
	Title    string
	Min, Max int
}

// Inner resolves the content width in columns.
//
// The terminal is a ceiling, not a target: the box takes what it needs and
// stops, so a wide window does not spread six short rows across it. A
// requested width of zero imposes no ceiling but Max, and the natural width of
// the content decides. The result never falls below Min less the chrome.
func (b Box) Inner(requested, natural int) int {
	if requested <= 0 {
		requested = b.Max
	}
	requested = min(max(requested, b.Min), b.Max)
	return max(min(requested-Chrome, natural), b.Min-Chrome)
}

// Render draws the box around lines at the given inner width. A line longer
// than the width is cut; a shorter one is padded, so every row ends at the
// same column.
func (b Box) Render(inner int, lines []string) string {
	var out strings.Builder

	// The rule runs to inner+3 before the corner, so the head matches the
	// content lines, which are inner columns between two single spaces.
	head := "┌"
	if b.Title != "" {
		head += "─ " + b.Title + " "
	}
	out.WriteString(head)
	out.WriteString(strings.Repeat("─", max(inner+3-VisibleLen(head), 0)))
	out.WriteString("┐\n")

	for _, line := range lines {
		fmt.Fprintf(&out, "│ %s │\n", Pad(Fit(line, inner), inner))
	}

	out.WriteString("└")
	out.WriteString(strings.Repeat("─", inner+2))
	out.WriteString("┘\n")
	return out.String()
}

// Pane is the unbordered narrow form: labelled rows at a shared label width,
// for a side pane where a rule would cost columns the content needs.
type Pane struct {
	// LabelW is the width the labels share, so every value starts at one
	// column. The label rests back on the left; the value carries the weight.
	LabelW int
	// Width is the total inner width. Values are cut to what is left after the
	// label and its two-space gutter.
	Width int
	Color bool
}

// ValueW is the columns a value has, after the label and its gutter.
func (p Pane) ValueW() int { return max(p.Width-p.LabelW-2, 1) }

// Row renders one labelled line, with the value carrying ansi. An empty label
// leaves the label column blank, which is how a value continues onto a second
// line under the first.
func (p Pane) Row(label, value, ansi string) string {
	return Paint(PadLeft(label, p.LabelW)+"  ", Dim, p.Color) +
		Paint(Fit(value, p.ValueW()), ansi, p.Color)
}
