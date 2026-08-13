package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/TGPSKI/flow-indicator/internal/calibrate"
	"github.com/TGPSKI/flow-indicator/internal/labels"
)

// labelCmd writes the units of one family to stdout for a labeler to fill in.
//
// It prints the source and no verdict. A labeler shown what the build decided
// agrees with the build, and a calibration run against those labels measures the
// agreement rather than the rule.
func labelCmd(args []string) error {
	fs := flag.NewFlagSet("label", flag.ExitOnError)
	manifestPath := fs.String("manifest", "", "corpus manifest")
	session := fs.String("session", "", "session id to emit units for; empty means every session on the split")
	split := fs.String("split", calibrate.SplitDev, "restrict to one side of the split")
	family := fs.String("family", labels.FamilyObligation, "which family's units to emit")
	sample := fs.Int("sample-per-mille", 0, "emit this many operator turns out of every thousand; 0 emits all of them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" {
		return errors.New("label: --manifest is required")
	}
	m, err := calibrate.LoadManifest(*manifestPath)
	if err != nil {
		return err
	}

	selected := m.Select(*split)
	if *session != "" {
		var one []calibrate.ManifestSession
		for _, s := range m.Sessions {
			if s.ID == *session {
				one = append(one, s)
			}
		}
		if len(one) == 0 {
			return fmt.Errorf("label: the manifest holds no session %q", *session)
		}
		selected = one
	}
	if len(selected) == 0 {
		return fmt.Errorf("label: no session on the %s split", *split)
	}

	w := bufio.NewWriter(os.Stdout)
	enc := json.NewEncoder(w)
	var n int
	for _, s := range selected {
		units, err := calibrate.EmitUnits(m, s, *family, calibrate.SampleTurns(*sample))
		if err != nil {
			return err
		}
		for _, u := range units {
			if err := enc.Encode(u); err != nil {
				return err
			}
			n++
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("label: write units: %w", err)
	}
	fmt.Fprintf(os.Stderr, "%d units for family %q across %d session(s)", n, *family, len(selected))
	if *sample > 0 {
		fmt.Fprintf(os.Stderr, ", sampling %d operator turns per thousand", *sample)
	}
	fmt.Fprint(os.Stderr, "; fill in label, labeler and note\n")
	return nil
}
