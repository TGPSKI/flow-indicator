// Package observe computes deterministic counts over a record's text.
//
// Everything here is OBSERVED evidence: it is a function of the bytes, with no
// interpretation. Nothing in this package decides what a turn means.
package observe

import (
	"strings"
	"unicode/utf8"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Observation is the per-record observed record. Fields below Lines apply to
// operator turns only; OperatorTurn says whether they were computed.
type Observation struct {
	Bytes int `json:"bytes"`
	Chars int `json:"chars"`
	Words int `json:"words"`
	Lines int `json:"lines"`

	OperatorTurn bool `json:"operator_turn"`

	// Action counts and targets. These are read straight off the record's
	// actions, which the adapter read straight out of the source. They are here
	// so a rule over write sets compares lists the observer already built rather
	// than re-deriving them from the source, and so a stored session carries
	// what the rule saw.
	//
	// Targets are relative to the session root wherever the source named one.
	ActionCount   int      `json:"action_count,omitempty"`
	FailedActions int      `json:"failed_actions,omitempty"`
	PathTargets   []string `json:"path_targets,omitempty"`
	WritePaths    []string `json:"write_paths,omitempty"`

	QuotedChars    int     `json:"quoted_chars"`
	QuotedFraction float64 `json:"quoted_fraction"`
	RepeatChars    int     `json:"repeat_chars"`
	RepeatFraction float64 `json:"repeat_fraction"`

	LexicalOverlapPrevious float64 `json:"lexical_overlap_previous"`
	LexicalOverlapWindow   float64 `json:"lexical_overlap_window"`

	// Gaps are nil when either timestamp is missing. A missing timestamp is
	// unknown, never zero.
	GapSecondsPrevious      *float64 `json:"gap_seconds_previous"`
	GapSecondsPreviousHuman *float64 `json:"gap_seconds_previous_human"`
}

// countChars fills the four size counts.
func countChars(text string, o *Observation) {
	o.Bytes = len(text)
	o.Chars = utf8.RuneCountInString(text)
	o.Words = len(strings.Fields(text))
	if text == "" {
		o.Lines = 0
		return
	}
	o.Lines = 1 + strings.Count(text, "\n")
}

// countActions fills the action counts and target lists. Nothing here decides
// what an action meant: it counts what the adapter read and lists what it named.
func countActions(r stream.Record, o *Observation) {
	if len(r.Actions) == 0 {
		return
	}
	o.ActionCount = len(r.Actions)
	o.FailedActions = r.FailedActions()
	o.PathTargets = r.PathTargets()
	o.WritePaths = r.WriteTargets()
}

// Observer carries the rolling operator-turn history the repetition and
// overlap observations are measured against.
type Observer struct {
	window  int
	history []turnHistory
	last    lastSeen
}

type turnHistory struct {
	tokens map[string]struct{}
	lines  map[string]struct{}
}

// New returns an Observer with a rolling window of the given number of
// operator turns.
func New(window int) *Observer {
	if window < 1 {
		window = 1
	}
	return &Observer{window: window}
}

// Observe returns the observation for r and advances the rolling history.
func (o *Observer) Observe(r stream.Record) Observation {
	var obs Observation
	countChars(r.Text, &obs)
	countActions(r, &obs)
	obs.GapSecondsPrevious, obs.GapSecondsPreviousHuman = o.gaps(r)

	if !r.IsOperatorTurn() {
		return obs
	}
	obs.OperatorTurn = true

	obs.QuotedChars = quotedChars(r.Text)
	obs.QuotedFraction = fraction(obs.QuotedChars, obs.Chars)

	obs.RepeatChars = o.repeatChars(r.Text)
	obs.RepeatFraction = fraction(obs.RepeatChars, obs.Chars)

	tokens := stream.TokenSet(r.Text)
	obs.LexicalOverlapPrevious = o.overlapPrevious(tokens)
	obs.LexicalOverlapWindow = o.overlapWindow(tokens)

	o.push(r.Text, tokens)
	return obs
}

// Reset clears the rolling history. An epoch boundary starts fresh baselines;
// session-global counters live elsewhere.
func (o *Observer) Reset() { o.history = nil }

func (o *Observer) push(text string, tokens map[string]struct{}) {
	o.history = append(o.history, turnHistory{tokens: tokens, lines: normalizedLines(text)})
	if len(o.history) > o.window {
		o.history = o.history[len(o.history)-o.window:]
	}
}

func fraction(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole)
}
