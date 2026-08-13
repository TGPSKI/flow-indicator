package stream

import (
	"reflect"
	"testing"
)

func TestLinesKeepOffsets(t *testing.T) {
	text := "first\nsecond\n"
	got := Lines(text)
	want := []Line{
		{Text: "first", Start: 0, End: 5},
		{Text: "second", Start: 6, End: 12},
		{Text: "", Start: 13, End: 13},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Lines(%q) = %+v, want %+v", text, got, want)
	}
	if Lines("") != nil {
		t.Error("empty text produced lines")
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Stop after CI passes.": "stop after ci passes",
		"  STOP   after CI  ":   "stop after ci",
		"\"stop after CI\"!":    "stop after ci",
		"":                      "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJaccardHasNoEvidenceForEmptySets(t *testing.T) {
	if got := Jaccard(TokenSet(""), TokenSet("")); got != 0 {
		t.Fatalf("Jaccard of two empty sets = %v, want 0", got)
	}
	a := TokenSet("stop after ci passes")
	if got := Jaccard(a, a); got != 1 {
		t.Fatalf("Jaccard of a set with itself = %v, want 1", got)
	}
	b := TokenSet("stop after ci passes and wait")
	if got := Jaccard(a, b); got != 4.0/6.0 {
		t.Fatalf("Jaccard = %v, want 4/6", got)
	}
}

func TestTokensKeepPathsAndIdentifiers(t *testing.T) {
	got := Tokens("Revert internal/worker/retry.go now")
	want := []string{"revert", "internal/worker/retry.go", "now"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tokens = %v, want %v", got, want)
	}
}

func TestIsOperatorTurn(t *testing.T) {
	human := Record{SpeakerClass: SpeakerHuman, Metadata: Metadata{}}
	if !human.IsOperatorTurn() {
		t.Error("a human record is not counted as an operator turn")
	}
	sidechain := Record{SpeakerClass: SpeakerHuman, Metadata: Metadata{MetaSidechain: "true"}}
	if sidechain.IsOperatorTurn() {
		t.Error("a sidechain human record was counted as operator serialization")
	}
	agent := Record{SpeakerClass: SpeakerAgent, Metadata: Metadata{}}
	if agent.IsOperatorTurn() {
		t.Error("an agent record was counted as an operator turn")
	}
}
