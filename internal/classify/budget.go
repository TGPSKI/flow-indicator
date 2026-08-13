package classify

import "strconv"

// Budgets the calibration program tracks per iteration.
//
// Both counts are expected to fall, not rise. The direction of the program is
// fewer patterns doing less work, with structure carrying the load, and a
// constant chosen because it maximized a score is a fitted parameter whichever
// argument accompanies it.
//
// Reporting them is what makes the direction checkable. A change that adds a
// pattern has to say why structure could not carry it, and a change that adds a
// parameter has to name the visible separation between two populations that
// justifies its value.

// Parameter is one constant a rule reads that could have been another value.
type Parameter struct {
	Name string `json:"name"`
	// Value is the constant, rendered.
	Value string `json:"value"`
	// Justification is the separation between two populations that fixes the
	// value, or the statement that no such separation has been demonstrated.
	Justification string `json:"justification"`
	// Fitted reports that the value was chosen against a score rather than
	// against a visible separation. A fitted parameter is not forbidden; it is
	// declared, because an undeclared one is how a corpus gets fitted.
	Fitted bool `json:"fitted"`
}

// Budget is the count of what a build spends on tuning surface.
type Budget struct {
	Patterns   int         `json:"regex_patterns"`
	Parameters []Parameter `json:"free_parameters"`
}

// FreeParameters is every constant in the default classifier that a rule reads
// and that could have been another value.
//
// The list is written by hand rather than derived, because a constant becomes a
// free parameter by being read by a rule, not by being a literal. Adding one and
// leaving it out of this list is the failure the list exists to catch, and the
// generalization gate checks the count against the register.
func FreeParameters() []Parameter {
	return []Parameter{
		{
			Name:  "bulk_paste_lines",
			Value: "40",
			Justification: "the gap between two populations a real session produces: typed directives run to tens of lines, " +
				"pasted specifications to hundreds; no threshold inside that gap changes which turns are affected",
		},
		{
			Name:  "near_repeat_threshold",
			Value: "0.80",
			Justification: "no visible separation demonstrated; it bounds a candidate kind that no rule acts on, " +
				"so its value changes what a semantic tier would be offered and nothing this build decides",
			Fitted: true,
		},
		{
			Name:  "release_coverage",
			Value: "0.50",
			Justification: "no visible separation demonstrated; set high on the argument that a release naming no candidate " +
				"this clearly should resolve nothing, since the alternative drops a requirement still in force",
			Fitted: true,
		},
		{
			Name:          "release_shared_tokens",
			Value:         "2",
			Justification: "one shared token is a coincidence at these lengths: \"never mind\" and \"never touch the release workflow\" share \"never\"",
		},
		{
			Name:          "min_obligation_chars",
			Value:         "8",
			Justification: "no visible separation demonstrated; it bounds what is long enough to carry a requirement",
			Fitted:        true,
		},
	}
}

// CurrentBudget is what this build spends under the shipped baseline.
func CurrentBudget() Budget { return BudgetOf(Heuristic{}) }

// BudgetOf is what one classifier spends. An operator overlay that adds marker
// groups shows up here: the budget is a property of the rules in force, not of
// the source tree.
func BudgetOf(h Heuristic) Budget {
	rs := h.rules()
	params := FreeParameters()
	for i := range params {
		if v, ok := rs.Profile().Parameters[params[i].Name]; ok {
			params[i].Value = strconv.FormatFloat(v, 'g', -1, 64)
		}
	}
	return Budget{Patterns: len(patternTable(rs)), Parameters: params}
}

// FittedParameters counts the parameters whose value rests on no demonstrated
// separation. It is the number the program is trying to drive to zero.
func FittedParameters() int {
	var n int
	for _, p := range FreeParameters() {
		if p.Fitted {
			n++
		}
	}
	return n
}
