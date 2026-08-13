package harness

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TGPSKI/flow-indicator/internal/adapter"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func init() { Register(Qwen{}) }

// Qwen reads Qwen Code CLI sessions.
//
// The decoding is the existing adapter's, same as Claude Code: one JSONL file
// per session is exactly the shape an adapter wants, and the byte-stream path
// is what the live meter tails. This type adds the two things an adapter has
// no opinion about — where the sessions are, and which of them are an
// operator's own.
type Qwen struct{}

func (Qwen) Name() string { return "qwen" }

func (Qwen) DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("qwen: locate home directory: %w", err)
	}
	return filepath.Join(home, ".qwen", "projects"), nil
}

func (q Qwen) Discover(root string) ([]Session, error) {
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
		rel, _ := filepath.Rel(root, path)
		project, _, _ := strings.Cut(rel, string(filepath.Separator))
		s := Session{
			ID:       strings.TrimSuffix(d.Name(), ".jsonl"),
			Locator:  path,
			Project:  project,
			Modified: info.ModTime(),
			Bytes:    info.Size(),
		}
		// Qwen Code keeps a sub-agent's stream in its own file under a
		// subagents directory, one per spawning session, and every record in
		// it carries isSidechain plus the top-level sessionId that spawned
		// it. That sessionId, not the file's own stem, names the parent: the
		// file's stem is a per-call identifier a sealed manifest already
		// treats as this session's ID, and re-deriving the parent from the
		// path would fight a directory layout that may not stay the same
		// across releases.
		if head, ok := qwenHeadOf(path); ok {
			s.Root = head.cwd
			s.Sidechain = head.sidechain
			if head.sidechain {
				s.Parent = head.sessionID
			}
		}
		out = append(out, s)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("qwen: discover %s: %w", root, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Locator < out[j].Locator })
	return out, nil
}

// qwenHeadScanLines bounds how far into a session the head read goes. The
// working directory is on the opening records; a file that has not named one
// by then is not identified by this path.
const qwenHeadScanLines = 200

// qwenHead is what the opening records establish about a session.
type qwenHead struct {
	cwd       string
	sessionID string
	sidechain bool
}

func qwenHeadOf(path string) (qwenHead, bool) {
	f, err := os.Open(path)
	if err != nil {
		return qwenHead{}, false
	}
	defer f.Close()
	sc := newLineScanner(f)
	for i := 0; i < qwenHeadScanLines && sc.Scan(); i++ {
		var rec struct {
			CWD         string `json:"cwd"`
			SessionID   string `json:"sessionId"`
			IsSidechain bool   `json:"isSidechain"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.CWD == "" {
			continue
		}
		return qwenHead{cwd: rec.CWD, sessionID: rec.SessionID, sidechain: rec.IsSidechain}, true
	}
	return qwenHead{}, false
}

// Chunks returns the adapter the live meter tails with. Qwen Code records
// every fact a record needs on that record, so the decoder carries no state
// between chunks.
func (Qwen) Chunks(Session) adapter.Adapter { return adapter.Qwen{} }

func (q Qwen) Open(s Session) ([]stream.Record, error) {
	raw, err := os.ReadFile(s.Locator)
	if err != nil {
		return nil, fmt.Errorf("qwen: read %s: %w", s.Locator, err)
	}
	return adapter.Qwen{}.Decode(raw, stream.SourceRef{Adapter: q.Name(), Path: s.Locator})
}
