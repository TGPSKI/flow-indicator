package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/TGPSKI/flow-indicator/internal/calibrate"
	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/labels"
)

func calibrateCmd(args []string) error {
	fs := flag.NewFlagSet("calibrate", flag.ExitOnError)
	var common commonFlags
	common.bind(fs)
	labelDir := fs.String("labels", "", "directory of label files")
	manifestPath := fs.String("manifest", "", "corpus manifest naming the sessions and the split")
	split := fs.String("split", calibrate.SplitDev, "which side of the split to score: dev or holdout")
	reason := fs.String("reason", "", "why the holdout is being opened; required for --split holdout")
	classifierMode := fs.String("classifier", "heuristic", "evidence path to score: heuristic or configured")
	asJSON := fs.Bool("json", false, "write the run record as JSON instead of the table")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *labelDir == "" || *manifestPath == "" {
		return errors.New("calibrate: --labels and --manifest are required")
	}
	cfg, _, err := common.load()
	if err != nil {
		return err
	}
	manifest, err := calibrate.LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	labelSet, err := labels.Load(*labelDir)
	if err != nil {
		return err
	}
	rules, err := common.rules()
	if err != nil {
		return err
	}
	var classifier classify.Classifier
	switch *classifierMode {
	case "heuristic":
		classifier = classify.Heuristic{Rules: rules}
	case "configured":
		classifier, err = classifierFor(cfg, rules, false)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("calibrate: --classifier must be heuristic or configured, got %q", *classifierMode)
	}
	rep, err := calibrate.Run(context.Background(), calibrate.Options{
		Manifest:   manifest,
		Labels:     labelSet,
		Config:     cfg,
		Classifier: classifier,
		Split:      *split,
		Reason:     *reason,
	})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	fmt.Print(renderReport(rep))
	return nil
}

// renderReport prints the run record.
//
// The header comes first and is not optional. A score whose subject is not on
// the page is a number nobody can reproduce, and the whole point of this command
// is that no number it produces is reproducible only by the person who ran it.
func renderReport(r *calibrate.Report) string {
	var b strings.Builder

	fmt.Fprintf(&b, "calibration run — %s split\n\n", r.Split)
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  artifact\t%s\n", r.Artifact)
	fmt.Fprintf(w, "  corpus manifest\t%s\n", r.ManifestHash)
	fmt.Fprintf(w, "  label set\t%s\n", r.LabelHash)
	fmt.Fprintf(w, "  rules\t%s v%s %s\n", r.Classifier, r.RuleVersion, r.RuleHash)
	fmt.Fprintf(w, "  sessions\t%s\n", strings.Join(r.Sessions, ", "))
	if len(r.Domains) > 0 {
		fmt.Fprintf(w, "  domains\t%s\n", strings.Join(r.Domains, ", "))
	}
	if r.Reason != "" {
		fmt.Fprintf(w, "  holdout opened for\t%s\n", r.Reason)
	}
	_ = w.Flush()

	for _, s := range r.Scores {
		cov := r.Coverage[s.Family]
		fmt.Fprintf(&b, "\n%s\n", s.Family)
		if s.Labeled == 0 {
			fmt.Fprintf(&b, "  no labeled unit; %d units predicted and none judged\n", cov.PredictedUnits)
			continue
		}
		fmt.Fprintf(&b, "  precision %s   recall %s   f1 %s\n",
			labels.FormatRate(s.Precision), labels.FormatRate(s.Recall), labels.FormatRate(s.F1))
		fmt.Fprintf(&b, "  tp %d  fp %d  fn %d  tn %d   over %d labeled units, %d unlabelable\n",
			s.TruePositives, s.FalsePositives, s.FalseNegatives, s.TrueNegatives, s.Labeled, s.Unlabelable)
		fmt.Fprintf(&b, "  consequence: %s\n", s.Consequence)
		b.WriteString(renderConfusion(s))
	}

	if len(r.Discriminators) > 0 {
		b.WriteString("\ndiscriminators, each alone\n")
		dw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprint(dw, "  name\tprecision\trecall\tf1\n")
		for _, d := range r.Discriminators {
			fmt.Fprintf(dw, "  %s\t%s\t%s\t%s\n", d.Name,
				labels.FormatRate(d.Score.Precision), labels.FormatRate(d.Score.Recall), labels.FormatRate(d.Score.F1))
		}
		_ = dw.Flush()
	}

	if len(r.Agreements) > 0 {
		b.WriteString("\nlabel agreement\n")
		aw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprint(aw, "  family\tpasses\tunits\tobserved\tkappa\n")
		for _, a := range r.Agreements {
			fmt.Fprintf(aw, "  %s\t%s vs %s\t%d\t%s\t%s\n", a.Family, a.LabelerA, a.LabelerB, a.Units,
				labels.FormatRate(a.Observed), labels.FormatRate(a.Kappa))
		}
		_ = aw.Flush()
	} else {
		b.WriteString("\nlabel agreement\n  one pass only; agreement is not computable, so the codebook is untested\n")
	}

	fmt.Fprintf(&b, "\nbudget\n  %d regex patterns, %d free parameters (%d fitted)\n",
		r.Budget.Patterns, len(r.Budget.Parameters), fittedCount(r.Budget.Parameters))
	return b.String()
}

// renderConfusion prints the matrix, including the two silences: labeled units
// the build said nothing about, and predicted units nobody judged.
func renderConfusion(s labels.Score) string {
	classes := s.Confusion.Classes()
	if len(classes) == 0 {
		return ""
	}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprint(w, "  labeled \\ predicted")
	for _, c := range classes {
		fmt.Fprintf(w, "\t%s", c)
	}
	fmt.Fprint(w, "\tbuild silent\n")
	for _, l := range classes {
		row, ok := s.Confusion.Cells[l]
		if !ok && s.Confusion.MissedByBuild[l] == 0 {
			continue
		}
		fmt.Fprintf(w, "  %s", l)
		for _, p := range classes {
			fmt.Fprintf(w, "\t%d", row[p])
		}
		fmt.Fprintf(w, "\t%d\n", s.Confusion.MissedByBuild[l])
	}
	if len(s.Confusion.Unlabeled) > 0 {
		keys := make([]string, 0, len(s.Confusion.Unlabeled))
		for k := range s.Confusion.Unlabeled {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s %d", k, s.Confusion.Unlabeled[k]))
		}
		fmt.Fprintf(w, "  (predicted, nobody judged)\t%s\n", strings.Join(parts, ", "))
	}
	_ = w.Flush()
	return b.String()
}

func fittedCount(ps []classify.Parameter) int {
	var n int
	for _, p := range ps {
		if p.Fitted {
			n++
		}
	}
	return n
}
