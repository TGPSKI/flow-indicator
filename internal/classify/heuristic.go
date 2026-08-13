package classify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/TGPSKI/flow-indicator/internal/profile"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// HeuristicVersion is the version of the marker rules below. Changing any
// pattern requires changing this constant: classifications carry it, and a
// stored event must stay attributable to the rules that produced it.
const HeuristicVersion = "5"

// bulkPasteLines is the line count above which an operator turn is read as
// delivered material rather than as an address to the agent's last turn.
//
// Correction markers and the stop marker are claims about what the agent just
// did. Inside a pasted document they are not that claim: one literal "wrong"
// somewhere in six hundred lines of analysis is the document's vocabulary, not
// the operator objecting. Above this many lines those markers stop being
// correction evidence, in both tiers. The tier-two case needs the rule more
// than tier one does, because its corroboration is that the turn repeats an
// unresolved obligation, and a paste that re-sends earlier text repeats one by
// construction. Everything else the turn produces is unaffected, because
// obligations and references are about content and the operator did deliver
// the content.
//
// The value sits in the gap between the two populations a real session
// produces: typed directives run to tens of lines, pasted specifications to
// hundreds. It is not a tuning dial, and no threshold in that gap changes
// which turns are affected.
const bulkPasteLines = 40

// nearRepeatThreshold is the token Jaccard at which a differently worded
// obligation becomes a near-repeat candidate. A candidate is not a repeat. Only
// a semantic classifier may promote it.
const nearRepeatThreshold = 0.80

// releaseCoverage is the fraction of an unresolved candidate's tokens that a
// release sentence must carry before the release is read as being about that
// candidate.
//
// Coverage, not Jaccard: the release sentence says more than the requirement
// did — it carries the release marker too — so the symmetric measure would
// punish the extra words the marker contributes. What matters is how much of
// the requirement the operator restated while withdrawing it.
//
// The bar is high on purpose. A release that names no candidate this clearly
// resolves nothing, because the alternative is removing a requirement that is
// still in force on the strength of an incidental shared word.
const releaseCoverage = 0.5

// releaseSharedTokens is the least number of tokens a release sentence and a
// candidate must share. One shared token is a coincidence at these lengths;
// "never mind" and "never touch the release workflow" share "never".
const releaseSharedTokens = 2

// The marker patterns are no longer here. They live in internal/profile, as the
// shipped baseline an operator may overlay, because which words a person reaches
// for when they correct or constrain is the part of these rules that varies by
// person, team, language and domain. The structural rules stay in code and carry
// no dial.

// defaultRules is the compiled shipped baseline, used by a Heuristic that names
// no ruleset of its own. Compiling it is a build-time fact: a baseline that does
// not compile is a defect in this program, not a condition to recover from.
var defaultRules = func() *profile.Ruleset {
	p, err := profile.Baseline()
	if err != nil {
		panic("classify: shipped baseline profile does not parse: " + err.Error())
	}
	rs, err := profile.Compile(p)
	if err != nil {
		panic("classify: shipped baseline profile does not compile: " + err.Error())
	}
	return rs
}()

// patternTable is every marker group the ruleset carries, in a fixed order. It
// is the subject of both the rule hash and the regex budget, so the count the
// program tracks and the fingerprint it is attributed to cannot disagree.
func patternTable(rs *profile.Ruleset) []string {
	names := rs.Names()
	sort.Strings(names)
	return names
}

// rulesHash fingerprints the rules behind a classification. It covers the whole
// profile — every pattern, parameter and discriminator — so two builds that
// disagree anywhere produce different hashes even if the version constant was
// not bumped, and an operator overlay is visibly a different ruleset.
func rulesHash(rs *profile.Ruleset) string {
	h := sha256.New()
	h.Write([]byte(HeuristicVersion))
	h.Write([]byte{0})
	h.Write([]byte(rs.Profile().Fingerprint()))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Heuristic is the deterministic marker classifier. It produces candidates.
//
// Rules is the compiled profile it reads. A zero Heuristic uses the shipped
// baseline, so every existing caller keeps working and a build with no
// configuration still classifies.
type Heuristic struct {
	Rules *profile.Ruleset
}

// rules returns the ruleset this classifier reads.
func (h Heuristic) rules() *profile.Ruleset {
	if h.Rules != nil {
		return h.Rules
	}
	return defaultRules
}

func (Heuristic) Name() string    { return "heuristic" }
func (Heuristic) Version() string { return HeuristicVersion }

// Hash covers the profile as well as the version, so a classification made under
// an operator overlay is never mistaken for one made under the baseline.
func (h Heuristic) Hash() string { return rulesHash(h.rules()) }

// Profile names the ruleset behind this classifier, for the run record.
func (h Heuristic) Profile() *profile.Profile { return h.rules().Profile() }

// Capabilities is release evidence and verified repair.
//
// Release language is literal text, so a marker rule reaches it.
//
// Verified repair is reachable because it stopped being a question about agent
// prose. "Was the target repaired" is answered by a write to the path the
// operator named, which the source records structurally. No marker asserts it
// and the agent's own account of its work is not consulted.
//
// Obligation satisfaction is still out of reach. It asks whether the agent's
// work met a requirement, and a requirement is not a path.
func (Heuristic) Capabilities() Capabilities {
	return Capabilities{CapObligationRelease, CapVerifiedRepair}
}

// Classify labels one record. Operator turns get segments, obligations,
// pointer and correction candidacy; agent turns get repair signals; everything
// else gets an empty result with provenance.
func (h Heuristic) Classify(_ context.Context, in Input) (Result, error) {
	res := Result{
		Provenance: Provenance{
			Classifier: h.Name(),
			Version:    h.Version(),
			Hash:       h.Hash(),
			SourceTurn: in.Turn.Seq,
		},
		Pointer:    Pointer{Type: PointerUnknown},
		Correction: Correction{TargetType: "unknown"},
	}

	switch {
	case in.Turn.IsOperatorTurn():
		h.classifyOperator(in, &res)
		res.Confidence = 0.6
	case in.Turn.SpeakerClass == stream.SpeakerAgent:
		h.classifyAgent(in, &res)
		res.Confidence = 0.5
	default:
		res.Confidence = 0
	}
	res.Provenance.Confidence = res.Confidence
	return res, nil
}

func (h Heuristic) classifyOperator(in Input, res *Result) {
	rs := h.rules()
	text := in.Turn.Text
	priorLines := priorLineSet(in.PriorOperatorText)

	bulk := strings.Count(text, "\n")+1 > int(rs.Param("bulk_paste_lines", bulkPasteLines))

	res.Reset = rs.Match("reset", text)
	res.Stop = !bulk && rs.Match("stop", text)
	res.Acceptance = firstSentenceMatches(rs, "acceptance", text)
	res.Continuation = firstSentenceMatches(rs, "continuation", text)

	res.Obligations, res.Rejected = extractObligations(rs, text)
	res.NearRepeats = nearRepeats(rs, res.Obligations, in.UnresolvedObligations)
	res.Pointer = extractPointer(rs, text)

	// A release withdraws a requirement, which is a claim about the exchange and
	// not about content, so the bulk-paste guard covers it for the same reason
	// it covers the correction markers.
	if !bulk {
		res.Resolutions = releases(rs, text, in.UnresolvedObligations)
	}

	// Neither tier of correction marker is evidence inside a pasted document.
	// The tier-two case needs the guard more than tier one does, not less: its
	// corroboration is that the turn repeats an unresolved obligation, and a
	// paste that re-sends earlier text repeats one by construction. In
	// dec3dde2 the operator pasted the same 495-line server log twice; the word
	// "again" inside it opened two episodes and put the meter in THRASH over a
	// session that ended "good session, see you next time".
	repeatsUnresolved := !bulk && repeatsUnresolvedObligation(res.Obligations, in.UnresolvedObligations)
	tier1 := rs.Find("correction_tier1", text)
	if bulk {
		tier1 = ""
	}
	switch {
	case tier1 != "":
		res.Correction = Correction{IsCorrection: true, TargetType: correctionTarget(res.Pointer), Marker: strings.ToLower(tier1)}
	case repeatsUnresolved && rs.Match("correction_tier2", text):
		res.Correction = Correction{IsCorrection: true, TargetType: correctionTarget(res.Pointer), Marker: strings.ToLower(rs.Find("correction_tier2", text))}
	default:
		res.Correction = Correction{TargetType: "unknown"}
	}
	if res.Correction.IsCorrection {
		res.Correction.TargetKey = correctionKey(res.Obligations, res.Pointer)
		res.Correction.TargetPaths = namedPaths(rs, text)
	}

	res.Segments = segment(rs, text, priorLines, in.ActiveRepair != nil)
}

func (h Heuristic) classifyAgent(in Input, res *Result) {
	rs := h.rules()
	// What the agent did is read before what it said, because the two answer
	// different questions and only one of them is evidence about repair.
	h.repairFromActions(in, res)

	text := in.Turn.Text
	if text == "" {
		return
	}
	res.Repair.NewTasks = boolToInt(rs.Match("expansion_task", text))
	res.Repair.NewValidation = boolToInt(rs.Match("expansion_validation", text))
	res.Repair.NewConstraints = boolToInt(rs.Match("expansion_constraint", text))
	// The prose expansion marker still counts, but only where the record
	// carried no actions. Where it did, the write set already answered the
	// question exactly and the phrase adds a guess to a fact.
	if len(in.Turn.Actions) == 0 {
		res.Repair.NewScope = boolToInt(rs.Match("expansion_scope", text))
	}
	// The repair claim is only meaningful while an episode is open. Outside
	// one, an agent saying "reverted" answers no correction.
	//
	// The claim is recorded as a claim, and it stays separate from
	// TargetRepaired. "Removed it." is the agent describing its own work; a
	// write to the path the operator named is the work itself.
	if in.ActiveRepair != nil && rs.Match("repair_verb", text) {
		res.Repair.ClaimedRepaired = true
	}
}

// repairFromActions decides verified repair and expansion from what the record
// establishes the agent did.
//
// Both are exact, with no threshold and no phrase list:
//
//   - the target was repaired when some write action named a path the
//     correction named;
//   - the response expanded scope when the write set holds a path the
//     correction did not name.
//
// Both need a target. A correction that named no path leaves TargetRepaired nil
// and reports no expansion: without a named target there is nothing to be inside
// or outside of, and a write set on its own says only that the agent worked.
func (h Heuristic) repairFromActions(in Input, res *Result) {
	if in.ActiveRepair == nil || len(in.ActiveRepair.TargetPaths) == 0 {
		return
	}
	writes := in.Turn.WriteTargets()
	if len(writes) == 0 {
		return
	}
	named := make(map[string]struct{}, len(in.ActiveRepair.TargetPaths))
	for _, p := range in.ActiveRepair.TargetPaths {
		named[p] = struct{}{}
	}
	var hit, outside bool
	for _, w := range writes {
		if _, ok := named[w]; ok {
			hit = true
		} else {
			outside = true
		}
	}
	if hit {
		repaired := true
		res.Repair.TargetRepaired = &repaired
	}
	if outside {
		res.Repair.NewScope = 1
	}
}

// namedPaths returns the paths a turn's text named, in first-seen order.
//
// The pattern is the one the file pointer already uses, applied to every match
// rather than the first. A correction naming three files has three targets, and
// reading only the first would call two of them expansion.
func namedPaths(rs *profile.Ruleset, text string) []string {
	matches := rs.FindAll("pointer_file", text)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	return out
}

// segment splits text into sentence-sized spans that tile the whole text and
// labels each by first matching rule. Precedence is fixed: reset, correction,
// restatement, stop, scope, negative, positive, meta, referent, temporal,
// reconstruction, evidence, forward, other.
func segment(rs *profile.Ruleset, text string, priorLines map[string]struct{}, repairOpen bool) []Segment {
	spans := sentences(text)
	out := make([]Segment, 0, len(spans))
	for _, sp := range spans {
		s := strings.TrimSpace(text[sp.start:sp.end])
		if s == "" {
			continue
		}
		out = append(out, Segment{
			Label: labelOf(rs, s, priorLines, repairOpen),
			Start: sp.start,
			End:   sp.end,
			Chars: utf8.RuneCountInString(text[sp.start:sp.end]),
		})
	}
	return out
}

func labelOf(rs *profile.Ruleset, s string, priorLines map[string]struct{}, repairOpen bool) SegmentLabel {
	switch {
	case rs.Match("reset", s):
		if repairOpen {
			return LabelHandoffAfterFailure
		}
		return LabelHandoff
	case rs.Match("correction_tier1", s):
		return LabelCorrection
	case isRestatement(s, priorLines):
		return LabelRestatedPriorState
	case rs.Match("stop", s):
		return LabelStopCondition
	case rs.Match("scope", s):
		return LabelScopeConstraint
	case rs.Match("negative", s):
		return LabelNegativeConstraint
	case rs.Match("positive", s):
		return LabelPositiveConstraint
	case rs.Match("meta", s):
		return LabelMetaProcess
	case rs.Match("new_task_line", s):
		return LabelNewTask
	case rs.Match("referent", s):
		return LabelReferentDisambiguation
	case rs.Match("reconstruction", s):
		return LabelRestartReconstruction
	case rs.Match("temporal", s):
		return LabelTemporalDisambiguation
	case rs.Match("evidence", s):
		return LabelNewEvidence
	case rs.Match("forward", s):
		return LabelForwardWork
	default:
		return LabelOther
	}
}

// isRestatement reports exact repetition of an earlier operator sentence after
// normalization. Similarity is not repetition.
func isRestatement(s string, priorLines map[string]struct{}) bool {
	norm := stream.Normalize(s)
	if len(norm) < minObligationChars {
		return false
	}
	_, ok := priorLines[norm]
	return ok
}

// minObligationChars is the shortest normalized sentence that can carry an
// obligation or a restatement claim.
const minObligationChars = 8

type span struct{ start, end int }

// sentences splits text into spans that tile it completely. Boundaries are
// newlines and sentence-final punctuation followed by whitespace. Trailing
// whitespace belongs to the span it follows, so segment offsets cover every
// byte of the source text.
func sentences(text string) []span {
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

// extractObligations returns one candidate per sentence that states a
// requirement on the agent.
//
// A marker makes a sentence a candidate; structure decides whether it is one. A
// sentence whose structure says it describes rather than directs is dropped
// here, so it never reaches the inventory. That is the whole of the fix for the
// over-firing this family had: the marker table was answering a question about
// speech acts with a question about vocabulary.
//
// The key stays the normalized sentence, which is identity. RepeatKey carries
// the content tokens, which is what a restatement of the same requirement shares.
func extractObligations(rs *profile.Ruleset, text string) (kept, rejected []ObligationCandidate) {
	inside := containedLines(text)
	for _, sp := range sentences(text) {
		raw := strings.TrimSpace(text[sp.start:sp.end])
		norm := stream.Normalize(raw)
		if len(norm) < int(rs.Param("min_obligation_chars", minObligationChars)) {
			continue
		}
		// A sentence that withdraws a requirement does not state one. Without
		// this, "you can drop the scope rule" introduces a scope obligation on
		// the word "scope" and the inventory grows on the turn meant to shrink
		// it.
		if rs.Match("release", raw) {
			continue
		}
		kind, markerAt := obligationKind(rs, raw)
		if kind == "" {
			continue
		}
		c := ObligationCandidate{
			Key:       norm,
			Kind:      kind,
			Text:      raw,
			RepeatKey: ContentKey(raw),
			Directive: describeSentence(rs, raw, markerAt, inside(sp.start)),
			Start:     sp.start,
			End:       sp.end,
		}
		if c.Directive.describes(rs) {
			rejected = append(rejected, c)
			continue
		}
		kept = append(kept, c)
	}
	return kept, rejected
}

// obligationKind names the requirement a sentence states, and the byte offset of
// the earliest marker in it.
//
// The two answer different questions and are taken differently. Which kind of
// requirement it is follows the fixed precedence below. Where the marker sits is
// the earliest marker of any kind, because the position discriminator asks
// whether the sentence opens on a directive, and a sentence that opens with
// "make sure" is directive-shaped whatever other marker appears later in it.
//
// Taking the position from the precedence-first kind instead read "make sure i
// dont lose my .config" off the "dont" in the middle, found "make sure i" before
// it, and dropped the requirement as a description. That sentence is the one the
// operator typed in the session they named RESCUE.
func obligationKind(rs *profile.Ruleset, s string) (kind string, markerAt int) {
	markerAt = -1
	for _, probe := range []struct {
		group string
		kind  string
	}{
		{"stop", ObligationStop},
		{"scope", ObligationScope},
		{"negative", ObligationNegative},
		{"positive", ObligationPositive},
	} {
		m := rs.FindIndex(probe.group, s)
		if m == nil {
			continue
		}
		if kind == "" {
			kind = probe.kind
		}
		if markerAt < 0 || m[0] < markerAt {
			markerAt = m[0]
		}
	}
	if markerAt < 0 {
		markerAt = 0
	}
	return kind, markerAt
}

// releases reports the unresolved candidates a turn withdrew.
//
// A release marker says a requirement was dropped; it does not say which one.
// The binding is the rest of the sentence: the operator who withdraws a
// requirement restates enough of it to be understood, and that restatement is
// what identifies the candidate. A sentence carrying the marker and nothing
// else — "never mind" — names no candidate and releases none, which is the
// right answer rather than a missed one.
//
// Ambiguity resolves to nothing. Two candidates fitting a release equally means
// the turn did not say which, and picking either would drop a requirement that
// is still in force.
func releases(rs *profile.Ruleset, text string, unresolved []ObligationRef) []ObligationResolution {
	var out []ObligationResolution
	claimed := make(map[string]bool)
	for _, sp := range sentences(text) {
		raw := strings.TrimSpace(text[sp.start:sp.end])
		marker := rs.Find("release", raw)
		if marker == "" {
			continue
		}
		// The marker's own words carry no information about which requirement
		// is meant. "never mind" shares "never" with "never touch the release
		// workflow" and says nothing whatever about it.
		tokens := stream.TokenSet(strings.Replace(raw, marker, " ", 1))

		var best *ObligationRef
		var bestCover float64
		var ambiguous bool
		for i := range unresolved {
			a := &unresolved[i]
			if claimed[a.Key] {
				continue
			}
			cover, shared := tokenCoverage(tokens, stream.TokenSet(a.Key))
			if shared < int(rs.Param("release_shared_tokens", releaseSharedTokens)) || cover < rs.Param("release_coverage", releaseCoverage) {
				continue
			}
			switch {
			case best == nil || cover > bestCover:
				best, bestCover, ambiguous = a, cover, false
			case cover == bestCover:
				ambiguous = true
			}
		}
		if best == nil || ambiguous {
			continue
		}
		claimed[best.Key] = true
		out = append(out, ObligationResolution{
			Key:  best.Key,
			Kind: ResolutionReleased,
			Evidence: fmt.Sprintf(
				"the operator wrote %q in a sentence restating %d%% of this candidate's tokens, and no other unresolved candidate fits it as well",
				strings.ToLower(marker), int(bestCover*100)),
		})
	}
	return out
}

// tokenCoverage is the fraction of want's tokens that appear in have, with the
// count of shared tokens.
//
// Coverage rather than Jaccard: the two sets are not symmetric here. A release
// sentence says everything the requirement said plus the words that withdraw
// it, and the symmetric measure would penalize exactly those extra words.
func tokenCoverage(have, want map[string]struct{}) (float64, int) {
	if len(want) == 0 {
		return 0, 0
	}
	var shared int
	for t := range want {
		if _, ok := have[t]; ok {
			shared++
		}
	}
	return float64(shared) / float64(len(want)), shared
}

// nearRepeats reports obligations that resemble an unresolved candidate without
// being recognized as a repeat of it. These are candidates for a semantic
// classifier, never repeats.
//
// A pair the content-token key already matched is excluded: it is a repeat, and
// the surface records it as one. What is left here is the residue the structural
// rule did not reach, which is the population a semantic tier would have to earn
// its place on.
func nearRepeats(rs *profile.Ruleset, candidates []ObligationCandidate, unresolved []ObligationRef) []NearRepeat {
	var out []NearRepeat
	for _, c := range candidates {
		ct := stream.TokenSet(c.Key)
		for _, a := range unresolved {
			if a.Key == c.Key || repeatMatch(c, a) {
				continue
			}
			j := stream.Jaccard(ct, stream.TokenSet(a.Key))
			if j >= rs.Param("near_repeat_threshold", nearRepeatThreshold) {
				out = append(out, NearRepeat{Key: c.Key, PriorKey: a.Key, Jaccard: j, Text: c.Text})
			}
		}
	}
	return out
}

// repeatMatch reports that a candidate restates an unresolved one, by identity
// or by content tokens. It is the same comparison the obligation surface makes,
// kept in one place so the classifier and the projector cannot disagree about
// what counts as a repeat.
func repeatMatch(c ObligationCandidate, a ObligationRef) bool {
	if a.Key == c.Key {
		return true
	}
	return c.RepeatKey != "" && c.RepeatKey == a.RepeatKey
}

func repeatsUnresolvedObligation(candidates []ObligationCandidate, unresolved []ObligationRef) bool {
	for _, c := range candidates {
		for _, a := range unresolved {
			if repeatMatch(c, a) {
				return true
			}
		}
	}
	return false
}

// extractPointer returns the first compact reference in the turn. Pointer types
// are checked most specific first.
func extractPointer(rs *profile.Ruleset, text string) Pointer {
	type probe struct {
		group string
		t     PointerType
	}
	for _, p := range []probe{
		{"pointer_task", PointerTask},
		{"pointer_file", PointerFile},
		{"pointer_namespace", PointerNamespace},
		{"pointer_operation", PointerOperation},
		{"pointer_quote", PointerQuote},
		{"pointer_alias", PointerAlias},
	} {
		if m := rs.Find(p.group, text); m != "" {
			return Pointer{IsPointer: true, Type: p.t, Chars: utf8.RuneCountInString(m), Text: m}
		}
	}
	return Pointer{Type: PointerUnknown}
}

func correctionTarget(p Pointer) string {
	if p.IsPointer {
		return string(p.Type)
	}
	return "unknown"
}

// correctionKey names what a correction is about, preferring a repeated
// obligation over the pointer text. It is used for recurrence comparison, which
// reports unknown when the key is empty.
func correctionKey(obligations []ObligationCandidate, p Pointer) string {
	for _, o := range obligations {
		if o.Kind == ObligationStop || o.Kind == ObligationScope {
			return o.Key
		}
	}
	if p.IsPointer {
		return stream.Normalize(p.Text)
	}
	return ""
}

// priorLineSet is the set of normalized sentences the operator has already
// sent, used for restatement detection.
func priorLineSet(prior []string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, t := range prior {
		for _, sp := range sentences(t) {
			norm := stream.Normalize(t[sp.start:sp.end])
			if len(norm) >= minObligationChars {
				set[norm] = struct{}{}
			}
		}
	}
	return set
}

// firstSentenceMatches limits acceptance markers to the opening sentence:
// "good" in the middle of a correction is not acceptance.
func firstSentenceMatches(rs *profile.Ruleset, group, text string) bool {
	spans := sentences(text)
	if len(spans) == 0 {
		return false
	}
	return rs.Match(group, strings.TrimSpace(text[spans[0].start:spans[0].end]))
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
