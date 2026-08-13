package classify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/profile"
)

// harnessToolNames are tool names belonging to one agent harness. A rule that
// named one would inherit that harness's vocabulary the way the marker rules
// inherit English's, and would stop working the moment the same behaviour
// arrived under a different name.
//
// The adapter is allowed to know them — mapping them into the action vocabulary
// is its whole job. Nothing downstream of the adapter is.
var harnessToolNames = []string{
	"AskUserQuestion", "NotebookEdit", "WebSearch", "WebFetch",
	"ToolSearch", "TaskCreate", "TaskUpdate", "SendMessage",
	"tool_use", "tool_result", "toolUseResult", "isSidechain", "parentUuid",
}

// "Broadly applicable" is a claim, and this is the test that can fail it.
//
// Every rule reads the action vocabulary. If a harness's own tool name appears
// in a rule file, the rule is about that harness, and a score it earns on that
// harness's transcripts is a score about the harness.
func TestRulesNameNoHarnessTool(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		for _, name := range harnessToolNames {
			if strings.Contains(body, name) {
				t.Errorf("%s names the harness tool %q; rules read the action vocabulary only", f, name)
			}
		}
	}
}

// The lexicon lives in the shipped profile, not scattered through the rules. An
// operator who needs their own correction vocabulary writes an overlay; they do
// not edit Go. This is what makes the instrument extensible past one operator.
func TestTheLexiconLivesInTheProfile(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "regexp.MustCompile") {
			t.Errorf("%s compiles a pattern of its own; marker patterns belong in the profile", f)
		}
	}
}

// An overlay changes what the rules match, and says so in the hash. A
// classification made under an operator's own lexicon must never be mistaken for
// one made under the shipped baseline.
func TestOverlayChangesTheRulesAndTheHash(t *testing.T) {
	base, err := profile.Baseline()
	if err != nil {
		t.Fatal(err)
	}
	overlay := &profile.Profile{
		Name:    "test-operator",
		Markers: map[string]profile.MarkerGroup{"correction_tier1": {Phrases: []string{"bzzt"}}},
	}
	rs, err := profile.Compile(profile.Merge(base, overlay))
	if err != nil {
		t.Fatal(err)
	}
	custom := Heuristic{Rules: rs}

	if !rs.Match("correction_tier1", "bzzt, not like that") {
		t.Error("the overlay's phrase does not match")
	}
	if !rs.Match("correction_tier1", "no, that is wrong") {
		t.Error("the overlay replaced the baseline instead of adding to it")
	}
	if custom.Hash() == (Heuristic{}).Hash() {
		t.Error("a classifier under an overlay hashes the same as the baseline")
	}
}

// The one place a harness's vocabulary is allowed to live is its own adapter.
// This states where that boundary is so a reader does not have to infer it from
// the absence of a failure.
func TestTheAdapterIsTheOnlyPlaceHarnessNamesLive(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "adapter", "claude.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "claudeToolVerbs") {
		t.Error("the Claude adapter no longer holds the tool-to-verb table; the boundary this gate assumes has moved")
	}
}

// The lexical rules, listed.
//
// This is the honest statement of the instrument's reach. Every rule named here
// is English-only, and the list is expected to shrink each pass. A rule that
// leaves it has been replaced by structure; a rule that joins it has to say why
// structure could not carry the work.
func TestLexicalRulesAreListed(t *testing.T) {
	// Counted rather than named one by one: the marker groups are the list, and
	// keeping a second copy of it here would drift.
	patterns := len(patternTable(Heuristic{}.rules()))
	const listed = 31
	if patterns != listed {
		t.Errorf("the build carries %d lexical patterns, the register records %d; "+
			"a change to the regex budget is a registered change", patterns, listed)
	}

	// The two closed-class word sets are lexical too, and they are English. They
	// are not patterns, so they would otherwise escape the budget entirely.
	if len(preSubjectWords) == 0 || len(closedClass) == 0 || len(negations) == 0 {
		t.Error("a closed-class word set is empty; the structural rules silently stopped discriminating")
	}
}

// The budget is reported, and every free parameter is declared. A parameter that
// appears without being declared is how a corpus gets fitted.
func TestEveryFreeParameterIsDeclared(t *testing.T) {
	b := CurrentBudget()
	if len(b.Parameters) == 0 {
		t.Fatal("the build declares no free parameters, which cannot be true of a build with thresholds in it")
	}
	for _, p := range b.Parameters {
		if strings.TrimSpace(p.Justification) == "" {
			t.Errorf("parameter %s carries no justification", p.Name)
		}
		if !p.Fitted && strings.Contains(p.Justification, "no visible separation") {
			t.Errorf("parameter %s admits no demonstrated separation but is not declared fitted", p.Name)
		}
	}
}
