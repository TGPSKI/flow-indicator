package harness

import (
	"bufio"
	"io"
)

// maxLineBytes bounds one transcript line.
//
// Eight megabytes because a real Codex rollout carries base64 images inline and
// the longest line observed in a local history was 2.1 MB. A scanner left at its
// default 64 KB stops on such a line with an error, and a decoder that treats
// that error as end-of-file reports a short session rather than a broken one.
const maxLineBytes = 8 << 20

// newLineScanner returns a scanner sized for transcript lines.
func newLineScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	return sc
}
