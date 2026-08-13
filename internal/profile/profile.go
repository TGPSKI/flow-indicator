// Package profile holds the operator-variable part of the classified families.
//
// The families are made of two different kinds of rule, and only one of them
// should ever be tuned.
//
// The structural rules are universal. Whether a sentence has a subject before
// its marker, whether it sits inside a fence, whether the agent re-edited a path
// it had just written, whether a write landed outside what the correction named
// — none of that is about a person or a project. It stays in code, it carries no
// dial, and an operator does not get to change it.
//
// The lexicon is not universal. Which words an operator reaches for when they
// correct, stop, or constrain varies by person, by team, by language and by
// domain. That is what a profile holds, and it is the only thing a profile
// holds.
//
// Profiles layer. A baseline ships in the binary and is meant to work reasonably
// for anyone; an operator overlay names what is different about them and is
// merged over it. The overlay adds rather than replaces by default, because an
// operator who wants their own phrase recognized almost never wants the common
// ones forgotten.
package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// MarkerGroup is one named set of surface forms.
//
// Three ways to state one, in increasing order of power and decreasing order of
// safety. An operator writing an overlay should reach for Phrases; Pattern is
// there because a few of the baseline groups genuinely need position and
// character-class logic that a phrase list cannot express.
type MarkerGroup struct {
	// Phrases match anywhere in the text, on word boundaries, case-insensitively.
	Phrases []string `json:"phrases,omitempty"`
	// Anchored match only at the start of the text. "no" opening a turn is a
	// rejection; "no" inside a sentence is usually a word.
	Anchored []string `json:"anchored,omitempty"`
	// Pattern is a raw regular expression, used where position or character
	// classes matter. It is unioned with the phrases.
	Pattern string `json:"pattern,omitempty"`
	// CaseSensitive keeps the group's matching case-sensitive. Almost no marker
	// wants this; the ones that do are matching identifier shapes rather than
	// words, where upper case is the signal.
	CaseSensitive bool `json:"case_sensitive,omitempty"`
	// Replace makes an overlay's phrases stand in place of the baseline's
	// instead of adding to them. The default is to add: an operator naming
	// their own way of correcting rarely wants the common ways forgotten.
	Replace bool `json:"replace,omitempty"`
}

// Profile is one layer of the operator-variable rules.
type Profile struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	// Language names what the lexicon is written in. Every marker rule in this
	// program is language-specific, and a profile is where that stops being
	// implicit.
	Language string `json:"language,omitempty"`

	Markers map[string]MarkerGroup `json:"markers,omitempty"`
	// Parameters are the constants a rule reads. Each one is declared in
	// classify.FreeParameters with what justifies its value.
	Parameters map[string]float64 `json:"parameters,omitempty"`
	// Discriminators switch the structural tests on and off. They are here, not
	// in the markers, because whether a test runs is an operator decision even
	// though how it works is not.
	Discriminators map[string]bool `json:"discriminators,omitempty"`
}

// Merge layers an overlay over a base and returns the result. Neither input is
// modified.
//
// Markers add by default and replace on request. Parameters and discriminators
// override per key: a number has no union, and an operator naming one means
// that number.
func Merge(base, overlay *Profile) *Profile {
	out := &Profile{
		Name:           base.Name,
		Version:        base.Version,
		Description:    base.Description,
		Language:       base.Language,
		Markers:        map[string]MarkerGroup{},
		Parameters:     map[string]float64{},
		Discriminators: map[string]bool{},
	}
	for k, v := range base.Markers {
		out.Markers[k] = v
	}
	for k, v := range base.Parameters {
		out.Parameters[k] = v
	}
	for k, v := range base.Discriminators {
		out.Discriminators[k] = v
	}
	if overlay == nil {
		return out
	}
	if overlay.Name != "" {
		out.Name = base.Name + "+" + overlay.Name
	}
	if overlay.Language != "" {
		out.Language = overlay.Language
	}
	for k, add := range overlay.Markers {
		cur, ok := out.Markers[k]
		if !ok || add.Replace {
			add.Replace = false
			out.Markers[k] = add
			continue
		}
		cur.CaseSensitive = cur.CaseSensitive && add.CaseSensitive
		cur.Phrases = union(cur.Phrases, add.Phrases)
		cur.Anchored = union(cur.Anchored, add.Anchored)
		if add.Pattern != "" {
			if cur.Pattern == "" {
				cur.Pattern = add.Pattern
			} else {
				cur.Pattern = "(?:" + cur.Pattern + ")|(?:" + add.Pattern + ")"
			}
		}
		out.Markers[k] = cur
	}
	for k, v := range overlay.Parameters {
		out.Parameters[k] = v
	}
	for k, v := range overlay.Discriminators {
		out.Discriminators[k] = v
	}
	return out
}

// Compile turns a marker group into one regular expression.
//
// An empty group compiles to nil rather than to a pattern that matches
// everything or nothing by accident. A caller holding a nil pattern treats the
// rule as not firing, which is the honest reading of a group an operator
// emptied.
func (g MarkerGroup) Compile() (*regexp.Regexp, error) {
	var parts []string
	if g.Pattern != "" {
		parts = append(parts, g.Pattern)
	}
	for _, p := range g.Phrases {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, `\b`+regexp.QuoteMeta(p)+`\b`)
		}
	}
	for _, p := range g.Anchored {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, `^`+regexp.QuoteMeta(p)+`\b`)
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	flags := "(?i)"
	if g.CaseSensitive {
		flags = ""
	}
	re, err := regexp.Compile(flags + strings.Join(parts, "|"))
	if err != nil {
		return nil, fmt.Errorf("profile: marker group does not compile: %w", err)
	}
	return re, nil
}

// Load reads a profile from a file.
func Load(path string) (*Profile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("profile: read %s: %w", path, err)
	}
	return Parse(raw, path)
}

// Parse decodes a profile. Unknown keys are an error: a misspelled marker group
// in an overlay would otherwise be accepted and silently do nothing, which is
// the worst outcome available — the operator believes they changed a rule.
func Parse(raw []byte, name string) (*Profile, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var p Profile
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("profile: decode %s: %w", name, err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("profile: %s names no profile", name)
	}
	for group, g := range p.Markers {
		if _, err := g.Compile(); err != nil {
			return nil, fmt.Errorf("profile: %s: group %q: %w", name, group, err)
		}
	}
	return &p, nil
}

// Fingerprint identifies a profile's content exactly, so a classification can be
// attributed to the rules that produced it. Two profiles that differ anywhere
// produce different fingerprints.
func (p *Profile) Fingerprint() string {
	groups := make([]string, 0, len(p.Markers))
	for k := range p.Markers {
		groups = append(groups, k)
	}
	sort.Strings(groups)

	var b strings.Builder
	b.WriteString(p.Name)
	b.WriteString("\x00")
	b.WriteString(p.Version)
	for _, k := range groups {
		g := p.Markers[k]
		phrases := append([]string(nil), g.Phrases...)
		anchored := append([]string(nil), g.Anchored...)
		sort.Strings(phrases)
		sort.Strings(anchored)
		fmt.Fprintf(&b, "\x00%s|%s|%t|%s|%s", k, g.Pattern, g.CaseSensitive,
			strings.Join(phrases, ","), strings.Join(anchored, ","))
	}
	keys := make([]string, 0, len(p.Parameters))
	for k := range p.Parameters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "\x00%s=%g", k, p.Parameters[k])
	}
	dkeys := make([]string, 0, len(p.Discriminators))
	for k := range p.Discriminators {
		dkeys = append(dkeys, k)
	}
	sort.Strings(dkeys)
	for _, k := range dkeys {
		fmt.Fprintf(&b, "\x00%s=%t", k, p.Discriminators[k])
	}
	return b.String()
}

// Param returns a parameter, or the fallback when the profile does not set it.
func (p *Profile) Param(name string, fallback float64) float64 {
	if v, ok := p.Parameters[name]; ok {
		return v
	}
	return fallback
}

// On reports whether a discriminator is enabled. A discriminator the profile
// does not mention takes the fallback.
func (p *Profile) On(name string, fallback bool) bool {
	if v, ok := p.Discriminators[name]; ok {
		return v
	}
	return fallback
}

func union(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, s := range list {
			if s = strings.TrimSpace(s); s == "" {
				continue
			}
			key := strings.ToLower(s)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
