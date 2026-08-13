package classify

import (
	"context"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// obligationsOf runs the extractor over one operator turn.
func obligationsOf(t *testing.T, text string) []ObligationCandidate {
	t.Helper()
	res, err := Heuristic{}.Classify(context.Background(), Input{
		Turn: stream.Record{SpeakerClass: stream.SpeakerHuman, Text: text},
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	return res.Obligations
}

// The directive/description distinction is what carries this family. Every
// sentence below carries a marker word, so no marker rule separates them; what
// separates them is where the marker sits and who the sentence addresses.
//
// The descriptions are verbatim from the WRECK session, which produced 124
// candidates from 72 operator turns because its subject matter was validation
// and its vocabulary was therefore the marker table's vocabulary.
func TestDirectiveSeparatedFromDescription(t *testing.T) {
	descriptions := []string{
		"`the runner` never sends the provenance challenge",
		"It never produces:",
		"Currently it does not verify that:",
		"An incomplete confirmatory battery should terminate nonzero / `INCOMPLETE`, never look like successful execution.",
	}
	for _, s := range descriptions {
		if got := obligationsOf(t, s); len(got) != 0 {
			t.Errorf("a description became a requirement: %q -> %+v", s, got)
		}
	}

	directives := []string{
		"never touch the release workflow",
		"do not add another test",
		"only edit internal/worker",
		"make sure i dont lose my .config or other tings when i re-install it",
		"please make sure you preserve the config",
		"and never commit to main",
		"you must always run the tests before pushing",
	}
	for _, s := range directives {
		if got := obligationsOf(t, s); len(got) == 0 {
			t.Errorf("a requirement was dropped: %q", s)
		}
	}
}

// A sentence the operator delivered rather than said is not addressed to the
// agent, whatever it contains. The whole-turn case is bulkPasteLines; this is
// the embedded case, and a fenced or quoted requirement inside an ordinary turn
// went straight into the inventory before.
func TestContainedSentenceStatesNoRequirement(t *testing.T) {
	fenced := "Here is what the old spec said:\n\n```\nnever retry the request\n```\n\nI want the opposite.\n"
	for _, c := range obligationsOf(t, fenced) {
		if c.Directive.Contained {
			t.Errorf("a fenced sentence reached the inventory: %q", c.Text)
		}
	}

	quoted := "The doc claims:\n\n> you must never call this twice\n\nThat is out of date.\n"
	for _, c := range obligationsOf(t, quoted) {
		if c.Directive.Contained {
			t.Errorf("a quoted sentence reached the inventory: %q", c.Text)
		}
	}
}

// The rule's known costs, recorded rather than hidden.
//
// Both cases put a subject in front of the marker without addressing the agent,
// so the structural rule reads them as descriptions. Both are real directives.
// Pinning them here means the day either changes, the change is deliberate and
// arrives with the labelled evidence that justified it.
func TestKnownDirectiveMisses(t *testing.T) {
	misses := []struct {
		text string
		why  string
	}{
		{
			"the installer must not touch /opt",
			"a requirement stated in the third person: its subject is the installer, not the agent",
		},
		{
			// The RESCUE turn, whole. Its constraint clause alone is caught;
			// the sentence the operator actually typed is not, because a
			// request precedes the marker and supplies the subject.
			"install this to userspace instaead of opt and make sure i dont lose my .config or other tings when i re-install it",
			"a request and a constraint in one sentence: the request's words stand before the marker",
		},
	}
	for _, m := range misses {
		if got := obligationsOf(t, m.text); len(got) != 0 {
			t.Errorf("a known miss now produces %+v (%s); the cost changed, so record what justified it", got, m.why)
		}
	}
}

// Code density is measured and reported, and it decides nothing. A threshold on
// it would be a fitted parameter with no labelled population to justify its
// value, so the field carries the measurement and Describes never reads it.
func TestCodeDensityIsMeasuredAndUnused(t *testing.T) {
	dense := describeSentence(Heuristic{}.rules(), "always run `make ci` before `git push` in flow-indicator/Makefile", 0, false)
	if dense.CodeDensity <= 0 {
		t.Error("a sentence dense with identifiers measured zero code density")
	}
	if dense.Describes() {
		t.Error("code density changed the verdict; it is meant to be inert until labels justify a threshold")
	}
	sparse := describeSentence(Heuristic{}.rules(), "always run the whole suite before pushing", 0, false)
	if sparse.CodeDensity != 0 {
		t.Errorf("a sentence with no identifiers measured %v code density", sparse.CodeDensity)
	}
}

// Exact keys found no repeat anywhere in the seed corpus, because an operator
// restating a requirement rewords it. Content tokens survive the rewording;
// function words and word order do not.
func TestRepeatKeySurvivesRewording(t *testing.T) {
	same := []string{
		"do not touch the release workflow",
		"please don't touch release workflow",
		"never touch the release workflow",
		"the release workflow: do not touch it",
	}
	first := ContentKey(same[0])
	if first == "" {
		t.Fatal("a requirement with content words produced an empty repeat key")
	}
	for _, s := range same[1:] {
		if got := ContentKey(s); got != first {
			t.Errorf("ContentKey(%q) = %q, want %q", s, got, first)
		}
	}

	// The key has to separate requirements too. A repeat rule that fires loosely
	// inflates repeated_obligations, and that count feeds a regime rule.
	if ContentKey("never touch the release workflow") == ContentKey("never touch the config file") {
		t.Error("two different requirements share a repeat key")
	}

	// Polarity is the distinction a repeat key must never collapse. Dropping
	// negations as function words would make these the same requirement.
	if ContentKey("always run the tests") == ContentKey("never run the tests") {
		t.Error("opposite requirements share a repeat key")
	}

	// A sentence of nothing but function words identifies no requirement, so it
	// matches nothing rather than matching everything.
	if got := ContentKey("do not do that"); got != "" {
		t.Errorf("a requirement made only of function words produced the key %q", got)
	}
}

// The recall the rule does not have, pinned so that gaining it is deliberate.
// An inflected restatement is a different key, because nothing here stems.
func TestRepeatKeyMissesInflection(t *testing.T) {
	if ContentKey("do not touch the release workflow") == ContentKey("the release workflow must not be touched") {
		t.Error("inflected forms now share a key; the rule gained stemming, so record what justified it")
	}
}
