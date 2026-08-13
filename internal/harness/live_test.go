package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// codexRollout is a rollout in the shape Codex 0.147 writes: the working
// directory only on session_meta, operator text only in the event stream, and
// the wire log repeating the same conversation under a user role.
var codexRollout = []string{
	`{"timestamp":"2026-08-13T21:57:42.478Z","type":"session_meta","payload":{"session_id":"s1","id":"s1","cwd":"/w/proj","thread_source":"user"}}`,
	`{"timestamp":"2026-08-13T21:57:43.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"AGENTS.md injected context that is not typed input"}]}}`,
	`{"timestamp":"2026-08-13T21:57:44.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"t1","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"add the retry loop to worker/retry.go"}]}}}`,
	`{"timestamp":"2026-08-13T21:57:48.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"t1","item":{"type":"CommandExecution","id":"e1","command":["/usr/bin/zsh","-lc","go test ./..."],"cwd":"file:///w/proj","exit_code":0}}}`,
	`{"timestamp":"2026-08-13T21:57:52.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"t1","item":{"type":"AgentMessage","id":"a1","content":[{"type":"text","text":"done"}]}}}`,
	`{"timestamp":"2026-08-13T21:58:02.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"t2","item":{"type":"UserMessage","id":"u2","content":[{"type":"text","text":"no, revert that"}]}}}`,
}

func writeRollout(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The meter must not disagree with replay about what a session contains. Codex
// is the case where it could: the whole-file path and the chunked path are two
// readers of one format, and the format puts the working directory on a line
// the chunked reader may never see twice.
func TestCodexChunkedDecodeMatchesWholeFile(t *testing.T) {
	path := writeRollout(t, codexRollout)
	s := Session{ID: "s1", Locator: path}

	want, err := Codex{}.Open(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("the whole-file path decoded nothing; the fixture is wrong")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Split on a record boundary partway through, exactly as a tail does when a
	// session is written to while it is being followed.
	cut := strings.Index(string(raw), codexRollout[3]) + len(codexRollout[3]) + 1
	dec := Codex{}.Chunks(s)
	var got []stream.Record
	for _, chunk := range [][]byte{raw[:cut], raw[cut:]} {
		recs, err := dec.Decode(chunk, stream.SourceRef{Adapter: "codex", Path: path})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, recs...)
	}

	if len(got) != len(want) {
		t.Fatalf("chunked decode produced %d records, whole-file produced %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Text != want[i].Text || got[i].Speaker != want[i].Speaker {
			t.Errorf("record %d differs: chunked %q/%q, whole-file %q/%q",
				i, got[i].Speaker, got[i].Text, want[i].Speaker, want[i].Text)
		}
		if got[i].Seq != want[i].Seq {
			t.Errorf("record %d seq: chunked %d, whole-file %d", i, got[i].Seq, want[i].Seq)
		}
		if len(got[i].Actions) != len(want[i].Actions) {
			t.Errorf("record %d carries %d actions chunked, %d whole-file",
				i, len(got[i].Actions), len(want[i].Actions))
		}
	}
}

// A tail started at the end never sees session_meta, and session_meta is the
// only line that names the working directory. Without it every action target is
// an absolute path and no rule that compares targets can fire.
func TestCodexTailReadsTheRootFromTheFileHead(t *testing.T) {
	path := writeRollout(t, codexRollout)
	dec := Codex{}.Chunks(Session{ID: "s1", Locator: path})

	// Feed only the trailing lines, as a tail from the end would.
	tailBytes := []byte(strings.Join(codexRollout[3:], "\n") + "\n")
	recs, err := dec.Decode(tailBytes, stream.SourceRef{Adapter: "codex", Path: path})
	if err != nil {
		t.Fatal(err)
	}

	var targets []string
	for _, r := range recs {
		for _, a := range r.Actions {
			targets = append(targets, a.Targets...)
		}
	}
	for _, target := range targets {
		if strings.HasPrefix(target, "/w/proj") {
			t.Errorf("target %q is absolute; the root was not read from the file head", target)
		}
	}
}

// Following is not replaying. A record handed to the projector cannot be
// amended, so a chunk that arrives after the operator's turn must carry only
// what is new.
func TestCodexTailEmitsEachRecordOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(codexRollout[:3], "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	live, err := Follow("codex", path, "", "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	first, err := live.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Text != "add the retry loop to worker/retry.go" {
		t.Fatalf("first read returned %d records, want the one operator turn", len(first))
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(strings.Join(codexRollout[3:], "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var rest []stream.Record
	for len(rest) < 3 {
		recs, err := live.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rest = append(rest, recs...)
	}
	for _, r := range rest {
		if r.Text == "add the retry loop to worker/retry.go" {
			t.Error("the first operator turn was delivered a second time")
		}
	}
	if rest[len(rest)-1].Text != "no, revert that" {
		t.Errorf("last record is %q, want the second operator turn", rest[len(rest)-1].Text)
	}
}

// Every registered harness has to be reachable live, or say plainly that it is
// not. The failure this replaces was a harness the help text advertised and the
// watch path rejected.
func TestEveryHarnessIsFollowableOrSaysWhyNot(t *testing.T) {
	for _, name := range Names() {
		h, err := New(name)
		if err != nil {
			t.Fatal(err)
		}
		_, chunker := h.(Chunker)
		_, follower := h.(Follower)
		if !chunker && !follower {
			t.Errorf("%s can be replayed but not followed, and nothing says so", name)
		}
	}
}

// A name the help text advertises must resolve everywhere it is advertised.
func TestAdvertisedHarnessesResolveForWatching(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "opencode", "generic"} {
		if _, err := New(name); err != nil {
			t.Errorf("%s does not resolve: %v", name, err)
		}
	}
}

// newOpencodeDB builds a store with opencode's schema, so the poller is
// exercised against the same SQL the real one runs.
func newOpencodeDB(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not on PATH; opencode is read through it")
	}
	path := filepath.Join(t.TempDir(), "opencode.db")
	run := func(sql string) {
		t.Helper()
		cmd := exec.Command("sqlite3", path, sql)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v: %s", err, out)
		}
	}
	run(`CREATE TABLE session (id text PRIMARY KEY, project_id text NOT NULL, parent_id text,
		directory text NOT NULL, title text NOT NULL, time_created integer NOT NULL,
		time_updated integer NOT NULL);
	CREATE TABLE message (id text PRIMARY KEY, session_id text NOT NULL,
		time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL);
	CREATE TABLE part (id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL,
		time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL);`)
	return path
}

func opencodeExec(t *testing.T, db, sql string) {
	t.Helper()
	cmd := exec.Command("sqlite3", db, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
}

// opencode grows the newest message in place while the agent streams parts into
// it. The projector cannot amend a record it has consumed, so an agent message
// that is still being written must be held back rather than frozen half-formed.
func TestOpenCodeFollowHoldsBackTheMessageStillBeingWritten(t *testing.T) {
	db := newOpencodeDB(t)
	opencodeExec(t, db, `INSERT INTO session VALUES ('ses_test1','p1',NULL,'/w/proj','t',1000,1000);
		INSERT INTO message VALUES ('m1','ses_test1',1000,1000,'{"role":"user"}');
		INSERT INTO part VALUES ('p1','m1','ses_test1',1000,1000,'{"type":"text","text":"add the retry loop"}');
		INSERT INTO message VALUES ('m2','ses_test1',2000,2000,'{"role":"assistant"}');
		INSERT INTO part VALUES ('p2','m2','ses_test1',2000,2000,'{"type":"text","text":"working on it"}');`)

	live, err := Follow("opencode", db+"#ses_test1", "/w/proj", "ses_test1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first, err := live.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The operator's turn is complete on insert and is released at once. The
	// agent's reply is the newest message and is still open.
	if len(first) != 1 {
		t.Fatalf("first read returned %d records, want only the completed operator turn", len(first))
	}
	if !first[0].IsOperatorTurn() {
		t.Fatalf("first record is not the operator turn: %+v", first[0])
	}

	// The agent finishes its message and the operator speaks again.
	opencodeExec(t, db, `INSERT INTO part VALUES ('p3','m2','ses_test1',2100,2100,
			'{"type":"tool","tool":"bash","callID":"c1","state":{"status":"completed","input":{"command":"go test ./..."}}}');
		INSERT INTO message VALUES ('m3','ses_test1',3000,3000,'{"role":"user"}');
		INSERT INTO part VALUES ('p4','m3','ses_test1',3000,3000,'{"type":"text","text":"no, revert that"}');`)

	var rest []stream.Record
	for len(rest) < 2 {
		recs, err := live.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rest = append(rest, recs...)
	}

	// The agent message is released only once it is whole, with the tool call
	// that landed after the first poll.
	if rest[0].IsOperatorTurn() {
		t.Fatal("the agent message was never delivered")
	}
	if len(rest[0].Actions) != 1 {
		t.Errorf("agent message carries %d actions, want the bash call that arrived after the first poll", len(rest[0].Actions))
	}
	if rest[1].Text != "no, revert that" {
		t.Errorf("last record is %q, want the second operator turn", rest[1].Text)
	}
	// Nothing may arrive twice.
	seen := map[string]int{}
	for _, r := range append(first, rest...) {
		seen[r.TurnID]++
		if seen[r.TurnID] > 1 {
			t.Errorf("record %s was delivered %d times", r.TurnID, seen[r.TurnID])
		}
	}
}
