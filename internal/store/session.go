package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/TGPSKI/flow-indicator/internal/event"
)

// File names under a session directory.
const (
	FileSource          = "source.json"
	FileObservations    = "observations.jsonl"
	FileClassifications = "classifications.jsonl"
	FileObligations     = "obligations.jsonl"
	FileRepairs         = "repairs.jsonl"
	FileMetrics         = "metrics.jsonl"
	FileSummary         = "summary.json"
	FileMetricsJSON     = "metrics.json"
	FileReport          = "report.md"
	FileTimeline        = "timeline.csv"
)

// eventFiles are the append-only files, in write order.
var eventFiles = []string{FileObservations, FileClassifications, FileObligations, FileRepairs, FileMetrics}

// Source records where a session's records came from.
type Source struct {
	StreamID  string `json:"stream_id"`
	Adapter   string `json:"adapter"`
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	Records   int    `json:"records"`
	Offset    int64  `json:"offset"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
}

// Session is one stream's directory of append-only files.
type Session struct {
	Dir      string
	StreamID string
	writers  map[string]*Writer
}

// SessionDir returns the directory for a stream under root.
func SessionDir(root, streamID string) string {
	return filepath.Join(root, "sessions", SanitizeID(streamID))
}

// SanitizeID makes a stream identifier safe as a single path element.
func SanitizeID(id string) string {
	if id == "" {
		return "unknown-stream"
	}
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '-'
		}
	}, id)
	return strings.Trim(clean, "-.")
}

// OpenSession creates or opens a session directory.
//
// An existing session with event files is refused unless force is set: the
// event files are append-only, and appending a second replay of the same source
// would double every fact.
func OpenSession(root, streamID string, force bool) (*Session, error) {
	dir := SessionDir(root, streamID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create session directory %s: %w", dir, err)
	}
	existing, err := hasEvents(dir)
	if err != nil {
		return nil, err
	}
	if existing && !force {
		return nil, fmt.Errorf("store: session %s already holds events at %s: pass --force to replace it", streamID, dir)
	}
	if existing {
		for _, name := range eventFiles {
			path := filepath.Join(dir, name)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("store: remove %s: %w", path, err)
			}
		}
	}
	return &Session{Dir: dir, StreamID: streamID, writers: make(map[string]*Writer)}, nil
}

func hasEvents(dir string) (bool, error) {
	for _, name := range eventFiles {
		info, err := os.Stat(filepath.Join(dir, name))
		if err == nil && info.Size() > 0 {
			return true, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("store: stat %s: %w", filepath.Join(dir, name), err)
		}
	}
	return false, nil
}

// FileFor routes an event kind to its append-only file. Routing is by exact
// kind: candidate kinds share the "repair_" prefix with state transitions, and
// a prefix rule would file a classifier candidate as a state change.
func FileFor(kind string) string {
	switch kind {
	case event.KindRecordObserved, event.KindObservationStopped,
		event.KindOperatorInterrupt:
		return FileObservations
	case event.KindSegmentsClassified, event.KindCorrectionCandidate,
		event.KindStopCandidate, event.KindResetCandidate, event.KindPointerCandidate,
		event.KindObligationCandidate, event.KindNearRepeatCandidate,
		event.KindExpansionCandidate, event.KindClassifierFailed,
		event.KindSemanticCompleted:
		return FileClassifications
	case event.KindPointerResolved, event.KindEpochAdvanced,
		event.KindRegimeChanged, event.KindTrendEmerged, event.KindMetricsComputed,
		event.KindStateAtObservationStop:
		return FileMetrics
	}
	switch {
	case strings.HasPrefix(kind, "obligation_"):
		return FileObligations
	case strings.HasPrefix(kind, "repair_"):
		return FileRepairs
	default:
		return FileClassifications
	}
}

// Append writes one event to the file its kind routes to.
func (s *Session) Append(e event.Event) error {
	name := FileFor(e.Kind)
	w, ok := s.writers[name]
	if !ok {
		var err error
		w, err = OpenWriter(filepath.Join(s.Dir, name))
		if err != nil {
			return err
		}
		s.writers[name] = w
	}
	return w.Append(e)
}

// AppendAll writes events in order.
func (s *Session) AppendAll(events []event.Event) error {
	for _, e := range events {
		if err := s.Append(e); err != nil {
			return err
		}
	}
	return nil
}

// Flush pushes every open file's buffer to disk.
func (s *Session) Flush() error {
	for _, name := range eventFiles {
		if w, ok := s.writers[name]; ok {
			if err := w.Flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close flushes every open file.
func (s *Session) Close() error {
	var first error
	for _, name := range eventFiles {
		w, ok := s.writers[name]
		if !ok {
			continue
		}
		if err := w.Close(); err != nil && first == nil {
			first = err
		}
	}
	s.writers = nil
	return first
}

// WriteSource records the provenance of the session's records.
//
// Source metadata is reproducible only when its caller supplied reproducible
// values. This method must not add observation-clock values: source.json is a
// stored artifact and replay must not produce a different file solely because
// it happened later.
func (s *Session) WriteSource(src Source) error {
	return WriteJSON(filepath.Join(s.Dir, FileSource), src)
}

// Path returns the path of a file inside the session directory.
func (s *Session) Path(name string) string { return filepath.Join(s.Dir, name) }

// ReadKind reads every event of one kind from the session.
func ReadKind(dir, kind string) ([]event.Event, error) {
	events, err := ReadEvents(filepath.Join(dir, FileFor(kind)))
	if err != nil {
		return nil, err
	}
	var out []event.Event
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out, nil
}

// ReadAll reads every event file in a session, in file order.
func ReadAll(dir string) ([]event.Event, error) {
	var out []event.Event
	for _, name := range eventFiles {
		events, err := ReadEvents(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, events...)
	}
	return out, nil
}
