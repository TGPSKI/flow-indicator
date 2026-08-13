// Package labels reads ground truth and aligns it to what a build predicted.
//
// A label is a judgement a person made about a source record. It is never
// produced here, never inferred, and never revised here: this package reads what
// a labeler wrote and lines it up with what the rules said, so the two can be
// counted against each other.
//
// Alignment is by record identifier and byte span rather than by segment index.
// Segmentation is part of what calibration changes, and a label keyed to a
// segment number would move underneath the rule it is meant to judge.
package labels

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Family names the question a label answers. The three families are calibrated
// separately because they have different units and different error costs.
const (
	// FamilyObligation labels one operator sentence: does it place a requirement
	// on the agent, or describe something.
	FamilyObligation = "obligation"
	// FamilyObligationPair labels a sentence against an outstanding candidate.
	FamilyObligationPair = "obligation_pair"
	// FamilyRecovery labels an operator turn against the response cycle before
	// it: did recovery begin here.
	FamilyRecovery = "recovery"
	// FamilyPollution labels one response cycle.
	FamilyPollution = "pollution"
)

// Labels. Unlabelable is a first-class outcome in every family: a cell nobody
// can decide from the record is a fact about the evidence, and forcing it into
// a class would hide that fact inside a score.
const (
	Unlabelable = "unlabelable"

	// Obligation family.
	Directive   = "directive"
	Description = "description"
	// Task is a one-shot instruction: the operator asked for work, and doing
	// the work discharges it. It is separated from Description because the two
	// are not the same finding. A miss on Description is a rule staying quiet
	// where it should; a miss on Task is the family declining to look, and the
	// bulk of what this family never sees is Task.
	Task = "task"

	// Obligation-pair family.
	Repeat     = "repeat"
	Supersedes = "supersedes"
	Releases   = "releases"
	Unrelated  = "unrelated"

	// Recovery family.
	Opens   = "opens"
	Deepens = "deepens"

	// Pollution family.
	Clean    = "clean"
	Polluted = "polluted"
	Failed   = "failed"
	Unknown  = "unknown"
)

// positives are the labels a family counts as the positive class. Precision and
// recall are asymmetric measures and need one; the choice is the class whose
// over-reporting or under-reporting is the failure the family has.
var positives = map[string]map[string]bool{
	FamilyObligation:     {Directive: true},
	FamilyObligationPair: {Repeat: true, Supersedes: true, Releases: true},
	FamilyRecovery:       {Opens: true, Deepens: true},
	FamilyPollution:      {Clean: true, Polluted: true, Failed: true},
}

// Label is one judgement about one unit of one record.
type Label struct {
	Session string `json:"session"`
	// Record is the source record's own identifier, carried through as the
	// canonical record's TurnID.
	Record string `json:"record"`
	Family string `json:"family"`
	// Start and End are byte offsets into the record's text. A zero-length span
	// means the whole record, which is the unit for the families whose unit is a
	// turn or a cycle rather than a sentence.
	Start int    `json:"start"`
	End   int    `json:"end"`
	Label string `json:"label"`
	// Kind is the sub-classification a family asks for beyond the label, such as
	// the kind of requirement a directive states. It is not scored unless a
	// family's scorer asks for it.
	Kind string `json:"kind,omitempty"`
	// Labeler identifies the pass. Two independent passes carry different
	// values, and agreement is computed between them.
	Labeler string `json:"labeler"`
	// Text is the labeled span, carried so a reader can audit a label without
	// the source beside them. It is never compared: the span is the identity.
	Text string `json:"text,omitempty"`
	// Note is the labeler's reason, required for Unlabelable.
	Note string `json:"note,omitempty"`
}

// WholeRecord reports that the label covers the record rather than a span in it.
func (l Label) WholeRecord() bool { return l.End <= l.Start }

// Positive reports whether the label is in its family's positive class.
func (l Label) Positive() bool { return positives[l.Family][l.Label] }

// Set is every label read, with the hash of the bytes it was read from.
type Set struct {
	Labels []Label
	// SHA256 identifies the label set exactly. A calibration run that cannot
	// name the labels it scored against is not evidence.
	SHA256 string
	// Files are the label files read, in the order read.
	Files []string
}

// Load reads every .jsonl file under dir, in sorted order.
//
// The hash covers the file contents in that order, so two runs over the same
// labels agree and a run over edited labels does not.
func Load(dir string) (*Set, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("labels: read %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	set := &Set{}
	h := sha256.New()
	for _, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("labels: read %s: %w", path, err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(raw)
		parsed, err := parse(path, raw)
		if err != nil {
			return nil, err
		}
		set.Labels = append(set.Labels, parsed...)
		set.Files = append(set.Files, name)
	}
	set.SHA256 = hex.EncodeToString(h.Sum(nil))
	return set, nil
}

// parse decodes one label file. A malformed line is an error, not a skip: a
// label silently dropped is ground truth silently changed.
func parse(path string, raw []byte) ([]Label, error) {
	var out []Label
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "//") {
			continue
		}
		var l Label
		if err := json.Unmarshal([]byte(text), &l); err != nil {
			return nil, fmt.Errorf("labels: %s:%d: %w", path, line, err)
		}
		if err := l.validate(); err != nil {
			return nil, fmt.Errorf("labels: %s:%d: %w", path, line, err)
		}
		out = append(out, l)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("labels: scan %s: %w", path, err)
	}
	return out, nil
}

// vocabulary is every label a family accepts. A value outside it is refused at
// load rather than scored: a class the scorer does not know is a class that
// counts as neither right nor wrong, and it would leave the confusion matrix
// quietly short of its own denominator.
var vocabulary = map[string]map[string]bool{
	FamilyObligation:     {Directive: true, Description: true, Task: true, Unlabelable: true},
	FamilyObligationPair: {Repeat: true, Supersedes: true, Releases: true, Unrelated: true, Unlabelable: true},
	FamilyRecovery:       {Opens: true, Deepens: true, Unrelated: true, Unlabelable: true},
	FamilyPollution:      {Clean: true, Polluted: true, Failed: true, Unknown: true, Unlabelable: true},
}

func (l Label) validate() error {
	if l.Record == "" {
		return fmt.Errorf("a label names no record")
	}
	known, ok := vocabulary[l.Family]
	if !ok {
		return fmt.Errorf("unknown family %q", l.Family)
	}
	if l.Label == "" {
		return fmt.Errorf("a label on record %s carries no value", l.Record)
	}
	if !known[l.Label] {
		return fmt.Errorf("family %s has no label %q on record %s", l.Family, l.Label, l.Record)
	}
	if l.Label == Unlabelable && strings.TrimSpace(l.Note) == "" {
		return fmt.Errorf("record %s is marked unlabelable with no reason; the reason is the finding", l.Record)
	}
	if l.Labeler == "" {
		return fmt.Errorf("a label on record %s names no labeler, so agreement cannot be computed", l.Record)
	}
	return nil
}

// Pass returns the labels from one labeling pass, for one family.
func (s *Set) Pass(family, labeler string) []Label {
	var out []Label
	for _, l := range s.Labels {
		if l.Family == family && l.Labeler == labeler {
			out = append(out, l)
		}
	}
	return out
}

// Labelers lists the passes present for a family, sorted.
func (s *Set) Labelers(family string) []string {
	seen := make(map[string]struct{})
	for _, l := range s.Labels {
		if l.Family == family {
			seen[l.Labeler] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Families lists the families present, sorted.
func (s *Set) Families() []string {
	seen := make(map[string]struct{})
	for _, l := range s.Labels {
		seen[l.Family] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Sessions lists the sessions labeled, sorted.
func (s *Set) Sessions() []string {
	seen := make(map[string]struct{})
	for _, l := range s.Labels {
		if l.Session != "" {
			seen[l.Session] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
