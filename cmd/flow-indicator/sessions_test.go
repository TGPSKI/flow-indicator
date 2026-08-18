package main

import (
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/harness"
)

// A handle the listing prints has to resolve to the session it was printed for.
// A shorter one that names two sessions is not a shortcut, it is a dead end.
func TestPrintedHandlesResolveToOneSession(t *testing.T) {
	all := []harness.Session{
		{ID: "019ffd5a-f531-7fb3-bcc8-c4ec95b8fd8f"},
		{ID: "019ffd20-d786-7cc1-b6ec-641063dd17f5"},
		{ID: "019ffd20-d786-7cc1-b6ec-000000000000"},
		{ID: "ses_002d13d18ffeO1gjzgk2TEOWQ1"},
	}
	handles := uniquePrefixes(all)

	for _, s := range all {
		h := handles[s.ID]
		if h == "" {
			t.Fatalf("%s got no handle", s.ID)
		}
		if !strings.HasPrefix(s.ID, h) {
			t.Errorf("handle %q is not a prefix of %q", h, s.ID)
		}
		var matched int
		for _, other := range all {
			if strings.HasPrefix(other.ID, h) {
				matched++
			}
		}
		if matched != 1 {
			t.Errorf("handle %q for %s matches %d sessions", h, s.ID, matched)
		}
	}

	// The two that share 28 characters must be told apart, and the two that do
	// not must stay short.
	if h := handles["019ffd5a-f531-7fb3-bcc8-c4ec95b8fd8f"]; len(h) != shortIDLen {
		t.Errorf("an identifier sharing nothing got a %d-character handle: %q", len(h), h)
	}
	if h := handles["019ffd20-d786-7cc1-b6ec-641063dd17f5"]; len(h) <= shortIDLen {
		t.Errorf("two identifiers sharing a long prefix were not told apart: %q", h)
	}
}

// One tool that spawns a session per task holds thousands of them in a single
// directory. Without a cap that project fills the page and the session the
// operator is sitting in is not on it.
func TestListingCapsOneProject(t *testing.T) {
	now := time.Now()
	var in []harness.Session
	for i := range 40 {
		in = append(in, harness.Session{
			ID: "noise", Harness: "opencode", Root: "/w/churn",
			Modified: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	in = append(in, harness.Session{
		ID: "wanted", Harness: "codex", Root: "/w/proj",
		Modified: now.Add(-time.Hour),
	})

	out := capPerProject(in, perProjectRows)
	if len(out) != perProjectRows+1 {
		t.Fatalf("kept %d rows, want %d from the busy project plus the other one", len(out), perProjectRows+1)
	}
	var found bool
	for _, s := range out {
		if s.ID == "wanted" {
			found = true
		}
	}
	if !found {
		t.Error("the quiet project's session was crowded out by the busy one")
	}
}

// Flags after a positional argument have to be honoured. `watch <id> --full` is
// the ordering people type once naming a session is the ordinary path, and the
// flag package stops at the first operand.
func TestFlagsAfterAPositionalAreParsed(t *testing.T) {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	full := fs.Bool("full", false, "")
	dataDir := fs.String("data-dir", "", "")

	operands, err := parseInterspersed(fs, []string{"019ffd20", "--full", "--data-dir", "/tmp/x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(operands) != 1 || operands[0] != "019ffd20" {
		t.Fatalf("operands = %v, want the one identifier", operands)
	}
	if !*full {
		t.Error("--full after the identifier was ignored")
	}
	if *dataDir != "/tmp/x" {
		t.Errorf("--data-dir after the identifier = %q, want /tmp/x", *dataDir)
	}
}

// A path is not an identifier. It is taken at its word rather than looked up.
func TestNamedPathIsNotResolvedAsAnIdentifier(t *testing.T) {
	got, err := resolveSession("/no/such/rollout.jsonl", "codex", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Locator != "/no/such/rollout.jsonl" {
		t.Errorf("locator = %q, want the path as given", got.Locator)
	}
	if got.Harness != "codex" {
		t.Errorf("harness = %q, want the one named", got.Harness)
	}
}

// A path names the only source discovery may read. Without --adapter it uses
// the command default; inferring a harness by scanning every store would violate
// the named-source boundary before decoding began.
func TestNamedPathWithoutAdapterDoesNotTriggerDiscovery(t *testing.T) {
	got, err := resolveSession("/no/such/session.jsonl", "claude-code", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != "claude-code" || got.Locator != "/no/such/session.jsonl" {
		t.Fatalf("resolved path = %+v, want the named path under the default harness", got)
	}
}

// An identifier that names nothing has to say what to do next, because the
// operator has no other way to find out what identifiers exist.
func TestUnknownIdentifierPointsAtTheListing(t *testing.T) {
	_, err := resolveSession("zzzzzzzzzzzz", "", false)
	if err == nil {
		t.Fatal("an identifier that names nothing resolved")
	}
	if !strings.Contains(err.Error(), "flow-indicator sessions") {
		t.Errorf("the error does not point at the listing: %v", err)
	}
}

// The listing leaves out sub-agent threads: they are an agent's own stream, and
// offering one as "your session" measures the wrong operator.
func TestListingLeavesOutSubagents(t *testing.T) {
	in := []harness.Session{
		{ID: "top", Harness: "codex", Root: "/w"},
		{ID: "child", Harness: "codex", Root: "/w", Parent: "top"},
		{ID: "side", Harness: "claude-code", Root: "/w", Sidechain: true},
	}
	out := filterSessions(in, "", false)
	if len(out) != 1 || out[0].ID != "top" {
		t.Errorf("listing kept %v, want only the operator's own session", out)
	}
}
