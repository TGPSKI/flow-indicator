package stream

import (
	"strings"
	"unicode"
)

// Line is one physical line of a record's text with its byte offsets into that
// text. Offsets are into Record.Text, not into the source file.
type Line struct {
	Text  string
	Start int
	End   int
}

// Lines splits text on "\n" and keeps byte offsets. The newline itself is not
// part of a line's span. An empty text yields no lines.
func Lines(text string) []Line {
	if text == "" {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i <= len(text); i++ {
		if i == len(text) || text[i] == '\n' {
			out = append(out, Line{Text: text[start:i], Start: start, End: i})
			start = i + 1
		}
	}
	return out
}

// Normalize is the canonical comparison form: case folded, whitespace
// collapsed to single spaces, surrounding punctuation trimmed. It is used for
// exact-repeat detection and obligation keys. It never claims equivalence
// beyond byte equality of this form.
func Normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	})
}

// Tokens splits normalized text into word tokens, dropping punctuation-only
// tokens.
func Tokens(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '/' && r != '.'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, "-./_")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// TokenSet returns the distinct tokens of s.
func TokenSet(s string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, t := range Tokens(s) {
		set[t] = struct{}{}
	}
	return set
}

// Jaccard is |a ∩ b| / |a ∪ b|. Two empty sets return 0: no evidence, not
// perfect agreement.
func Jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if _, ok := b[t]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
