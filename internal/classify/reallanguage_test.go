package classify

import "testing"

// The cases in this file are verbatim operator language from recorded sessions,
// not paraphrases. A marker table that only recognizes tidy prose recognizes
// nothing that happens when control is actually failing: the operator types
// fast, drops apostrophes, and shouts.

func TestRealCorrectionLanguage(t *testing.T) {
	corrections := []string{
		"i did not say push.",
		`"let me test that claim" IM SAYING DELETE YOUR STUPID TESTS`,
		"why are you keeping false positives that fail",
		"when did i say stop. i said stop sucking and questioning everytyhing, and follow the fukcing examples",
	}
	for _, text := range corrections {
		res := classify(t, Input{Turn: operator(text)})
		if !res.Correction.IsCorrection {
			t.Errorf("no correction candidate for %q", text)
		}
	}
}

// Known miss, recorded rather than patched: "i dontt hink you got the righ
// tone" is a correction that no marker in the table finds. A pattern written
// for that one line would fit the typo, not the language. Only a semantic
// classifier closes this class.
func TestTypoCorrectionIsAKnownMiss(t *testing.T) {
	if classify(t, Input{Turn: operator("i dontt hink you got the righ tone")}).Correction.IsCorrection {
		t.Skip("the marker table now finds heavily mistyped corrections; delete this test")
	}
}

// Case and profanity are not the signal. The same sentence in lower case with
// the profanity removed is the same correction.
func TestCorrectionDoesNotDependOnCaseOrProfanity(t *testing.T) {
	loud := classify(t, Input{Turn: operator(`"let me test that claim" IM SAYING DELETE YOUR STUPID TESTS`)})
	quiet := classify(t, Input{Turn: operator(`"let me test that claim" im saying delete the tests`)})
	if !loud.Correction.IsCorrection || !quiet.Correction.IsCorrection {
		t.Fatalf("correction depends on case or profanity: loud=%v quiet=%v",
			loud.Correction.IsCorrection, quiet.Correction.IsCorrection)
	}
}

func TestRealStopLanguage(t *testing.T) {
	stops := []string{
		"once CI is green you must stop. i will give you specific instructions after.",
		"when you are done with the task list, stop.\ndo not perform any follow up actions.\nwait for instructions.",
	}
	for _, text := range stops {
		res := classify(t, Input{Turn: operator(text)})
		if !res.Stop {
			t.Errorf("no stop marker for %q", text)
		}
		if res.Reset {
			t.Errorf("stop language produced a reset for %q", text)
		}
	}
}

// A session transition resets. The noun does not.
func TestResetNeedsASessionTransition(t *testing.T) {
	resets := []string{
		"let's context clear and hand this off to a new session",
		"clear context and start a new session",
	}
	for _, text := range resets {
		if !classify(t, Input{Turn: operator(text)}).Reset {
			t.Errorf("no reset for a session transition: %q", text)
		}
	}

	notResets := []string{
		"write a handoff document",
		"review the handoff",
		"the handoff format is wrong",
		"new session handling is documented here",
		"the handoff doc should describe how a new session picks up the work",
	}
	for _, text := range notResets {
		if classify(t, Input{Turn: operator(text)}).Reset {
			t.Errorf("a lexical occurrence was treated as a state transition: %q", text)
		}
	}
}

// Permission to continue is not a judgement that what came before was right.
func TestContinuationIsNotAcceptance(t *testing.T) {
	for _, text := range []string{"continue", "proceed", "go ahead", "carry on", "keep going"} {
		res := classify(t, Input{Turn: operator(text)})
		if res.Acceptance {
			t.Errorf("%q was treated as acceptance", text)
		}
		if !res.Continuation {
			t.Errorf("%q was not recognized as continuation", text)
		}
	}
	for _, text := range []string{"good", "correct", "lgtm", "that works", "looks good"} {
		if !classify(t, Input{Turn: operator(text)}).Acceptance {
			t.Errorf("%q was not recognized as acceptance", text)
		}
	}
}
