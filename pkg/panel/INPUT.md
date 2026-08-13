# Output and input in `panel`

`panel` writes to a terminal and never reads from one. This document states
what that buys, what it forecloses, and what adding input would cost. Written
2026-08-13, when the package was extracted from `internal/render`.

## The decision

Output-only was chosen, not inherited. `flow-indicator` displays a measurement
that changes on its own; there is nothing for an operator to drive. The second
program the package was extracted for was scoped the same way. Every simplifying
property below follows from that one choice, so read this before assuming a key
can be added cheaply.

## What output-only means mechanically

The whole terminal surface is `Screen.Paint`: a string of text and escape
sequences written to an `io.Writer`.

| sequence | in `screen.go` | job |
|---|---|---|
| `\033[?25l` / `\033[?25h` | 32, 48 | hide the cursor for the view's life, give it back on `Close` |
| `\033[H` | 35 | home the cursor before each frame |
| `\033[K` | 38 | erase each line as it is written, instead of clearing first |
| `\033[J` | 40 | erase whatever the previous frame left below |

Erasing per line rather than clearing first is deliberate: a clear blanks the
terminal between the two writes, which at a one-second heartbeat is a visible
flicker on every frame.

The terminal stays in **canonical (cooked) mode** throughout. The kernel's line
discipline keeps line buffering and echo on, exactly as at a shell prompt.
Consequences, all of them by design:

- No key does anything. There is no quit key, no scroll, no pause, no way to
  switch views at runtime — view choice is a startup flag.
- Typing while the view runs echoes the characters into the frame. The program
  never sees them; the next repaint overwrites them.
- The process needs no cleanup beyond `Screen.Close`, because it changed no
  terminal state except the cursor's visibility.

## Two things that look like input and are not

Both mislead on a first read of the code. Neither involves reading a key.

**Ctrl-C.** The terminal driver, not the program, sees `0x03` and raises
SIGINT. `cmd/flow-indicator/main.go:324` catches it with
`signal.NotifyContext`. Remove the driver's translation — which is what raw
mode does — and Ctrl-C becomes an ordinary byte that nothing is reading, so the
program stops being interruptible. Anyone adding raw mode owns that regression.

**Window resize.** There is no SIGWINCH handler. `TerminalWidth`
(`width_unix.go:27`) reissues the `TIOCGWINSZ` ioctl, and the caller asks on
every frame. Because the view repaints on a one-second heartbeat
(`main.go:441`, `main.go:502`), a resize corrects itself on the next tick.
Polling substitutes for the event, and only works because the heartbeat exists.

## The heartbeat carries liveness

With no input there is no user action to redraw against, so the timer is the
only thing separating a stalled observer from a quiet source. The loop shape is
documented in prose rather than exported as a `Loop` type: each branch of the
select exits differently, and behind an interface it was longer and less clear
than inline.

Adding input adds a third case to that select. It does not change the loop's
shape.

## What adding input would cost

Four pieces, none of which exist. Stdlib-only throughout; figures are for a
minimal, correct implementation.

**1. Raw mode.** Clear `ICANON` and `ECHO` so bytes arrive per keystroke and are
not echoed into the frame. Usually also `ISIG` and `IXON`, which is what forfeits
Ctrl-C and Ctrl-S. Set `VMIN`/`VTIME` for the read policy.

`syscall.Termios` exists on Linux and every BSD, but the ioctl constants do not
agree. Verified 2026-08-13 by cross-compiling against each target:

| target | get / set attributes |
|---|---|
| linux | `syscall.TCGETS` / `syscall.TCSETS` |
| darwin, freebsd, openbsd | `syscall.TIOCGETA` / `syscall.TIOCSETA` |

So raw mode needs a build-tagged constant pair — the same split
`width_unix.go` / `width_other.go` already uses. Windows needs the console API
and shares no code.

**2. Restoring it.** The original `Termios` must go back on every exit path:
normal return, signal, and panic. A missed path leaves the user's shell with
echo off, which reads as a broken terminal. This is the failure mode that costs
the most trust, and `defer` alone does not cover a signal that terminates the
process. `Screen` is the natural owner, since it already hides and restores the
cursor.

**3. A key decoder.** Arrows and function keys arrive as multi-byte escape
sequences (`\033[A` and relatives). A decoder must reassemble them across read
boundaries and disambiguate a bare Escape from the start of a sequence, which
conventionally means a short timeout. This is the piece most often
underestimated.

**4. A key channel** feeding the select loop as a third case.

Estimate: 150-250 lines, unix-only. SIGWINCH becomes worth handling at that
point, since a raw-mode program that stops polling has nothing else to notice a
resize.

## The constraint to settle first: stdin may already be taken

`flow-indicator watch` reads a piped transcript with `io.ReadAll(os.Stdin)`
(`main.go:722`). In that mode stdin is the data source, and there is no
keyboard on it.

A program that both accepts piped data and reads keys must open `/dev/tty`
directly for the keyboard, which gets the controlling terminal regardless of how
stdin was redirected. Decide this before writing the input layer: it determines
whether the key reader takes an `*os.File` or opens its own, and retrofitting it
touches every call site.

## What input would not change

The layout half of the package is independent of all of this. `Box`, `Table`,
`Pane`, `Cell`, `Row` and the text functions in `text.go` are pure functions of
their arguments. They would be untouched.

Tracking would be untouched. `Tracker` and `Decay` key off wall-clock time and
observed values, not events.

The changes land in `Screen` (owning termios and its restore), a new key reader
and decoder, and the caller's loop. That is the seam to design against: keep
input out of the layout types, the way `Table.Style` and `Directions` already
keep domain meaning out of them.
