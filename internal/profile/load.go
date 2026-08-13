package profile

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// baselineJSON is the shipped profile. It travels in the binary so that a build
// with no configuration at all still classifies, and so that "the defaults" is a
// thing with a version and a fingerprint rather than a scattering of constants.
//
//go:embed baseline.json
var baselineJSON []byte

var (
	baselineOnce sync.Once
	baseline     *Profile
	baselineErr  error
)

// Baseline returns the profile shipped with this build.
//
// A malformed baseline is a build defect, not an operator error, so it is
// reported rather than worked around. Nothing falls back to an empty profile:
// classifying with no markers would report a calm session for every transcript.
func Baseline() (*Profile, error) {
	baselineOnce.Do(func() {
		baseline, baselineErr = Parse(baselineJSON, "baseline.json")
	})
	return baseline, baselineErr
}

// OverlayName is the file an operator writes their own lexicon into, beside the
// configuration file.
const OverlayName = "profile.json"

// Resolve returns the profile this build should use: the baseline, with an
// operator overlay merged over it when one exists.
//
// An explicit path that does not exist is an error, because the operator named
// it. The default path not existing is not: most operators never write one, and
// the baseline is meant to work without it.
func Resolve(explicit, configDir string) (*Profile, error) {
	base, err := Baseline()
	if err != nil {
		return nil, err
	}
	path := explicit
	if path == "" {
		if configDir == "" {
			return Merge(base, nil), nil
		}
		path = filepath.Join(configDir, OverlayName)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return Merge(base, nil), nil
		}
	}
	overlay, err := Load(path)
	if err != nil {
		return nil, err
	}
	return Merge(base, overlay), nil
}

// Ruleset is a profile compiled into the patterns the rules read.
//
// Groups are looked up by name. A name the profile does not carry compiles to
// nil, and a nil pattern does not match: a rule whose marker group an operator
// emptied stops firing rather than firing on everything.
type Ruleset struct {
	profile  *Profile
	compiled map[string]*regexp.Regexp
}

// Compile builds the ruleset for a profile.
func Compile(p *Profile) (*Ruleset, error) {
	rs := &Ruleset{profile: p, compiled: make(map[string]*regexp.Regexp, len(p.Markers))}
	for name, g := range p.Markers {
		re, err := g.Compile()
		if err != nil {
			return nil, fmt.Errorf("profile: group %q: %w", name, err)
		}
		rs.compiled[name] = re
	}
	return rs, nil
}

// Profile returns the profile the ruleset was compiled from.
func (r *Ruleset) Profile() *Profile { return r.profile }

// Group returns the compiled pattern for a marker group, or nil.
func (r *Ruleset) Group(name string) *regexp.Regexp { return r.compiled[name] }

// Match reports whether a group matches. A group that is absent or empty
// matches nothing.
func (r *Ruleset) Match(name, text string) bool {
	re := r.compiled[name]
	return re != nil && re.MatchString(text)
}

// Find returns the leftmost match of a group, or the empty string.
func (r *Ruleset) Find(name, text string) string {
	re := r.compiled[name]
	if re == nil {
		return ""
	}
	return re.FindString(text)
}

// FindIndex returns the leftmost match's byte range, or nil.
func (r *Ruleset) FindIndex(name, text string) []int {
	re := r.compiled[name]
	if re == nil {
		return nil
	}
	return re.FindStringIndex(text)
}

// FindAll returns every match of a group.
func (r *Ruleset) FindAll(name, text string) []string {
	re := r.compiled[name]
	if re == nil {
		return nil
	}
	return re.FindAllString(text, -1)
}

// Param returns a numeric parameter with a fallback.
func (r *Ruleset) Param(name string, fallback float64) float64 {
	return r.profile.Param(name, fallback)
}

// On reports whether a discriminator is enabled.
func (r *Ruleset) On(name string, fallback bool) bool { return r.profile.On(name, fallback) }

// Names lists the marker groups the ruleset carries, for the budget report.
func (r *Ruleset) Names() []string {
	out := make([]string, 0, len(r.compiled))
	for k, re := range r.compiled {
		if re != nil {
			out = append(out, k)
		}
	}
	return out
}
