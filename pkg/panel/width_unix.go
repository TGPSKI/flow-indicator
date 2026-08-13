//go:build unix

package panel

import (
	"os"
	"syscall"
	"unsafe"
)

// winsize is the kernel's TIOCGWINSZ payload. Only the column count is read.
type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

// TerminalWidth returns the column count of the terminal attached to f, or 0
// when f is not a terminal.
//
// Ask it on every draw rather than once at startup, so a resized window is
// followed instead of leaving the view laid out for a width that is gone.
// There is no SIGWINCH handler here; polling per frame is what replaces it.
func TerminalWidth(f *os.File) int {
	if f == nil {
		return 0
	}
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0
	}
	return int(ws.cols)
}
