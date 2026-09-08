package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// TailFromEnd starts a tail at the current end of the file.
const TailFromEnd int64 = -1

// pollInterval is how often a tail looks for growth. There is no file-watch
// dependency; polling a local file is enough for a session log.
const pollInterval = 200 * time.Millisecond

// Tail follows an append-only file and yields chunks of whole records.
//
// It never writes to the source, never re-reads a committed offset, and holds
// a partial final record until its newline arrives.
type Tail struct {
	path        string
	f           *os.File
	offset      int64
	partial     []byte
	initialSize int64
}

// OpenTail opens path for reading, starting at offset. Pass TailFromEnd to
// begin at the current end of the file.
func OpenTail(path string, offset int64) (*Tail, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("tail: open %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("tail: stat %s: %w", path, err)
	}
	if offset == TailFromEnd || offset > info.Size() {
		offset = info.Size()
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("tail: seek %s to %d: %w", path, offset, err)
	}
	return &Tail{path: path, f: f, offset: offset, initialSize: info.Size()}, nil
}

func (t *Tail) InitialSize() int64 { return t.initialSize }

// Offset is the byte offset of the first byte not yet returned as a whole
// record.
//
// It is where a later watch can start reading. It is not a resume point: a
// watch started at offset N builds its state from byte N forward and knows
// nothing of the obligations, episodes or baselines that the earlier bytes
// established.
func (t *Tail) Offset() int64 { return t.offset }

// ErrTruncated reports that the source shrank below the committed offset.
var ErrTruncated = errors.New("source file was truncated below the committed offset")

// Next blocks until at least one whole record is available, then returns those
// bytes and the absolute offset of their first byte. It returns ctx.Err() when
// the context ends.
func (t *Tail) Next(ctx context.Context) ([]byte, int64, error) {
	buf := make([]byte, 64*1024)
	for {
		info, err := t.f.Stat()
		if err != nil {
			return nil, 0, fmt.Errorf("tail: stat %s: %w", t.path, err)
		}
		if info.Size() < t.offset+int64(len(t.partial)) {
			return nil, 0, fmt.Errorf("tail: %s: %w", t.path, ErrTruncated)
		}

		n, err := t.f.Read(buf)
		if n > 0 {
			start := t.offset
			t.partial = append(t.partial, buf[:n]...)
			if cut := lastNewline(t.partial); cut >= 0 {
				whole := t.partial[:cut+1]
				t.partial = append([]byte(nil), t.partial[cut+1:]...)
				t.offset = start + int64(len(whole))
				return whole, start, nil
			}
			continue
		}
		if err != nil && err != io.EOF {
			return nil, 0, fmt.Errorf("tail: read %s: %w", t.path, err)
		}

		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// Close releases the file.
func (t *Tail) Close() error {
	if err := t.f.Close(); err != nil {
		return fmt.Errorf("tail: close %s: %w", t.path, err)
	}
	return nil
}

func lastNewline(b []byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == '\n' {
			return i
		}
	}
	return -1
}
