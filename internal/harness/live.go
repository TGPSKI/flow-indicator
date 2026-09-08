package harness

import (
	"context"
	"fmt"

	"github.com/TGPSKI/flow-indicator/internal/adapter"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Following a session that is still being written.
//
// Replay reads a finished transcript in one call. The live meter cannot: it has
// to pick up records as they land, and the two shapes a transcript comes in do
// not agree on what "as they land" means. A file-backed harness appends whole
// JSONL lines, so the arrival unit is a byte range. opencode writes rows into
// SQLite and grows the newest message in place, so there is no byte range and
// no append to notice.
//
// A harness declares which it is by implementing Chunker or Follower. Live is
// what both produce, so the meter holds one loop over one interface.

// Live is one session being read while it is still being written.
type Live interface {
	// Next blocks until at least one new record is available, then returns the
	// records that arrived. It returns ctx.Err() when the context ends.
	Next(ctx context.Context) ([]stream.Record, error)
	// Historical identifies records already present when observation opened.
	Historical(stream.Record) bool
	// Position is where this observation reached: a byte offset for a
	// file-backed harness, a count of records handed out for a store that has
	// no offsets. It is recorded so a later run can say where it started, and
	// is not a resume point — state is rebuilt, never restored.
	Position() int64
	// Mark is Position in words, because the number alone does not say what it
	// counts. Reporting a record count as a byte offset is a false statement
	// about the source.
	Mark() string
	Close() error
}

// Chunker is a harness whose transcript is an append-only byte stream.
//
// Chunks returns a decoder for one session. The decoder is per-session and
// stateful: a format whose header carries facts the later lines do not repeat
// has to remember them across chunks.
type Chunker interface {
	Chunks(s Session) adapter.Adapter
}

// Follower is a harness that follows a session its own way, because its
// transcript is not a byte stream.
type Follower interface {
	Follow(s Session, offset int64) (Live, error)
}

// IDOf is the stream identifier a bare locator implies: the file's stem, or the
// fragment for a store that addresses sessions inside one file.
func IDOf(locator string) string { return idOf(locator) }

// sessionFor builds the session a live read addresses.
func sessionFor(locator, root, streamID string) Session {
	s := Session{ID: streamID, Locator: locator, Root: root}
	if s.ID == "" {
		s.ID = idOf(locator)
	}
	return s
}

// Follow opens a live view of one session under a named harness.
//
// offset is a byte offset for a file-backed harness and a record count for the
// others; stream.TailFromEnd means start at whatever the session already holds
// and report only what arrives after.
func Follow(name, locator, root, streamID string, offset int64) (Live, error) {
	h, err := New(name)
	if err != nil {
		return nil, err
	}
	s := sessionFor(locator, root, streamID)
	switch t := h.(type) {
	case Follower:
		return t.Follow(s, offset)
	case Chunker:
		tail, err := stream.OpenTail(locator, offset)
		if err != nil {
			return nil, err
		}
		return &fileTail{tail: tail, dec: t.Chunks(s), name: name, path: locator}, nil
	}
	return nil, fmt.Errorf("harness: %s sessions cannot be followed live; replay one instead", name)
}

// DecodeBytes decodes a whole transcript already held in memory, for the piped
// path where there is no file to tail.
func DecodeBytes(name, locator, root, streamID string, raw []byte) ([]stream.Record, error) {
	h, err := New(name)
	if err != nil {
		return nil, err
	}
	c, ok := h.(Chunker)
	if !ok {
		return nil, fmt.Errorf("harness: %s transcripts are not a byte stream and cannot be piped; "+
			"replay one by name instead", name)
	}
	s := sessionFor(locator, root, streamID)
	return c.Chunks(s).Decode(raw, stream.SourceRef{Adapter: name, Path: locator})
}

// fileTail follows an append-only transcript file, decoding each chunk of whole
// records the tail hands back.
type fileTail struct {
	tail *stream.Tail
	dec  adapter.Adapter
	name string
	path string
}

func (f *fileTail) Next(ctx context.Context) ([]stream.Record, error) {
	raw, start, err := f.tail.Next(ctx)
	if err != nil {
		return nil, err
	}
	return f.dec.Decode(raw, stream.SourceRef{Adapter: f.name, Path: f.path, Offset: start})
}

func (f *fileTail) Position() int64                 { return f.tail.Offset() }
func (f *fileTail) Historical(r stream.Record) bool { return r.Source.Offset < f.tail.InitialSize() }
func (f *fileTail) Mark() string                    { return fmt.Sprintf("byte %d", f.tail.Offset()) }
func (f *fileTail) Close() error                    { return f.tail.Close() }
