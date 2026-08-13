package observe

import (
	"strings"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// minRepeatLineChars is the shortest normalized line counted as a repeat.
// Below it, agreement is noise: "ok", "yes", "run it" recur in any stream.
const minRepeatLineChars = 8

// repeatChars counts the characters of lines whose normalized form already
// appeared in an earlier operator turn inside the rolling window. This is exact
// repetition of the operator's own serialization, not similarity.
func (o *Observer) repeatChars(text string) int {
	if len(o.history) == 0 {
		return 0
	}
	var repeated int
	for _, line := range stream.Lines(text) {
		norm := stream.Normalize(line.Text)
		if len(norm) < minRepeatLineChars {
			continue
		}
		for _, h := range o.history {
			if _, ok := h.lines[norm]; ok {
				repeated += len([]rune(line.Text))
				break
			}
		}
	}
	return repeated
}

// normalizedLines is the set of normalized lines of a turn, ignoring lines too
// short to carry a repeat claim.
func normalizedLines(text string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, line := range stream.Lines(text) {
		norm := stream.Normalize(line.Text)
		if len(norm) >= minRepeatLineChars {
			set[norm] = struct{}{}
		}
	}
	return set
}

// quotedChars counts characters the operator pasted rather than composed:
// fenced blocks, inline code spans, and quote-prefixed lines. Fence and quote
// markers themselves are counted, since they are part of what was typed.
func quotedChars(text string) int {
	var count int
	inFence := false
	for _, line := range stream.Lines(text) {
		trimmed := strings.TrimSpace(line.Text)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inFence = !inFence
			count += len([]rune(line.Text))
		case inFence:
			count += len([]rune(line.Text))
		case strings.HasPrefix(trimmed, ">"):
			count += len([]rune(line.Text))
		default:
			count += inlineCodeChars(line.Text)
		}
	}
	return count
}

// inlineCodeChars counts characters inside paired single backticks. An unpaired
// backtick opens nothing.
func inlineCodeChars(line string) int {
	runes := []rune(line)
	var count, open int
	inSpan := false
	for i, r := range runes {
		if r != '`' {
			continue
		}
		if !inSpan {
			inSpan, open = true, i
			continue
		}
		count += i - open + 1
		inSpan = false
	}
	return count
}
