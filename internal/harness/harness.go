// Package harness discovers and opens agent sessions across coding harnesses.
//
// An adapter turns bytes into records. That is the right shape for a file being
// tailed and the wrong shape for everything else a harness might do: opencode
// keeps its transcripts in SQLite, Codex splits sub-agent threads into sibling
// files, and neither is a stream of bytes anyone can hand to a decoder.
//
// A Harness sits above that. It knows where its sessions live, how to enumerate
// them, and how to turn one into canonical records. Everything downstream — the
// action vocabulary, the structural rules, the profiles, the calibration harness
// — reads canonical records and knows nothing about which harness produced them.
// That is the whole point of the layer: a rule calibrated on one harness's
// transcripts applies to the others without being told they exist.
//
// The contract a harness must meet, and the only one:
//
//   - Record.IsOperatorTurn() is true exactly for text the operator typed at the
//     agent. Not harness-injected context, not replayed history, not a tool
//     result wearing a user role. Every transmission metric divides by this.
//   - Record.Actions carry what the agent did, mapped into the closed verb
//     vocabulary, with paths relative to the session root.
//   - Records arrive in source order.
//
// Anything a harness cannot establish is left absent rather than guessed.
package harness

import (
	"fmt"
	"sort"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Session is one transcript a harness holds.
type Session struct {
	// ID is stable and unique within the harness. It is what a corpus manifest
	// names and what a label file points at.
	ID string
	// Locator is how this harness finds the session again: a file path for the
	// file-backed harnesses, a store URI plus a key for the others. It is opaque
	// to everything except the harness that produced it.
	Locator string
	// Root is the working directory the session ran in, when the source records
	// one. Action targets are made relative to it.
	Root string
	// Project groups sessions that share a working tree. A corpus split is
	// stratified on it.
	Project string
	// Modified is when the transcript last changed. A session written to
	// recently may still be running.
	Modified time.Time
	// Bytes is the transcript's size, for bounding what a scan will read.
	Bytes int64
	// Parent names the session that spawned this one, for harnesses that keep
	// sub-agent threads in their own transcripts. Empty for a top-level session.
	//
	// A sub-agent thread is not an operator's session: the turns in it were
	// written by an agent. A corpus that counted them would report an operator
	// who steers far more than they do.
	Parent string
	// Sidechain is set when the transcript marks itself as a nested agent
	// stream without naming what spawned it. It carries the same exclusion as
	// Parent; the two differ only in whether the source named the parent.
	Sidechain bool
	// Harness names the harness that produced this session. DiscoverAll fills
	// it, because a caller holding sessions from several harnesses has to know
	// which one to reopen each through.
	Harness string
}

// IsSubagent reports that the session is a sub-agent thread rather than an
// operator's own.
func (s Session) IsSubagent() bool { return s.Parent != "" || s.Sidechain }

// Harness is one coding agent's on-disk transcript store.
type Harness interface {
	// Name identifies the harness in a manifest and a run record.
	Name() string
	// DefaultRoot is where this harness keeps sessions, honouring whatever
	// environment variable it documents. An empty string with no error means
	// the harness has no default and must be pointed at one.
	DefaultRoot() (string, error)
	// Discover enumerates the sessions under a root. A root that does not exist
	// yields no sessions and no error: not having used a harness is not a
	// failure.
	Discover(root string) ([]Session, error)
	// Open decodes one session into canonical records, in source order.
	Open(s Session) ([]stream.Record, error)
}

// registry holds the harnesses this build knows.
var registry = map[string]Harness{}

// Register adds a harness. It is called from each harness's own file so that
// adding one touches nothing else.
func Register(h Harness) { registry[h.Name()] = h }

// New returns the harness registered under name.
func New(name string) (Harness, error) {
	h, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("harness: unknown harness %q: want one of %v", name, Names())
	}
	return h, nil
}

// Names lists the registered harnesses in a stable order.
func Names() []string {
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// DiscoverAll enumerates every registered harness's sessions from its default
// root.
func DiscoverAll() ([]Session, map[string]error) {
	return discoverDefaults(Names())
}

// DiscoverOnly enumerates one registered harness's sessions from its default
// root. Selecting a harness must narrow the read before discovery starts: an
// opencode result filter applied after DiscoverAll would already have invoked
// sqlite3 and read every other harness store.
func DiscoverOnly(name string) ([]Session, map[string]error) {
	return discoverDefaults([]string{name})
}

// discoverDefaults enumerates only the named harnesses. A root that does not
// exist contributes nothing and no error: not having used a selected harness is
// not a configuration failure.
func discoverDefaults(names []string) ([]Session, map[string]error) {
	var out []Session
	errs := map[string]error{}
	for _, name := range names {
		h, ok := registry[name]
		if !ok {
			errs[name] = fmt.Errorf("harness: unknown harness %q: want one of %v", name, Names())
			continue
		}
		root, err := h.DefaultRoot()
		if err != nil {
			errs[name] = err
			continue
		}
		if root == "" {
			continue
		}
		found, err := h.Discover(root)
		if err != nil {
			errs[name] = err
			continue
		}
		for i := range found {
			found[i].Harness = name
		}
		out = append(out, found...)
	}
	return out, errs
}
