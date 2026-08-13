package calibrate

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/TGPSKI/flow-indicator/internal/harness"
	"github.com/TGPSKI/flow-indicator/internal/labels"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Unit is one thing a labeler judges, carrying the source text and nothing else.
//
// What is deliberately absent is the build's verdict. Labeling by reviewing
// classifier output anchors the labeler on the rule being tested and turns the
// exercise into agreement with the current build, which measures nothing. The
// emitted unit is the record, the span, and the words the operator wrote.
type Unit struct {
	Session string `json:"session"`
	Record  string `json:"record"`
	Family  string `json:"family"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Text    string `json:"text"`
	// Label and Note are left empty for the labeler to fill in. Labeler is left
	// empty so a pass has to name itself.
	Label   string `json:"label"`
	Labeler string `json:"labeler"`
	Note    string `json:"note"`
	// Turn is the operator-turn ordinal, for a labeler working through a session
	// in order. It is not part of a label's identity.
	Turn int `json:"turn"`
	// TurnLines is how many lines the whole operator turn ran to. A sentence
	// lifted out of a hundred-line paste reads like a requirement and is not
	// one: the operator is showing the text, not saying it. The classifier has
	// the same fact and reads it as bulk paste; a labeler judging one sentence
	// on its own cannot see it at all unless the unit carries it.
	TurnLines int `json:"turn_lines,omitempty"`
	// Context is what the record establishes about the response cycle this turn
	// answers. The recovery and pollution families cannot be judged without it:
	// "did this turn correct the previous cycle" is a question about what the
	// previous cycle did.
	//
	// Everything in it is observed — paths written, calls that failed, what the
	// agent said. None of it is the build's verdict. A labeler shown the
	// classifier's answer agrees with the classifier; a labeler shown the
	// evidence judges it.
	Context *TurnContext `json:"context,omitempty"`
}

// TurnContext is the observed evidence about the cycle preceding an operator
// turn, and the one before that.
type TurnContext struct {
	// PriorOperatorText is the operator's previous turn, for judging whether
	// this one restates or corrects it.
	PriorOperatorText string `json:"prior_operator_text,omitempty"`
	// CycleWrites are the paths the agent wrote between the previous operator
	// turn and this one. EarlierWrites are the paths it wrote in the cycle
	// before that: their intersection is the agent re-editing what it had just
	// produced.
	CycleWrites   []string `json:"cycle_writes,omitempty"`
	EarlierWrites []string `json:"earlier_writes,omitempty"`
	// CycleFailedCalls counts tool calls the harness reported as errors.
	CycleFailedCalls int `json:"cycle_failed_calls,omitempty"`
	// CycleInterrupted reports that the operator stopped the agent mid-cycle.
	CycleInterrupted bool `json:"cycle_interrupted,omitempty"`
	// AgentSaid is a bounded excerpt of what the agent said in the cycle.
	AgentSaid string `json:"agent_said,omitempty"`

	// The response fields look forward instead of back: they describe the cycle
	// that FOLLOWED this operator turn. The pollution family asks what the agent
	// did about what this turn said, and a unit carrying only the cycle before
	// it asks about one turn while showing the evidence of another.
	ResponseWrites      []string `json:"response_writes,omitempty"`
	ResponseFailedCalls int      `json:"response_failed_calls,omitempty"`
	ResponseInterrupted bool     `json:"response_interrupted,omitempty"`
	ResponseAgentSaid   string   `json:"response_agent_said,omitempty"`
	// NextOperatorText is what the operator said after that response, bounded.
	// An operator who accepted the work and one who repeated themselves are
	// different outcomes, and neither is visible in the write set.
	NextOperatorText string `json:"next_operator_text,omitempty"`
}

// agentExcerpt bounds the agent text carried as context. A cycle can run to
// tens of thousands of characters and the question the context serves is what
// the agent claimed, which the opening of each record carries.
const agentExcerpt = 600

// SampleTurns selects a fraction of operator turns to label, deterministically.
//
// A whole split runs to five figures of sentences, which nobody labels. The
// sample is drawn over **turns**, not over sentences, for two reasons.
//
// It is the unit a labeler actually works in. Judging whether a turn corrects
// the previous cycle means reading the turn, and once it has been read its
// sentences cost almost nothing to judge as well. One reading pass yields all
// three families.
//
// And it refers to nothing the build decided. Sampling sentences the classifier
// flagged would anchor the labeler on the rule under test; sampling by turn asks
// only which turns exist. Selection is a hash of the record identifier, so the
// same sample comes back every time and nobody chose it.
//
// Zero takes every turn.
func SampleTurns(perMille int) func(recordID string) bool {
	if perMille <= 0 {
		return func(string) bool { return true }
	}
	return func(recordID string) bool {
		sum := sha256.Sum256([]byte(recordID))
		return int(binary.BigEndian.Uint32(sum[:4])%1000) < perMille
	}
}

// EmitUnits produces the units of one family for one session, in source order.
//
// The units for the obligation family are every sentence of every sampled
// operator turn, not only the sentences a marker matched. Emitting only the
// marker-carrying ones would make the marker table the denominator, and a
// requirement no marker caught would be invisible to recall by construction.
func EmitUnits(m *Manifest, s ManifestSession, family string, sample func(string) bool) ([]Unit, error) {
	records, err := harness.OpenPath(s.Adapter, m.Resolve(s), s.Root)
	if err != nil {
		return nil, err
	}

	var out []Unit
	var turn int
	// The cycle accumulators run over every record, sampled or not: the context
	// of a sampled turn is the cycle before it, which is made of records the
	// sample never sees.
	var cycle, earlier []string
	var failed int
	var interrupted bool
	var agentSaid strings.Builder
	var priorOperator string

	// open is the context of the last emitted turn whose response has not been
	// seen yet. The response fields are filled when the next operator turn
	// arrives, which is the only point at which the cycle is known to be over.
	var open *TurnContext
	var respWrites []string
	var respFailed int
	var respInterrupted bool
	var respSaid strings.Builder
	closeResponse := func(next string) {
		if open == nil {
			return
		}
		open.ResponseWrites = dedupe(respWrites)
		open.ResponseFailedCalls = respFailed
		open.ResponseInterrupted = respInterrupted
		open.ResponseAgentSaid = strings.TrimSpace(respSaid.String())
		open.NextOperatorText = excerpt(next, agentExcerpt)
		open, respWrites, respFailed, respInterrupted = nil, nil, 0, false
		respSaid.Reset()
	}

	for _, r := range records {
		if !r.IsOperatorTurn() {
			switch {
			case r.Metadata[stream.MetaInputMode] == stream.InputInterrupt:
				interrupted = true
				respInterrupted = true
			case r.SpeakerClass == stream.SpeakerAgent:
				cycle = append(cycle, r.WriteTargets()...)
				respWrites = append(respWrites, r.WriteTargets()...)
				if agentSaid.Len() < agentExcerpt && r.Text != "" {
					agentSaid.WriteString(excerpt(r.Text, agentExcerpt-agentSaid.Len()))
					agentSaid.WriteString(" ")
				}
				if respSaid.Len() < agentExcerpt && r.Text != "" {
					respSaid.WriteString(excerpt(r.Text, agentExcerpt-respSaid.Len()))
					respSaid.WriteString(" ")
				}
			}
			failed += r.FailedActions()
			respFailed += r.FailedActions()
			continue
		}
		closeResponse(r.Text)
		// The ordinal counts every operator turn, sampled or not, so a labeler
		// working through a session sees where in it they are.
		turn++

		ctx := &TurnContext{
			PriorOperatorText: excerpt(priorOperator, agentExcerpt),
			CycleWrites:       dedupe(cycle),
			EarlierWrites:     dedupe(earlier),
			CycleFailedCalls:  failed,
			CycleInterrupted:  interrupted,
			AgentSaid:         strings.TrimSpace(agentSaid.String()),
		}
		// Roll the cycle window forward before the turn is emitted or skipped.
		earlier, cycle = cycle, nil
		failed, interrupted = 0, false
		agentSaid.Reset()
		priorOperator = r.Text

		if sample != nil && !sample(r.TurnID) {
			continue
		}
		switch family {
		case labels.FamilyObligation:
			lines := strings.Count(r.Text, "\n") + 1
			for _, sp := range sentenceSpans(r.Text) {
				text := strings.TrimSpace(r.Text[sp.start:sp.end])
				if text == "" {
					continue
				}
				out = append(out, Unit{
					Session: s.ID, Record: r.TurnID, Family: family,
					Start: sp.start, End: sp.end, Text: text, Turn: turn,
					TurnLines: lines,
				})
			}
		case labels.FamilyRecovery, labels.FamilyPollution, labels.FamilyObligationPair:
			// The unit is the turn or the cycle it opens, so the span is the
			// whole record.
			out = append(out, Unit{
				Session: s.ID, Record: r.TurnID, Family: family,
				Text: r.Text, Turn: turn, Context: ctx,
			})
			if family == labels.FamilyPollution {
				// Pollution judges what the agent did about this turn, so the
				// unit stays open until that response has happened.
				open = ctx
			}
		default:
			return nil, fmt.Errorf("calibrate: no unit is defined for family %q", family)
		}
	}
	// A session that ends on the agent's side leaves one response with no
	// operator turn after it. The evidence is still complete; what is missing is
	// the operator's reaction, and an empty one says exactly that.
	closeResponse("")
	return out, nil
}

// excerpt bounds a string to n bytes on a rune boundary.
func excerpt(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}

// dedupe keeps first-seen order and drops repeats.
func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// span is a byte range within a record's text.
type span struct{ start, end int }

// sentenceSpans splits an operator turn into labelable spans.
//
// This is the same split the classifier performs, and it is a convenience rather
// than a commitment: the label's identity is its byte span, and alignment is by
// overlap, so a later change to how sentences are found does not invalidate a
// label written against this one. That is the property the alignment golden test
// holds.
func sentenceSpans(text string) []span {
	if text == "" {
		return nil
	}
	var out []span
	start := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\n' {
			out = append(out, span{start, i + 1})
			start = i + 1
			continue
		}
		if c != '.' && c != '!' && c != '?' {
			continue
		}
		j := i + 1
		for j < len(text) && (text[j] == '.' || text[j] == '!' || text[j] == '?' || text[j] == '"' || text[j] == ')') {
			j++
		}
		if j >= len(text) {
			break
		}
		if text[j] != ' ' && text[j] != '\t' && text[j] != '\n' {
			continue
		}
		for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
			j++
		}
		out = append(out, span{start, j})
		start = j
		i = j - 1
	}
	if start < len(text) {
		out = append(out, span{start, len(text)})
	}
	return out
}
