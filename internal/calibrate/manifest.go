// Package calibrate replays labeled sessions and scores what the rules said
// against what a person judged.
//
// Every number it produces names its own subject: the artifact that produced it,
// the corpus it ran over, the rule version behind the predictions, and the label
// set it was scored against. A calibration run that cannot name those is not
// evidence, and this package will not emit one that does not.
package calibrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// newReader is a reader over the manifest bytes, kept separate so the hash is
// taken over exactly what was decoded.
func newReader(raw []byte) io.Reader { return bytes.NewReader(raw) }

// Splits. The split is by session, never by turn: turns inside a session are not
// independent, and a turn-level split leaks the session's vocabulary and subject
// matter across the boundary.
const (
	// SplitDev may be iterated on freely.
	SplitDev = "dev"
	// SplitHoldout is sealed. Opening it spends budget and is recorded.
	SplitHoldout = "holdout"
)

// ManifestSession is one session in the corpus, and which side of the split it
// is on.
type ManifestSession struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	// Adapter names the harness that reads this session: claude-code, codex,
	// opencode or generic. The key keeps its old name so a manifest written
	// before harnesses existed still loads.
	Adapter string `json:"adapter"`
	// Root is the working directory action targets are made relative to, for
	// sources that do not carry one in the transcript.
	Root  string `json:"root,omitempty"`
	Split string `json:"split"`
	// SHA256 is the source file's hash. It is checked on every run: a corpus
	// that changed underneath a score makes the score a number about nothing.
	SHA256 string `json:"sha256"`
	// Domain and Harness are what the generalization gate reads. A corpus that
	// is all one domain cannot tell a rule that learned the speech act from one
	// that learned the domain's vocabulary.
	Domain  string `json:"domain,omitempty"`
	Harness string `json:"harness,omitempty"`
}

// Manifest is the corpus and its split.
type Manifest struct {
	Sessions []ManifestSession `json:"sessions"`

	// SHA256 is the hash of the manifest file's bytes, carried in every run
	// record so a score names the corpus definition it ran against.
	SHA256 string `json:"-"`
	// Dir is the directory the manifest was read from; session paths are
	// resolved relative to it.
	Dir string `json:"-"`
}

// LoadManifest reads a corpus manifest.
func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("calibrate: read manifest %s: %w", path, err)
	}
	var m Manifest
	dec := json.NewDecoder(newReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("calibrate: decode manifest %s: %w", path, err)
	}
	sum := sha256.Sum256(raw)
	m.SHA256 = hex.EncodeToString(sum[:])
	m.Dir = filepath.Dir(path)

	seen := make(map[string]struct{}, len(m.Sessions))
	for _, s := range m.Sessions {
		switch s.Split {
		case SplitDev, SplitHoldout:
		default:
			return nil, fmt.Errorf("calibrate: session %q has split %q, want %q or %q", s.ID, s.Split, SplitDev, SplitHoldout)
		}
		if s.ID == "" || s.Path == "" {
			return nil, fmt.Errorf("calibrate: a manifest session is missing an id or a path")
		}
		if _, dup := seen[s.ID]; dup {
			return nil, fmt.Errorf("calibrate: session id %q appears twice", s.ID)
		}
		seen[s.ID] = struct{}{}
	}
	return &m, nil
}

// Select returns the sessions on one side of the split, in manifest order.
func (m *Manifest) Select(split string) []ManifestSession {
	var out []ManifestSession
	for _, s := range m.Sessions {
		if s.Split == split {
			out = append(out, s)
		}
	}
	return out
}

// Resolve returns a session's absolute source path.
func (m *Manifest) Resolve(s ManifestSession) string {
	if filepath.IsAbs(s.Path) {
		return s.Path
	}
	return filepath.Join(m.Dir, s.Path)
}

// Verify checks that each selected session's bytes still hash to what the
// manifest recorded.
//
// A session whose manifest entry carries no hash is an error rather than a pass.
// The manifest is what seals the corpus, and an unhashed entry seals nothing.
func (m *Manifest) Verify(sessions []ManifestSession) error {
	for _, s := range sessions {
		if s.SHA256 == "" {
			return fmt.Errorf("calibrate: session %q records no hash, so the corpus it names is not sealed", s.ID)
		}
		got, err := hashFile(m.Resolve(s))
		if err != nil {
			return err
		}
		if got != s.SHA256 {
			return fmt.Errorf("calibrate: session %q hashes to %s, manifest says %s: the corpus changed under the score", s.ID, got, s.SHA256)
		}
	}
	return nil
}

// Domains lists the distinct domains among the sessions, sorted.
func Domains(sessions []ManifestSession) []string {
	return distinct(sessions, func(s ManifestSession) string { return s.Domain })
}

// Harnesses lists the distinct harnesses among the sessions, sorted.
func Harnesses(sessions []ManifestSession) []string {
	return distinct(sessions, func(s ManifestSession) string { return s.Harness })
}

func distinct(sessions []ManifestSession, key func(ManifestSession) string) []string {
	seen := map[string]struct{}{}
	for _, s := range sessions {
		if v := key(s); v != "" {
			seen[v] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("calibrate: open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("calibrate: hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ArtifactHash hashes the running binary, so a run record names the bytes that
// produced it. An artifact that cannot be located is reported as such rather
// than left blank: a blank field reads as "no artifact", not "not resolved".
func ArtifactHash() string {
	exe, err := os.Executable()
	if err != nil {
		return "unresolved: " + err.Error()
	}
	sum, err := hashFile(exe)
	if err != nil {
		return "unresolved: " + err.Error()
	}
	return sum
}
