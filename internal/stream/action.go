package stream

import (
	"path/filepath"
	"strings"
)

// ActionVerb is the fixed vocabulary of things an agent can be observed doing.
//
// The vocabulary is small and closed on purpose. Adapters map their own harness
// tool names into it; every rule downstream reads the vocabulary and never a
// tool name. A rule that named a harness's tool would inherit that harness's
// vocabulary the way the marker rules inherit English's, and would stop working
// the moment the same behaviour arrived under a different name.
type ActionVerb string

const (
	// VerbRead is retrieving the content of a named target.
	VerbRead ActionVerb = "read"
	// VerbWrite is changing the content of a named target.
	VerbWrite ActionVerb = "write"
	// VerbExecute is running a command. The targets it touched are not in the
	// record; the argv is.
	VerbExecute ActionVerb = "execute"
	// VerbSearch is looking for targets rather than reading one named target.
	VerbSearch ActionVerb = "search"
	// VerbAsk is putting a question to the operator.
	VerbAsk ActionVerb = "ask"
	// VerbUnknown is a call this adapter does not recognize. It is counted so
	// that coverage is visible, and it is never guessed into another verb: a
	// call mapped to the wrong verb is worse than one mapped to none, because
	// the write set is what pollution and scope violation are decided from.
	VerbUnknown ActionVerb = "unknown"
)

// Action is one thing the agent did, as observed. It is not a classification:
// the adapter read it out of the source and nothing interpreted it.
type Action struct {
	Verb ActionVerb `json:"verb"`
	// Targets are paths, normalized relative to the session root. A path
	// outside the root keeps its absolute form, because being outside the root
	// is the fact a scope rule needs.
	Targets []string `json:"targets,omitempty"`
	// Argv is the command an execute action ran, as the source recorded it. A
	// harness that records a shell string rather than an argument vector yields
	// one element: splitting a shell string into arguments is a parse this
	// program does not perform and would not be able to do correctly.
	Argv []string `json:"argv,omitempty"`
	// Failed reports that the harness reported this call as an error.
	Failed bool `json:"failed,omitempty"`
}

// WriteTargets returns the paths the record's write actions named, deduplicated
// and in first-seen order.
func (r Record) WriteTargets() []string { return targetsOf(r.Actions, VerbWrite) }

// PathTargets returns every path the record's actions named, whatever the verb,
// deduplicated and in first-seen order.
func (r Record) PathTargets() []string { return targetsOf(r.Actions, "") }

// FailedActions counts the record's actions the harness reported as errors.
func (r Record) FailedActions() int {
	var n int
	for _, a := range r.Actions {
		if a.Failed {
			n++
		}
	}
	return n
}

// targetsOf collects the targets of actions with the given verb, or of every
// action when verb is empty.
func targetsOf(actions []Action, verb ActionVerb) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, a := range actions {
		if verb != "" && a.Verb != verb {
			continue
		}
		for _, t := range a.Targets {
			if t == "" {
				continue
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

// NormalizeTarget renders a path relative to the session root.
//
// A path under the root becomes relative to it, so the same file named from two
// sessions of the same project compares equal and no absolute home directory
// reaches the event log. A path outside the root keeps its absolute form: that
// it lies outside is exactly what a scope rule reads, and rewriting it would
// destroy the fact.
//
// An empty root leaves the path alone. Nothing is invented when the source did
// not say where the session was rooted.
func NormalizeTarget(root, path string) string {
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if root == "" || !filepath.IsAbs(path) {
		return path
	}
	root = filepath.Clean(root)
	if path == root {
		return "."
	}
	if rest, ok := strings.CutPrefix(path, root+string(filepath.Separator)); ok {
		return rest
	}
	return path
}
