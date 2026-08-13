package classify

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/TGPSKI/flow-indicator/internal/profile"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Structural discriminators over one sentence.
//
// The obligation family's defect was a category error: word presence was being
// used to decide a speech act. "`the runner` never sends the provenance challenge"
// describes a system; "never touch the release workflow" places a requirement on
// the agent. Both carry "never", so no list of marker words separates them.
//
// The words that do separate them are closed-class. "never", "must" and "avoid"
// are open-class in effect: the set of ways to state a requirement is unbounded,
// domain-specific, and grows with every new subject matter, which is why a
// session about validation broke the rule. Position, containment and function
// words are a bounded and slowly changing set, and a rule keyed on them does not
// acquire a new failure mode when the subject changes.
//
// Each discriminator below is computed and reported separately, so its own
// contribution can be scored against labels before any of them are combined.

// Directive is the structural evidence about one marker-carrying sentence. It is
// evidence, not a verdict: Directive.Describes applies the shipped combining
// rule, and the fields remain available so each can be scored alone.
type Directive struct {
	// SubjectBeforeMarker reports that something stands between the start of the
	// sentence and the marker that is not a function word. "`the runner` never
	// sends X" has such a subject; "never touch X" does not.
	SubjectBeforeMarker bool `json:"subject_before_marker"`
	// SecondPerson reports that the sentence addresses the agent. Directives are
	// addressed; descriptions of a system are not.
	SecondPerson bool `json:"second_person"`
	// Contained reports that the sentence sits inside a code fence or a block
	// quote. Such a sentence is delivered material, not an address to the agent.
	Contained bool `json:"contained"`
	// CodeDensity is the fraction of the sentence's characters that lie inside
	// backtick spans or path-shaped tokens.
	//
	// It is measured and reported, and it does not participate in Describes. A
	// density rule needs a threshold, and this program admits a constant only
	// where a visible separation between two populations justifies its value. No
	// labelled population exists yet, so a value here would be a fitted
	// parameter wearing a structural argument's clothes.
	CodeDensity float64 `json:"code_density"`
}

// Describes reports that the sentence describes rather than directs.
//
// Two premises, both structural, no free parameter:
//
//   - A sentence inside a fence or a block quote is material the operator
//     delivered. Whatever it says, the operator is not saying it to the agent.
//   - A sentence with a subject before its marker and no address to the agent
//     is about that subject. "It never produces:" reports a behaviour; "never
//     produce that" requires one.
//
// The second premise misses a directive stated in the third person — "the
// installer must not touch /opt" is a requirement whose subject is the
// installer. That is a known cost of a rule with no semantic tier, and it is
// what the labelled scoring is for.
func (d Directive) Describes() bool { return d.describes(nil) }

// describes applies the combining rule under a ruleset's discriminator
// switches. A discriminator an operator turned off contributes nothing; with all
// of them off the rule keeps every marker-carrying sentence, which is the
// pre-structural behaviour and is what "off" should mean.
func (d Directive) describes(rs *profile.Ruleset) bool {
	on := func(name string) bool {
		if rs == nil {
			return true
		}
		return rs.On(name, true)
	}
	if on("contained") && d.Contained {
		return true
	}
	if !on("subject_before_marker") {
		return false
	}
	addressed := on("second_person") && d.SecondPerson
	return d.SubjectBeforeMarker && !addressed
}

// preSubjectWords may stand between the start of a sentence and a directive
// marker without being the sentence's subject: conjunctions, discourse adverbs,
// politeness, modals and auxiliaries, determiners, and the pronouns of the two
// people in the exchange.
//
// The operator's own pronouns are here because a directive is allowed to name
// its speaker and its addressee. A third-person pronoun is not here: "it never
// produces" has a subject, and that subject is not the agent.
//
// This set is closed and English. It is one of the lexical rules Phase 5 lists.
var preSubjectWords = map[string]struct{}{
	// Coordination and discourse.
	"and": {}, "but": {}, "or": {}, "nor": {}, "yet": {}, "so": {},
	"also": {}, "then": {}, "now": {}, "again": {}, "however": {},
	"therefore": {}, "additionally": {}, "further": {}, "furthermore": {},
	"moreover": {}, "meanwhile": {}, "finally": {}, "instead": {},
	"still": {}, "currently": {}, "next": {}, "first": {}, "firstly": {},
	"second": {}, "secondly": {}, "lastly": {}, "otherwise": {}, "besides": {},
	// Politeness.
	"please": {}, "kindly": {},
	// The two people in the exchange.
	"you": {}, "your": {}, "yours": {}, "yourself": {},
	"i": {}, "me": {}, "my": {}, "we": {}, "us": {}, "our": {},
	// Modals and auxiliaries.
	"must": {}, "should": {}, "shall": {}, "will": {}, "would": {},
	"can": {}, "could": {}, "may": {}, "might": {}, "need": {},
	"do": {}, "does": {}, "did": {}, "to": {}, "be": {}, "let": {},
	// Determiners and degree.
	"the": {}, "a": {}, "an": {}, "just": {}, "ever": {}, "not": {},
	"no": {}, "only": {}, "always": {}, "never": {},
}

// describeSentence computes the structural evidence for one sentence, given the
// byte offset within it at which the marker that made it a candidate begins.
func describeSentence(rs *profile.Ruleset, sentence string, markerAt int, contained bool) Directive {
	d := Directive{
		Contained:    contained,
		SecondPerson: rs.Match("second_person", sentence),
		CodeDensity:  codeDensity(rs, sentence),
	}
	if markerAt > 0 {
		for _, t := range stream.Tokens(sentence[:markerAt]) {
			if _, ok := preSubjectWords[t]; !ok {
				d.SubjectBeforeMarker = true
				break
			}
		}
	}
	return d
}

// codeDensity is the fraction of a sentence's characters that lie inside
// backtick spans or path-shaped tokens.
//
// Overlapping matches are counted once: the measure is how much of the sentence
// is code, and a path inside a backtick span is one region, not two.
func codeDensity(rs *profile.Ruleset, sentence string) float64 {
	total := utf8.RuneCountInString(sentence)
	if total == 0 {
		return 0
	}
	covered := make([]bool, len(sentence))
	for _, group := range []string{"code_span", "path_token"} {
		re := rs.Group(group)
		if re == nil {
			continue
		}
		for _, m := range re.FindAllStringIndex(sentence, -1) {
			for i := m[0]; i < m[1]; i++ {
				covered[i] = true
			}
		}
	}
	var n int
	for i, c := range covered {
		// Count runes, not bytes, so the ratio is against the same unit the
		// denominator uses.
		if c && utf8.RuneStart(sentence[i]) {
			n++
		}
	}
	return float64(n) / float64(total)
}

// containedLines reports, per byte offset of text, whether that offset lies
// inside a code fence or a block quote.
//
// A fence opens and closes on a line whose first non-space characters are three
// backticks. An unclosed fence runs to the end of the text: the operator pasted
// something and did not close it, and the material after it is still delivered
// material.
func containedLines(text string) func(offset int) bool {
	type region struct{ start, end int }
	var regions []region
	var fenceStart = -1
	for _, ln := range stream.Lines(text) {
		trimmed := strings.TrimSpace(ln.Text)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			if fenceStart < 0 {
				fenceStart = ln.Start
			} else {
				regions = append(regions, region{fenceStart, ln.End})
				fenceStart = -1
			}
		case fenceStart < 0 && strings.HasPrefix(trimmed, ">"):
			regions = append(regions, region{ln.Start, ln.End})
		}
	}
	if fenceStart >= 0 {
		regions = append(regions, region{fenceStart, len(text)})
	}
	if len(regions) == 0 {
		return func(int) bool { return false }
	}
	return func(offset int) bool {
		for _, r := range regions {
			if offset >= r.start && offset < r.end {
				return true
			}
		}
		return false
	}
}

// closedClass are the English function words dropped when building a repeat
// key. What is left is what the requirement is about.
//
// Exact-key comparison found nothing a real operator writes twice: an operator
// restating a requirement rewords it, and byte equality after normalization
// misses every such restatement. Dropping function words leaves the content
// tokens, and two statements of the same requirement agree on those.
//
// This set is closed and English. It is one of the lexical rules Phase 5 lists.
var closedClass = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "this": {}, "that": {}, "these": {}, "those": {},
	"i": {}, "me": {}, "my": {}, "mine": {}, "we": {}, "us": {}, "our": {}, "ours": {},
	"you": {}, "your": {}, "yours": {}, "it": {}, "its": {}, "he": {}, "she": {},
	"they": {}, "them": {}, "their": {}, "theirs": {}, "there": {}, "here": {},
	"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
	"am": {}, "do": {}, "does": {}, "did": {}, "doing": {}, "done": {},
	"have": {}, "has": {}, "had": {}, "having": {},
	"will": {}, "would": {}, "shall": {}, "should": {}, "can": {}, "could": {},
	"may": {}, "might": {}, "must": {}, "need": {}, "let": {},
	"and": {}, "or": {}, "but": {}, "nor": {}, "so": {}, "yet": {}, "if": {},
	"then": {}, "than": {}, "as": {}, "because": {}, "while": {}, "when": {},
	"where": {}, "which": {}, "who": {}, "whom": {}, "what": {}, "how": {}, "why": {},
	"to": {}, "of": {}, "in": {}, "on": {}, "at": {}, "by": {}, "for": {},
	"with": {}, "from": {}, "into": {}, "onto": {}, "over": {}, "under": {},
	"about": {}, "up": {}, "down": {}, "out": {}, "off": {}, "again": {},
	"any": {}, "all": {}, "some": {}, "each": {}, "every": {},
	"more": {}, "most": {}, "other": {}, "such": {}, "very": {}, "too": {},
	"also": {}, "just": {}, "now": {}, "still": {}, "please": {}, "make": {},
	"sure": {}, "get": {}, "got": {},
}

// negations are the words that flip a requirement's polarity. They are mapped to
// one token rather than dropped.
//
// Dropping them would make "always run the tests" and "never run the tests" the
// same requirement, which is the worst false repeat this rule could produce.
// Keeping them verbatim would make "do not touch X" and "never touch X"
// different requirements, which is a recall miss on the commonest rewording an
// operator performs. Collapsing them keeps the distinction that changes the
// requirement and discards the one that does not.
// The negated auxiliaries appear in their split form as well as whole, because
// tokenization breaks a contraction at the apostrophe: "don't" arrives as "don"
// and "t".
var negations = map[string]struct{}{
	"not": {}, "no": {}, "never": {}, "nor": {}, "without": {},
	"avoid": {}, "cannot": {}, "cant": {}, "dont": {},
	"don": {}, "doesn": {}, "didn": {}, "isn": {}, "aren": {}, "wasn": {},
	"weren": {}, "won": {}, "wouldn": {}, "shouldn": {}, "couldn": {},
	"hasn": {}, "hadn": {}, "haven": {}, "mustn": {}, "shan": {},
}

// negationToken stands for any negation in a repeat key. It is not a word, so
// no sentence can contain it by accident.
const negationToken = "\x00not"

// ContentKey is the comparison key for repeat detection: the sentence's content
// tokens with polarity preserved, deduplicated and sorted.
//
// Sorting discards word order. That is the point: an operator restating a
// requirement reorders it, and a key that preserved order would call the
// restatement a different requirement. It also means two sentences built from
// the same content words in different orders share a key, which is part of the
// false-repeat rate this rule has to be scored on. A repeat rule that fires
// loosely inflates repeated_obligations, and that count feeds a regime rule.
//
// No stemming is performed. "touch" and "touched" are different tokens and
// therefore different keys, which is a recall miss the labelled scoring has to
// measure rather than a defect to paper over with a similarity threshold.
//
// A sentence with no content tokens left returns the empty string, which matches
// nothing. A requirement made entirely of function words is not identifiable.
func ContentKey(s string) string {
	seen := make(map[string]struct{})
	var out []string
	var polarity bool
	for _, t := range stream.Tokens(s) {
		if _, ok := negations[t]; ok {
			polarity = true
			continue
		}
		// A one-character token is the tail of a contraction the tokenizer split
		// at the apostrophe, or an article. Neither identifies a requirement, and
		// keeping them would make "don't touch X" and "do not touch X" different
		// requirements over the letter "t".
		if len(t) < 2 {
			continue
		}
		if _, ok := closedClass[t]; ok {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) == 0 {
		return ""
	}
	sort.Strings(out)
	if polarity {
		out = append([]string{negationToken}, out...)
	}
	return strings.Join(out, " ")
}
