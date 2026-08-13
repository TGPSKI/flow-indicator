package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/calibrate"
	"github.com/TGPSKI/flow-indicator/internal/labeler"
	"github.com/TGPSKI/flow-indicator/internal/labels"
)

// annotateCmd runs a model over the emitted units and writes candidate labels.
//
// Candidate is the operative word and it is in every path this command writes.
// A model's judgement is not ground truth; what this produces is a pass that an
// adjudicator accepts or rejects, and the label files name the model so a
// calibration run scored against them says what judged the corpus.
func annotateCmd(args []string) error {
	fs := flag.NewFlagSet("annotate", flag.ExitOnError)
	manifestPath := fs.String("manifest", "", "corpus manifest")
	outDir := fs.String("out", "", "directory to write candidate label files into")
	split := fs.String("split", calibrate.SplitDev, "which side of the split to annotate")
	family := fs.String("family", "", "one family, or empty for all of them")
	sample := fs.Int("sample-per-mille", 0, "annotate this many operator turns out of every thousand")
	endpoint := fs.String("endpoint", "http://127.0.0.1:8000/v1/chat/completions", "OpenAI-compatible chat completions endpoint")
	model := fs.String("model", "", "model name to send")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" || *outDir == "" || *model == "" {
		return errors.New("annotate: --manifest, --out and --model are required")
	}
	m, err := calibrate.LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	sessions := m.Select(*split)
	if len(sessions) == 0 {
		return fmt.Errorf("annotate: no session on the %s split", *split)
	}
	families := []string{labels.FamilyObligation, labels.FamilyRecovery, labels.FamilyPollution, labels.FamilyObligationPair}
	if *family != "" {
		families = []string{*family}
	}
	if err := os.MkdirAll(*outDir, 0o700); err != nil {
		return fmt.Errorf("annotate: create %s: %w", *outDir, err)
	}

	client := labeler.New(*endpoint, *model, os.Getenv("FLOW_INDICATOR_API_KEY"))
	ctx := context.Background()
	pick := calibrate.SampleTurns(*sample)

	for _, fam := range families {
		var units []calibrate.Unit
		for _, s := range sessions {
			u, err := calibrate.EmitUnits(m, s, fam, pick)
			if err != nil {
				return err
			}
			units = append(units, u...)
		}
		if len(units) == 0 {
			fmt.Printf("%-18s no units\n", fam)
			continue
		}
		for _, pass := range labeler.Passes {
			started := time.Now()
			var lastPrint time.Time
			got, err := client.Label(ctx, labeler.Options{
				Family: fam,
				Units:  units,
				Pass:   pass,
				Abandoned: func(units, total int) {
					fmt.Printf("\r%-18s %-6s ABANDONED %d of %d units: the endpoint could not be read for them\n",
						fam, pass.Name, units, total)
				},
				Progress: func(done, total int) {
					if time.Since(lastPrint) < 15*time.Second && done < total {
						return
					}
					lastPrint = time.Now()
					fmt.Printf("\r%-18s %-6s %5d/%-5d  %s", fam, pass.Name, done, total,
						strings.TrimSpace(time.Since(started).Round(time.Second).String()))
				},
			})
			if err != nil {
				return err
			}
			path := filepath.Join(*outDir, fmt.Sprintf("%s-%s-%s.jsonl", *split, fam, pass.Name))
			if err := writeLabels(path, got); err != nil {
				return err
			}
			fmt.Printf("\r%-18s %-6s %5d/%-5d labeled in %s -> %s%s\n",
				fam, pass.Name, len(got), len(units),
				time.Since(started).Round(time.Second), filepath.Base(path), degenerate(got))
		}
	}
	return nil
}

// degenerate names a pass that put every unit in one class.
//
// Such a pass agrees with nothing and disagrees with nothing: its kappa against
// any other pass is zero or undefined, and it contributes no information about
// the codebook. It is a defect in the instructions, not a finding about the
// corpus, and it is called out where it happens rather than left to be
// discovered as a strange number three steps later.
func degenerate(ls []labels.Label) string {
	if len(ls) < 2 {
		return ""
	}
	first := ls[0].Label
	for _, l := range ls {
		if l.Label != first {
			return ""
		}
	}
	return fmt.Sprintf("  [DEGENERATE: every unit labeled %q; this pass measures nothing]", first)
}

// writeLabels writes one pass's candidate labels.
func writeLabels(path string, ls []labels.Label) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("annotate: create %s: %w", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, l := range ls {
		if err := enc.Encode(l); err != nil {
			return fmt.Errorf("annotate: write %s: %w", path, err)
		}
	}
	return f.Close()
}
