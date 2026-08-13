package observe

import "github.com/TGPSKI/flow-indicator/internal/stream"

// overlapPrevious is the token Jaccard against the immediately preceding
// operator turn. With no preceding turn there is no evidence, so it is 0.
func (o *Observer) overlapPrevious(tokens map[string]struct{}) float64 {
	if len(o.history) == 0 {
		return 0
	}
	return stream.Jaccard(tokens, o.history[len(o.history)-1].tokens)
}

// overlapWindow is the token Jaccard against the union of the rolling window.
// It rises when an operator keeps re-sending the same vocabulary.
func (o *Observer) overlapWindow(tokens map[string]struct{}) float64 {
	if len(o.history) == 0 {
		return 0
	}
	union := make(map[string]struct{})
	for _, h := range o.history {
		for t := range h.tokens {
			union[t] = struct{}{}
		}
	}
	return stream.Jaccard(tokens, union)
}
