package calibrate

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/harness"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// SessionStat is what a scan establishes about one candidate session. Every
// field is counted from the decoded records; nothing here is a judgement about
// whether the session is any good.
type SessionStat struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Project string `json:"project"`
	// Harness names what read this session, and Root is the working directory
	// its action targets are relative to.
	Harness       string    `json:"harness,omitempty"`
	Root          string    `json:"root,omitempty"`
	Bytes         int64     `json:"bytes"`
	Records       int       `json:"records"`
	OperatorTurns int       `json:"operator_turns"`
	Actions       int       `json:"actions"`
	WriteActions  int       `json:"write_actions"`
	Interrupts    int       `json:"interrupts"`
	Modified      time.Time `json:"modified"`
	// Skipped names why a session was not a candidate, empty when it is one.
	Skipped string `json:"skipped,omitempty"`
}

// ScanOptions bound what counts as a candidate session.
type ScanOptions struct {
	// MinOperatorTurns is the fewest authored turns a session must carry. A
	// session of three turns contributes almost nothing to any family's
	// denominator and costs a labeler the same fixed overhead as a long one.
	MinOperatorTurns int
	// MaxBytes skips sessions too large to label. Zero means no ceiling.
	MaxBytes int64
	// QuietFor skips sessions written to recently. A file still being appended
	// to cannot be sealed by a hash, and it may belong to a session running in
	// another terminal right now.
	QuietFor time.Duration
	// Now is the clock the quiet period is measured against. Zero means the
	// wall clock.
	Now time.Time
}

// DefaultScanOptions are the bounds a scan uses when none are given.
func DefaultScanOptions() ScanOptions {
	return ScanOptions{MinOperatorTurns: 12, MaxBytes: 32 << 20, QuietFor: time.Hour}
}

// Scan enumerates one harness's sessions and counts each one.
//
// Sessions that fail a bound are returned with Skipped set rather than dropped.
// A corpus built by silently discarding candidates cannot be audited: the reader
// needs to see what was available as well as what was taken.
func Scan(root string, opts ScanOptions) ([]SessionStat, error) {
	return ScanHarness("claude-code", root, opts)
}

// ScanHarness enumerates one named harness and counts each session it holds.
//
// Counting means decoding: how many turns an operator actually took is not
// something a file's size or name establishes, and it is the number every bound
// here is stated in.
func ScanHarness(name, root string, opts ScanOptions) ([]SessionStat, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	h, err := harness.New(name)
	if err != nil {
		return nil, err
	}
	if root == "" {
		if root, err = h.DefaultRoot(); err != nil {
			return nil, err
		}
	}
	found, err := h.Discover(root)
	if err != nil {
		return nil, err
	}

	var out []SessionStat
	for _, sess := range found {
		stat := SessionStat{
			ID:       sess.ID,
			Path:     sess.Locator,
			Project:  sess.Project,
			Harness:  name,
			Root:     sess.Root,
			Bytes:    sess.Bytes,
			Modified: sess.Modified,
		}
		if stat.Project == "" {
			stat.Project = name
		}
		switch {
		case sess.IsSubagent():
			// A sub-agent thread's turns were written by an agent. A corpus
			// that counted them would report an operator who steers far more
			// than they do.
			stat.Skipped = "a sub-agent thread, not an operator's session"
		case opts.MaxBytes > 0 && stat.Bytes > opts.MaxBytes:
			stat.Skipped = fmt.Sprintf("larger than the %d-byte ceiling", opts.MaxBytes)
		case opts.QuietFor > 0 && !stat.Modified.IsZero() && now.Sub(stat.Modified) < opts.QuietFor:
			stat.Skipped = fmt.Sprintf("written to within the last %s; a growing file cannot be sealed by a hash", opts.QuietFor)
		}
		if stat.Skipped != "" {
			out = append(out, stat)
			continue
		}

		records, err := h.Open(sess)
		if err != nil {
			stat.Skipped = "does not decode: " + err.Error()
			out = append(out, stat)
			continue
		}
		stat.Records = len(records)
		for _, r := range records {
			if r.IsOperatorTurn() {
				stat.OperatorTurns++
			}
			if r.Metadata[stream.MetaInputMode] == stream.InputInterrupt {
				stat.Interrupts++
			}
			stat.Actions += len(r.Actions)
			stat.WriteActions += len(r.WriteTargets())
		}
		if stat.OperatorTurns < opts.MinOperatorTurns {
			stat.Skipped = fmt.Sprintf("%d operator turns, below the minimum of %d", stat.OperatorTurns, opts.MinOperatorTurns)
		}
		out = append(out, stat)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Candidates are the scanned sessions that passed every bound.
func Candidates(stats []SessionStat) []SessionStat {
	var out []SessionStat
	for _, s := range stats {
		if s.Skipped == "" {
			out = append(out, s)
		}
	}
	return out
}

// Assign places candidate sessions on the dev or holdout side of the split.
//
// The assignment is a function of the session's identifier and nothing else, so
// nobody chose which sessions the holdout got. A split chosen by a person is a
// split that can be chosen again when the first one gives a disappointing
// number, and a holdout that can be reshuffled is not a holdout.
//
// It is stratified by project. Hashing alone can put a whole project — and so a
// whole domain and vocabulary — on one side, and a holdout that shares no
// project with dev measures transfer between projects rather than the
// generalization the gate is asking about. Within each project the sessions are
// ordered by their hash and dealt alternately, which balances the two sides
// without letting anyone pick.
//
// Sessions already in prior keep the split they had. A session that has been
// iterated against cannot become holdout by being rescanned, and a holdout
// session that has been opened stays spent.
func Assign(candidates []SessionStat, prior *Manifest, perProject int) []ManifestSession {
	existing := map[string]ManifestSession{}
	if prior != nil {
		for _, s := range prior.Sessions {
			existing[s.ID] = s
		}
	}

	byProject := map[string][]SessionStat{}
	for _, c := range candidates {
		byProject[c.Project] = append(byProject[c.Project], c)
	}
	projects := make([]string, 0, len(byProject))
	for p := range byProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)

	var out []ManifestSession
	dealt := map[string]bool{}
	for _, p := range projects {
		group := byProject[p]
		sort.Slice(group, func(i, j int) bool { return hashOrder(group[i].ID) < hashOrder(group[j].ID) })
		if perProject > 0 && len(group) > perProject {
			group = group[:perProject]
		}
		// The deal starts on a parity taken from the project's own name. Dealing
		// every project from the same side sends every single-session project to
		// dev, and most projects have one session: the holdout then contains only
		// projects dev has also seen, and the question it is meant to answer —
		// whether a rule works on material it was not shaped against — is one it
		// cannot ask. The parity is still a hash of a name nobody picked.
		start := int(hashOrder(p) % 2)
		for i, c := range group {
			if kept, ok := existing[c.ID]; ok {
				out = append(out, kept)
				delete(existing, c.ID)
				continue
			}
			if dealt[c.ID] {
				// Two candidates claiming one identifier. Taking both writes a
				// manifest that will not load, and silently taking one puts a
				// session in the corpus that nobody can point at.
				continue
			}
			dealt[c.ID] = true
			split := SplitDev
			if (start+i)%2 == 1 {
				split = SplitHoldout
			}
			adapterName := c.Harness
			if adapterName == "" {
				adapterName = "claude-code"
			}
			out = append(out, ManifestSession{
				ID:      c.ID,
				Path:    c.Path,
				Root:    c.Root,
				Adapter: adapterName,
				Split:   split,
				Domain:  p,
				Harness: adapterName,
			})
		}
	}
	// Sessions the prior manifest held that this scan did not see are kept: a
	// corpus does not shrink because a source directory was reorganized.
	seen := map[string]bool{}
	for _, s := range out {
		seen[s.ID] = true
	}
	if prior != nil {
		for _, s := range prior.Sessions {
			if !seen[s.ID] {
				out = append(out, s)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// hashOrder is the deterministic order a session takes within its project. It is
// derived from the identifier alone, so re-running a scan deals the same hand.
func hashOrder(id string) uint64 {
	sum := sha256.Sum256([]byte(id))
	return binary.BigEndian.Uint64(sum[:8])
}
