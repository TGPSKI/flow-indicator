//go:build !unix

package panel

import "os"

// TerminalWidth reports no terminal on platforms without TIOCGWINSZ. Callers
// fall back to their default width.
func TerminalWidth(*os.File) int { return 0 }
