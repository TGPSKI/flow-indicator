// Package panel draws a one-screen terminal instrument: a bordered box of
// labelled rows over a column-measured table, repainted in place.
//
// It is stdlib-only and output-only. Nothing here reads a key, sets raw mode,
// or handles SIGWINCH; the terminal width is asked for on every draw instead.
// INPUT.md, beside this file, states what that forecloses and what adding an
// input layer would cost. Read it before assuming a keybinding is cheap.
//
// The package holds no state and knows nothing about what is being measured. A
// caller supplies rows and gets a string; the Screen writes that string over
// the previous one.
//
// # The loop
//
// There is no Loop type. The loop a live panel needs is a select over a
// heartbeat and whatever the caller's data arrives on, and it is shorter read
// inline than configured:
//
//	beat := time.NewTicker(time.Second)
//	defer beat.Stop()
//	for {
//		select {
//		case <-ctx.Done():
//			return finish()
//		case <-beat.C:
//			pulse++
//			draw()
//		case v, ok := <-updates:
//			if !ok {
//				return finish()
//			}
//			apply(v)
//			draw()
//		}
//	}
//
// The heartbeat is what separates a stalled observer from a quiet source: with
// no timer the screen holds its last frame for as long as nothing arrives.
package panel

import "strings"

// ANSI attributes that carry structure rather than meaning. A palette is the
// caller's: this package only needs to know how to rest a label back and how
// to close an attribute it opened.
const (
	Reset = "\033[0m"
	Bold  = "\033[1m"
	Dim   = "\033[2m"
)

// Paint wraps text in an ANSI attribute when colour is on. An empty attribute
// or colour off returns the text unchanged, so a caller never has to branch.
func Paint(text, ansi string, color bool) string {
	if !color || ansi == "" {
		return text
	}
	return ansi + text + Reset
}

// VisibleLen counts the columns a string occupies, skipping ANSI escape
// sequences. Padding computed from the raw length puts every coloured line
// several columns short of the box rule.
func VisibleLen(s string) int {
	n := 0
	esc := false
	for _, r := range s {
		switch {
		case esc:
			if r == 'm' {
				esc = false
			}
		case r == '\033':
			esc = true
		default:
			n++
		}
	}
	return n
}

// Fit hard-bounds a line to a width, appending an ellipsis when it cuts.
//
// Measurement skips ANSI escape sequences, and a cut string is closed with a
// reset: a line truncated in the middle of an attribute would leave the colour
// running into the rest of the terminal.
func Fit(s string, width int) string {
	if VisibleLen(s) <= width {
		return s
	}
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	seen, esc, colored := 0, false, false
	for _, r := range s {
		switch {
		case esc:
			b.WriteRune(r)
			if r == 'm' {
				esc = false
			}
			continue
		case r == '\033':
			b.WriteRune(r)
			esc, colored = true, true
			continue
		}
		if seen == width-1 {
			break
		}
		b.WriteRune(r)
		seen++
	}
	b.WriteString("…")
	if colored {
		b.WriteString(Reset)
	}
	return b.String()
}

// Pad extends s to width columns, ignoring ANSI attributes when measuring.
func Pad(s string, width int) string {
	n := width - VisibleLen(s)
	if n <= 0 {
		return s
	}
	return s + strings.Repeat(" ", n)
}

// PadLeft extends s to width columns with the text flush right.
func PadLeft(s string, width int) string {
	n := width - VisibleLen(s)
	if n <= 0 {
		return s
	}
	return strings.Repeat(" ", n) + s
}

// Center places s in a field of the given width, biased left when the padding
// cannot be split evenly. A width of zero returns s unchanged.
func Center(s string, width int) string {
	n := width - VisibleLen(s)
	if n <= 0 {
		return s
	}
	left := n / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", n-left)
}

// Ends places left and right on one line of exactly width columns, with the
// right flush to the edge. The left is trimmed first when they collide, and
// the right is dropped only if it cannot fit at all.
func Ends(left, right string, width int) string {
	rw := VisibleLen(right)
	if rw >= width {
		return Fit(right, width)
	}
	room := width - rw - 1
	left = Fit(left, max(room, 0))
	gap := width - VisibleLen(left) - rw
	return left + strings.Repeat(" ", max(gap, 0)) + right
}

// TrimRightVisible drops trailing spaces without disturbing a trailing ANSI
// reset, which must stay attached to the text it closes.
func TrimRightVisible(s string) string {
	if body, ok := strings.CutSuffix(s, Reset); ok {
		return strings.TrimRight(body, " ") + Reset
	}
	return strings.TrimRight(s, " ")
}
