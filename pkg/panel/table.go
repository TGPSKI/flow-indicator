package panel

import (
	"strings"
	"unicode/utf8"
)

// DefaultMinFigure is the floor for a figure subcolumn. Without it the table
// would be narrower on a turn whose numbers happen to be small and wider on the
// next one, and the whole view would resize while the operator was reading it.
const DefaultMinFigure = 5

// Cell is one measured value and the word naming it.
//
// The figure, its unit and the word are held apart so each can be placed in its
// own subcolumn: figures flush right against the unit, words flush left after
// it. A single pre-formatted string cannot be aligned that way.
//
// ID names the cell for Style. It is not drawn.
type Cell struct {
	ID      string
	Numeric string
	Unit    string
	Detail  string
}

// Row is one line of the table: a label, its cells, and a marker flush to the
// right edge. Rows may carry different numbers of cells; the layout pads them
// into shared columns.
type Row struct {
	Label string
	Cells []Cell
	Tail  string
}

// Table lays a set of rows out at one set of column widths, measured across
// every row so the figures line up down the view.
type Table struct {
	Rows []Row
	// Heads name the cell columns, left to right. A column past the end of
	// this slice is drawn without a heading.
	Heads []string
	// TailHead is the heading over the right-flushed marker column. That
	// column's letters are the least self-explaining thing on screen, so it is
	// headed rather than footnoted.
	TailHead string
	// MinFigure is the floor for a figure subcolumn. Zero uses
	// DefaultMinFigure.
	MinFigure int
}

// Style returns the ANSI attribute a cell carries, by the cell's ID, or the
// empty string for none. A nil Style draws every cell unattributed.
//
// This is the seam between the layout and whatever the values mean: the table
// knows a cell moved only because the caller says so.
type Style func(cellID string) string

// Options are the per-draw choices the table itself does not own.
type Options struct {
	Color bool
	Style Style
}

func (o Options) style(id string) string {
	if o.Style == nil {
		return ""
	}
	return o.Style(id)
}

// Natural is the width the table wants: enough for every column and no more.
//
// Pass it to Box.Inner. Letting anything that changes as the view runs — a
// name, a counter, a note — widen the box would resize the view mid-session;
// those lines fit into the width the table asks for, and truncate if they
// cannot.
func (t Table) Natural() int {
	labelW, cellW, tailW := t.measure(t.columns())
	return width(labelW, cellW, tailW)
}

// Draw renders the table into the given width, one line per row plus a heading
// line above them.
//
// Columns are dropped from the right when the table cannot fit: the
// alternative is per-row truncation, which cuts the marker off the longest rows
// and leaves the table looking ragged rather than narrow.
func (t Table) Draw(w int, o Options) []string {
	for keep := t.columns(); keep >= 1; keep-- {
		labelW, cellW, tailW := t.measure(keep)
		if width(labelW, cellW, tailW) > w && keep > 1 {
			continue
		}
		return t.draw(labelW, cellW, w, o)
	}
	return nil
}

// colWidth is one cell column. The three parts are measured separately so the
// figures line up independently of the units and words beside them.
type colWidth struct{ numeric, unit, detail int }

func (c colWidth) total() int {
	n := c.numeric + c.unit
	if c.detail > 0 {
		n += 1 + c.detail
	}
	return n
}

// columns is the number of cell columns any row carries.
func (t Table) columns() int {
	n := 0
	for _, r := range t.Rows {
		n = max(n, len(r.Cells))
	}
	return n
}

// headFor is the heading over cell column i, if it has one.
func (t Table) headFor(i int) string {
	if i < len(t.Heads) {
		return t.Heads[i]
	}
	return ""
}

// measure returns the label width, the width of each of the first keep cell
// columns, and the tail width, taken across every row so the columns are
// shared.
func (t Table) measure(keep int) (labelW int, cellW []colWidth, tailW int) {
	for _, r := range t.Rows {
		labelW = max(labelW, utf8.RuneCountInString(r.Label))
		tailW = max(tailW, utf8.RuneCountInString(r.Tail))
	}
	tailW = max(tailW, utf8.RuneCountInString(t.TailHead))

	cellW = make([]colWidth, keep)
	for _, r := range t.Rows {
		for i := 0; i < keep && i < len(r.Cells); i++ {
			c := r.Cells[i]
			cellW[i].numeric = max(cellW[i].numeric, utf8.RuneCountInString(c.Numeric))
			cellW[i].unit = max(cellW[i].unit, utf8.RuneCountInString(c.Unit))
			cellW[i].detail = max(cellW[i].detail, utf8.RuneCountInString(c.Detail))
		}
	}

	floor := t.MinFigure
	if floor <= 0 {
		floor = DefaultMinFigure
	}
	for i := range cellW {
		// Every figure subcolumn holds its floor, so the table is the same
		// width whatever this turn's numbers happen to be.
		cellW[i].numeric = max(cellW[i].numeric, floor)
		// The unit slot is held open even when nothing in the column carries a
		// unit, so a column of unknowns is the same width as a column of
		// percentages.
		cellW[i].unit = max(cellW[i].unit, 1)
		// A column is at least as wide as its heading, which is centred over it.
		if h := t.headFor(i); h != "" {
			if grow := utf8.RuneCountInString(h) - cellW[i].total(); grow > 0 {
				cellW[i].detail += grow
			}
		}
	}
	return labelW, cellW, tailW
}

// width is the columns a set of measured columns occupies: the label, each cell
// with its gutter, and the tail column two spaces clear of the last value.
func width(labelW int, cellW []colWidth, tailW int) int {
	total := labelW + tailW + 2
	for _, w := range cellW {
		total += w.total() + 2
	}
	return total
}

// draw renders the table at the measured widths. The label is flush right
// against its values; the values themselves run left from there.
func (t Table) draw(labelW int, cellW []colWidth, w int, o Options) []string {
	var out []string

	var head strings.Builder
	head.WriteString(strings.Repeat(" ", labelW))
	for i, c := range cellW {
		head.WriteString("  ")
		head.WriteString(Center(t.headFor(i), c.total()))
	}
	out = append(out, Paint(Ends(strings.TrimRight(head.String(), " "), t.TailHead, w), Dim, o.Color))

	for _, r := range t.Rows {
		var b strings.Builder
		// The label rests back; the figures beside it are what the eye is meant
		// to land on.
		b.WriteString(Paint(PadLeft(r.Label, labelW), Dim, o.Color))
		for i, cw := range cellW {
			var c Cell
			if i < len(r.Cells) {
				c = r.Cells[i]
			}
			b.WriteString("  ")
			b.WriteString(drawCell(c, cw, o))
		}
		out = append(out, Ends(TrimRightVisible(b.String()), Paint(r.Tail, Dim, o.Color), w))
	}
	return out
}

// drawCell places one cell: the figure flush right, its unit immediately after
// it, then the word flush left.
func drawCell(c Cell, w colWidth, o Options) string {
	numeric := PadLeft(c.Numeric, w.numeric)
	unit := Pad(c.Unit, w.unit)
	if ansi := o.style(c.ID); ansi != "" && c.Numeric != "" {
		numeric = Paint(PadLeft(c.Numeric, w.numeric), ansi, o.Color)
		unit = Paint(Pad(c.Unit, w.unit), ansi, o.Color)
	}
	body := numeric + unit
	if w.detail == 0 {
		return Pad(body, w.total())
	}
	if c.Detail == "" && c.Numeric != "" {
		// A value with no word of its own still occupies the whole column, so
		// the columns after it stay where the heading says they are.
		return Pad(body, w.total())
	}
	if c.Numeric == "" {
		// A word standing alone sits in the word subcolumn, under the words.
		return Pad(strings.Repeat(" ", w.numeric+w.unit+1)+
			Paint(Pad(c.Detail, w.detail), Dim, o.Color), w.total())
	}
	return body + " " + Paint(Pad(c.Detail, w.detail), Dim, o.Color)
}
