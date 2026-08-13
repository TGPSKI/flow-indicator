// Package store writes and reads the append-only session files.
//
// Event files are opened for append and never rewritten. Projections
// (summary.json, metrics.json, report.md, timeline.csv) are disposable and may
// be replaced, because they can be rebuilt from the event files.
package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/TGPSKI/flow-indicator/internal/event"
)

// Writer appends events to one file.
type Writer struct {
	path string
	f    *os.File
	w    *bufio.Writer
}

// OpenWriter opens path for append, creating it if needed.
func OpenWriter(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: open %s for append: %w", path, err)
	}
	return &Writer{path: path, f: f, w: bufio.NewWriter(f)}, nil
}

// Append writes one event as a single JSON line.
func (w *Writer) Append(e event.Event) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("store: marshal event %s: %w", e.ID, err)
	}
	if _, err := w.w.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("store: append to %s: %w", w.path, err)
	}
	return nil
}

// Flush pushes buffered lines to the file. A followed session flushes after
// every chunk: a live log that lags behind the stream is not a record of it.
func (w *Writer) Flush() error {
	if err := w.w.Flush(); err != nil {
		return fmt.Errorf("store: flush %s: %w", w.path, err)
	}
	return nil
}

// Close flushes and closes the file.
func (w *Writer) Close() error {
	if err := w.w.Flush(); err != nil {
		_ = w.f.Close()
		return fmt.Errorf("store: flush %s: %w", w.path, err)
	}
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("store: close %s: %w", w.path, err)
	}
	return nil
}

// ReadEvents reads every event from a JSONL file. A missing file reads as no
// events: a stream that produced none is not an error.
func ReadEvents(path string) ([]event.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	defer f.Close()

	var out []event.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e event.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("store: decode %s line %d: %w", path, line, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("store: read %s: %w", path, err)
	}
	return out, nil
}

// WriteJSON writes a projection file with a trailing newline.
func WriteJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("store: write %s: %w", path, err)
	}
	return nil
}
