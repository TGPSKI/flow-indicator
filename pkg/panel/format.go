package panel

import (
	"fmt"
	"time"
)

// ShortSpan renders a duration as the largest two units that fit. A
// non-positive span renders empty, so a caller with nothing to report gets
// nothing rather than a zero.
func ShortSpan(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return ShortSeconds(d.Seconds())
}

// ShortSeconds is ShortSpan for a measurement already held as seconds. Zero
// renders as "0s": the caller measured it, and a measured zero is not absence.
func ShortSeconds(sec float64) string {
	s := int(sec)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	default:
		return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
	}
}

// Compact renders a count inside five columns, so a figure that runs to six
// digits does not widen the table it sits in.
func Compact(n int) string {
	switch a := max(n, -n); {
	case a < 100000:
		return fmt.Sprintf("%d", n)
	case a < 1000000:
		return fmt.Sprintf("%dk", n/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
}

// Plural renders a count with its noun, so a line reads as English rather than
// as a template. It pluralises by suffix and is wrong for nouns that do not.
func Plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
