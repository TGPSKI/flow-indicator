package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/calibrate"
	"github.com/TGPSKI/flow-indicator/internal/harness"
)

// corpusCmd surveys a directory of harness sessions and builds a sealed corpus.
//
// Two steps, deliberately separate. The survey reports what is there and takes
// nothing; the build copies the selected sessions and writes the manifest that
// seals them. Reading the survey before building is how the operator sees what
// the corpus is made of rather than being handed a number.
func corpusCmd(args []string) error {
	fs := flag.NewFlagSet("corpus", flag.ExitOnError)
	scanRoot := fs.String("scan", "", "directory to survey; empty uses the harness's own default location")
	harnessName := fs.String("harness", "claude-code", "which harness to survey: "+strings.Join(harness.Names(), ", "))
	outDir := fs.String("out", "", "corpus directory to write sealed copies and the manifest into")
	perProject := fs.Int("per-project", 2, "at most this many sessions from any one project; 0 means no cap")
	minTurns := fs.Int("min-turns", 12, "fewest operator turns a session must carry")
	maxBytes := fs.Int64("max-bytes", 32<<20, "skip sessions larger than this")
	quietFor := fs.Duration("quiet-for", time.Hour, "skip sessions written to more recently than this")
	build := fs.Bool("build", false, "write the manifest sealing the selected sessions; without it, only survey")
	copyIn := fs.Bool("copy", false, "copy each session into the corpus directory instead of referencing it where it lies")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *build && *outDir == "" {
		return errors.New("corpus: --build needs --out")
	}

	stats, err := calibrate.ScanHarness(*harnessName, *scanRoot, calibrate.ScanOptions{
		MinOperatorTurns: *minTurns,
		MaxBytes:         *maxBytes,
		QuietFor:         *quietFor,
	})
	if err != nil {
		return err
	}
	candidates := calibrate.Candidates(stats)
	fmt.Print(renderSurvey(stats, candidates))
	if !*build {
		fmt.Printf("\nsurvey only; pass --build --out <dir> to seal a corpus from these %d candidates\n", len(candidates))
		return nil
	}

	var prior *calibrate.Manifest
	manifestPath := filepath.Join(*outDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err == nil {
		if prior, err = calibrate.LoadManifest(manifestPath); err != nil {
			return err
		}
	}

	// A session already sealed under one identifier must not be sealed again
	// under another. The seed corpus was copied by hand and named by hand, and
	// the same transcripts still sit upstream under their harness identifiers;
	// taking both would double every turn in them and would put one session on
	// both sides of the split.
	sealed, err := sealedHashes(*outDir, prior)
	if err != nil {
		return err
	}
	var deduped []calibrate.SessionStat
	var duplicates int
	for _, c := range candidates {
		sum, err := hashOf(c.Path)
		if err != nil {
			return err
		}
		if id, ok := sealed[sum]; ok {
			duplicates++
			_ = id
			continue
		}
		deduped = append(deduped, c)
	}
	if duplicates > 0 {
		fmt.Printf("  %4d  already sealed under another identifier\n", duplicates)
	}
	candidates = deduped

	assigned := calibrate.Assign(candidates, prior, *perProject)
	byID := map[string]calibrate.SessionStat{}
	for _, c := range candidates {
		byID[c.ID] = c
	}
	if err := os.MkdirAll(*outDir, 0o700); err != nil {
		return fmt.Errorf("corpus: create %s: %w", *outDir, err)
	}

	// What seals a session is its hash in the manifest, not where the bytes sit.
	//
	// The default is to reference each session where it already lies. A corpus of
	// transcripts is the most private thing this program touches, and copying
	// hundreds of megabytes of it to a second place doubles what has to be
	// protected without making the seal any stronger: Verify re-hashes the file
	// on every run either way, and a session that is resumed and grows fails that
	// check whichever directory it is in.
	//
	// --copy takes a snapshot instead, for a corpus that has to outlive the
	// directory it was drawn from.
	var added int
	for i := range assigned {
		s := &assigned[i]
		src, ok := byID[s.ID]

		if !*copyIn {
			if !ok {
				// Already in the manifest from an earlier run. Its recorded path
				// still stands; re-hash it so a file that moved or changed is
				// caught here rather than inside a score.
				if s.Path == "" {
					return fmt.Errorf("corpus: session %q is in the manifest with no path", s.ID)
				}
				if s.SHA256, err = hashOf(prior.Resolve(*s)); err != nil {
					return err
				}
				continue
			}
			s.Path = src.Path
			if s.SHA256, err = hashOf(src.Path); err != nil {
				return err
			}
			added++
			continue
		}

		if s.Path == "" || filepath.IsAbs(s.Path) {
			s.Path = s.ID + ".jsonl"
		}
		dst := filepath.Join(*outDir, s.Path)
		if _, err := os.Stat(dst); err == nil {
			// Already sealed. Re-copying would change the bytes under a hash
			// that a recorded score already named.
			if s.SHA256 == "" {
				if s.SHA256, err = hashOf(dst); err != nil {
					return err
				}
			}
			continue
		}
		if !ok {
			return fmt.Errorf("corpus: the manifest names session %q but this scan did not find it, and %s does not exist", s.ID, dst)
		}
		if err := copyFile(src.Path, dst); err != nil {
			return err
		}
		if s.SHA256, err = hashOf(dst); err != nil {
			return err
		}
		added++
	}

	raw, err := json.MarshalIndent(calibrate.Manifest{Sessions: assigned}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("corpus: write %s: %w", manifestPath, err)
	}
	where := "referenced in place"
	if *copyIn {
		where = "copied into " + *outDir
	}
	fmt.Printf("\nsealed %d new session(s), %s; manifest at %s\n", added, where, manifestPath)
	fmt.Print(renderSplit(assigned, byID))
	return nil
}

// renderSurvey reports what the scan found, taken and skipped alike. A corpus
// built by silently discarding candidates cannot be audited.
func renderSurvey(stats, candidates []calibrate.SessionStat) string {
	var b strings.Builder
	skips := map[string]int{}
	var turns, actions int
	for _, s := range stats {
		if s.Skipped != "" {
			skips[skipReason(s.Skipped)]++
		}
	}
	for _, c := range candidates {
		turns += c.OperatorTurns
		actions += c.Actions
	}
	fmt.Fprintf(&b, "scanned %d session files: %d candidates, %d skipped\n", len(stats), len(candidates), len(stats)-len(candidates))
	if len(skips) > 0 {
		reasons := make([]string, 0, len(skips))
		for r := range skips {
			reasons = append(reasons, r)
		}
		sort.Strings(reasons)
		for _, r := range reasons {
			fmt.Fprintf(&b, "  %4d  %s\n", skips[r], r)
		}
	}
	fmt.Fprintf(&b, "candidates carry %d operator turns and %d actions across %d projects\n",
		turns, actions, countProjects(candidates))
	if len(candidates) > 0 && actions == 0 {
		b.WriteString("  no actions were observed: every rule that reads a write set will stay silent on this corpus\n")
	}
	return b.String()
}

// renderSplit shows what landed on each side, so the balance is visible without
// re-reading the manifest.
func renderSplit(sessions []calibrate.ManifestSession, byID map[string]calibrate.SessionStat) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprint(w, "\nsplit\tsessions\toperator turns\tactions\tprojects\n")
	for _, split := range []string{calibrate.SplitDev, calibrate.SplitHoldout} {
		var n, turns, actions int
		projects := map[string]struct{}{}
		for _, s := range sessions {
			if s.Split != split {
				continue
			}
			n++
			projects[s.Domain] = struct{}{}
			if st, ok := byID[s.ID]; ok {
				turns += st.OperatorTurns
				actions += st.Actions
			}
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\n", split, n, turns, actions, len(projects))
	}
	_ = w.Flush()
	return b.String()
}

// skipReason collapses a reason to its stable prefix so counts group.
func skipReason(s string) string {
	if i := strings.Index(s, ","); i > 0 {
		return s[i+2:]
	}
	return s
}

func countProjects(stats []calibrate.SessionStat) int {
	seen := map[string]struct{}{}
	for _, s := range stats {
		seen[s.Project] = struct{}{}
	}
	return len(seen)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("corpus: open %s: %w", src, err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("corpus: create %s: %w", filepath.Dir(dst), err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("corpus: create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("corpus: copy %s: %w", src, err)
	}
	return out.Close()
}

func hashOf(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("corpus: open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("corpus: hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sealedHashes maps the content hash of every session already in the corpus to
// the identifier it was sealed under.
//
// It reads the files rather than trusting the manifest's recorded hashes: the
// manifest is what a score names, and using it to decide what to add would let a
// stale entry admit a duplicate.
func sealedHashes(dir string, prior *calibrate.Manifest) (map[string]string, error) {
	out := map[string]string{}
	if prior == nil {
		return out, nil
	}
	for _, s := range prior.Sessions {
		path := filepath.Join(dir, s.Path)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		sum, err := hashOf(path)
		if err != nil {
			return nil, err
		}
		out[sum] = s.ID
	}
	return out, nil
}
