package panel

import (
	"fmt"
	"io"
	"strings"
)

// Screen draws a frame in place. ANSI redraw is enough: there is no terminal
// framework here and none is wanted.
type Screen struct {
	w      io.Writer
	hidden bool
}

// NewScreen returns a Screen writing to w.
func NewScreen(w io.Writer) *Screen { return &Screen{w: w} }

// Paint writes a frame over the previous one.
//
// It homes the cursor and erases each line as it writes it, rather than
// clearing the screen first. A clear blanks the terminal for the interval
// between the two writes, which at a one-second heartbeat is a visible flicker
// on every frame.
//
// The cursor is hidden for the duration of the view. It would otherwise rest
// on the line after the frame, where it reads as a stray block under the box
// rather than as anything the operator can type at. Close gives it back.
func (s *Screen) Paint(frame string) {
	var b strings.Builder
	if !s.hidden {
		b.WriteString("\033[?25l")
		s.hidden = true
	}
	b.WriteString("\033[H")
	for line := range strings.SplitSeq(strings.TrimRight(frame, "\n"), "\n") {
		b.WriteString(line)
		b.WriteString("\033[K\n")
	}
	b.WriteString("\033[J")
	fmt.Fprint(s.w, b.String())
}

// Close returns the terminal to the caller: the cursor comes back on. It is
// safe to call without a preceding Paint.
func (s *Screen) Close() {
	if s.hidden {
		fmt.Fprint(s.w, "\033[?25h")
		s.hidden = false
	}
}
