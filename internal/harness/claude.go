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

func init() { Register(Claude{}) }

// Claude reads Claude Code sessions.
//
// The decoding is the existing adapter's: Claude Code writes one JSONL file per
// session, which is exactly the shape an adapter wants, and the byte-stream path
// is what the live meter tails. This type adds the two things an adapter has no
// opinion about — where the sessions are, and which of them are an operator's
// own.
type Claude struct{}

func (Claude) Name() string { return "claude-code" }

func (Claude) DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claude-code: locate home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

func (c Claude) Discover(root string) ([]Session, error) {
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
		// Claude Code keeps a sub-agent's stream in its own file under a
		// subagents directory, and also marks every record inside it. The
		// directory names the parent; the record only says that there is one.
		if strings.Contains(rel, string(filepath.Separator)+"subagents"+string(filepath.Separator)) {
			s.Parent = filepath.Base(filepath.Dir(filepath.Dir(path)))
		}
		// The working directory is only in the records. It is read here rather
		// than left to the caller because selecting the session running in this
		// directory is the one thing every caller of Discover wants, and the
		// encoded directory name is lossy.
		//
		// A file that never names one keeps an empty Root rather than being
		// dropped: Discover reports what is on disk, and deciding what is worth
		// following belongs to the caller. Claude Code writes sidecars next to
		// the real transcripts — an `.orphaned-*.jsonl` carrying a title and an
		// agent name and no conversation — and those are what an empty Root
		// picks out here.
		//
		// The identifier stays the file's stem. That is the session UUID, it is
		// what a sealed manifest already names, and re-deriving it from a field
		// inside the file would reshuffle a corpus split that is a function of
		// it.
		if head, ok := claudeHeadOf(path); ok {
			s.Root = head.cwd
			s.Sidechain = head.sidechain
		}
		out = append(out, s)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("claude-code: discover %s: %w", root, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Locator < out[j].Locator })
	return out, nil
}

// claudeHeadScanLines bounds how far into a session the head read goes. The
// working directory is on the first records; a file that has not named one by
// then is not identified by this path.
const claudeHeadScanLines = 200

// claudeHead is what the opening records establish about a session.
type claudeHead struct {
	cwd       string
	sidechain bool
}

// claudeHeadOf reads the identifying fields off the head of a session file.
//
// A sub-agent transcript is not the operator's session: every record in it was
// written inside a nested agent stream. The first identified record decides,
// because a file is one stream or the other.
func claudeHeadOf(path string) (claudeHead, bool) {
	f, err := os.Open(path)
	if err != nil {
		return claudeHead{}, false
	}
	defer f.Close()
	sc := newLineScanner(f)
	for i := 0; i < claudeHeadScanLines && sc.Scan(); i++ {
		var rec struct {
			CWD         string `json:"cwd"`
			IsSidechain bool   `json:"isSidechain"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.CWD == "" {
			continue
		}
		return claudeHead{cwd: rec.CWD, sidechain: rec.IsSidechain}, true
	}
	return claudeHead{}, false
}

// Chunks returns the adapter the live meter tails with. Claude Code records
// every fact a record needs on that record, so the decoder carries no state
// between chunks.
func (Claude) Chunks(Session) adapter.Adapter { return adapter.Claude{} }

func (c Claude) Open(s Session) ([]stream.Record, error) {
	raw, err := os.ReadFile(s.Locator)
	if err != nil {
		return nil, fmt.Errorf("claude-code: read %s: %w", s.Locator, err)
	}
	return adapter.Claude{}.Decode(raw, stream.SourceRef{Adapter: c.Name(), Path: s.Locator})
}
