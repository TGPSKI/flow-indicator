package harness

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TGPSKI/flow-indicator/internal/adapter"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func init() { Register(Generic{}) }

// Generic reads the neutral JSONL format: one turn per line, stating the
// vocabulary directly rather than being mapped into it.
//
// It is registered as a harness so that every path through this program goes
// through one interface. The fixtures are written in it, which means the frozen
// regression suite exercises the same seam a real harness does.
type Generic struct{}

func (Generic) Name() string { return "generic" }

// DefaultRoot is empty: the generic format is a file layout, not an
// installation. A caller points it at a directory.
func (Generic) DefaultRoot() (string, error) { return "", nil }

func (g Generic) Discover(root string) ([]Session, error) {
	if root == "" {
		return nil, nil
	}
	var out []Session
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, Session{
			ID:       strings.TrimSuffix(d.Name(), ".jsonl"),
			Locator:  path,
			Project:  filepath.Base(filepath.Dir(path)),
			Modified: info.ModTime(),
			Bytes:    info.Size(),
		})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("generic: discover %s: %w", root, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Locator < out[j].Locator })
	return out, nil
}

// Chunks returns the adapter the live meter tails with. The neutral format
// states everything on the line, so the decoder carries no state between
// chunks.
func (Generic) Chunks(Session) adapter.Adapter { return adapter.Generic{} }

func (g Generic) Open(s Session) ([]stream.Record, error) {
	raw, err := os.ReadFile(s.Locator)
	if err != nil {
		return nil, fmt.Errorf("generic: read %s: %w", s.Locator, err)
	}
	return adapter.Generic{}.Decode(raw, stream.SourceRef{Adapter: g.Name(), Path: s.Locator})
}

// OpenPath decodes one session file under any registered harness, without
// discovery.
//
// It is what the calibration harness uses: a corpus manifest names a harness and
// a locator, and the records have to come back the same way whether the locator
// is a file the operator pointed at or a session a scan found. Root is the
// working directory action targets are made relative to, for the harnesses that
// do not carry one in the transcript.
func OpenPath(name, locator, root string) ([]stream.Record, error) {
	h, err := New(name)
	if err != nil {
		return nil, err
	}
	return h.Open(Session{ID: idOf(locator), Locator: locator, Root: root})
}

// idOf is the identifier a bare locator implies: the file's stem, or the
// fragment for a store that addresses sessions inside one file.
func idOf(locator string) string {
	if _, frag, ok := strings.Cut(locator, "#"); ok {
		return frag
	}
	base := filepath.Base(locator)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
