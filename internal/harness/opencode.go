package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func init() { Register(OpenCode{}) }

// OpenCode reads opencode's transcript store. Every opencode-specific field name
// in this program appears in this file and nowhere else.
//
// opencode keeps its transcripts in SQLite rather than in files, which is why
// this is a harness and not an adapter: there is no byte stream to hand a
// decoder. The store is read through the sqlite3 command, in read-only mode,
// because this program carries no dependencies and a pure-Go SQLite engine would
// be the largest thing in it by an order of magnitude. Read-only matters twice
// over: opencode may be running against this database right now, and nothing
// here may disturb it.
//
// The operator's own words need care here too. A user-role message can carry
// text the harness injected — editor context, compaction continuations,
// system-reminder envelopes — and those are marked with a synthetic flag that
// looks like nothing at all if you do not read it.
type OpenCode struct{}

func (OpenCode) Name() string { return "opencode" }

// opencodeDataEnv is the XDG variable opencode's data root honours.
const opencodeDataEnv = "XDG_DATA_HOME"

// opencodeDB is the store's file name under the data root.
const opencodeDB = "opencode.db"

func (OpenCode) DefaultRoot() (string, error) {
	dir := os.Getenv(opencodeDataEnv)
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("opencode: locate home directory: %w", err)
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "opencode", opencodeDB), nil
}

// queryTimeout bounds one sqlite3 read.
//
// Discovery runs this against every store it finds, and a live watch reruns it
// on every poll. A database another process holds locked returns nothing and
// waits, and an unbounded wait there is a hang in a program whose whole job is
// to redraw a meter. Ten seconds is far longer than any observed read of a real
// store and short enough that a locked one reports rather than stops.
const queryTimeout = 10 * time.Second

// query runs one read-only statement and returns the rows as JSON.
//
// The URI carries mode=ro rather than immutable=1. Immutable would let the read
// skip the write-ahead log, which is where a live session's most recent turns
// are: the answer would be a stale transcript that looks complete.
func (OpenCode) query(dbPath, sql string) ([]byte, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	uri := "file:" + dbPath + "?mode=ro"
	cmd := exec.CommandContext(ctx, "sqlite3", "-json", "-readonly", uri, sql)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if _, look := exec.LookPath("sqlite3"); look != nil {
			return nil, fmt.Errorf("opencode: reading %s needs the sqlite3 command, which is not on PATH; "+
				"opencode keeps its transcripts in SQLite and this program carries no database driver", dbPath)
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("opencode: reading %s did not finish in %s; another process is holding the "+
				"store locked", dbPath, queryTimeout)
		}
		return nil, fmt.Errorf("opencode: query %s: %w: %s", dbPath, err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// opencodeSession is one row of the session table.
type opencodeSession struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	ParentID  string `json:"parent_id"`
	Directory string `json:"directory"`
	Title     string `json:"title"`
	Created   int64  `json:"time_created"`
	Updated   int64  `json:"time_updated"`
}

func (o OpenCode) Discover(root string) ([]Session, error) {
	raw, err := o.query(root, `SELECT id, project_id, COALESCE(parent_id,'') AS parent_id,
		COALESCE(directory,'') AS directory, COALESCE(title,'') AS title,
		time_created, time_updated FROM session ORDER BY time_created, id;`)
	if err != nil {
		if os.IsNotExist(err) {
			// Not having used opencode is not a failure.
			return nil, nil
		}
		return nil, err
	}
	var rows []opencodeSession
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("opencode: decode session rows: %w", err)
	}
	info, statErr := os.Stat(root)
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		s := Session{
			ID:      r.ID,
			Locator: root + "#" + r.ID,
			Root:    r.Directory,
			Project: r.Directory,
			Parent:  r.ParentID,
		}
		if r.Updated > 0 {
			s.Modified = time.UnixMilli(r.Updated).UTC()
		} else if statErr == nil {
			s.Modified = info.ModTime()
		}
		out = append(out, s)
	}
	return out, nil
}

// opencodeRow is one message with its parts, as the join returns them.
type opencodeRow struct {
	MessageID string `json:"message_id"`
	Created   int64  `json:"time_created"`
	Data      string `json:"data"`
	Kind      string `json:"kind"`
}

// opencodeMessage is the message table's JSON blob.
type opencodeMessage struct {
	Role string `json:"role"`
}

// opencodePart is the part table's JSON blob.
type opencodePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
	// Synthetic marks text the harness injected into a user-role message:
	// editor context, compaction continuations, reminder envelopes. It reads
	// exactly like typed input and is not.
	Synthetic bool           `json:"synthetic"`
	Tool      string         `json:"tool"`
	CallID    string         `json:"callID"`
	State     *opencodeState `json:"state"`
}

// opencodeState is a tool part's whole lifecycle: the call and its result live
// in one record, so there is nothing to join.
type opencodeState struct {
	Status   string          `json:"status"`
	Input    json.RawMessage `json:"input"`
	Error    string          `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

// opencodeToolInput carries the argument names this harness reads.
type opencodeToolInput struct {
	FilePath string `json:"filePath"`
	Path     string `json:"path"`
	Command  string `json:"command"`
	Pattern  string `json:"pattern"`
}

// opencodeToolMetadata carries the result fields that establish failure. A bash
// tool can report status "completed" and a non-zero exit at the same time;
// reading only the status under-counts failures.
type opencodeToolMetadata struct {
	Exit *int `json:"exit"`
}

// opencodeVerbs maps opencode's built-in tools into the action vocabulary. A
// tool absent from the table maps to the unknown verb and is counted.
var opencodeVerbs = map[string]stream.ActionVerb{
	"read":      stream.VerbRead,
	"list":      stream.VerbRead,
	"webfetch":  stream.VerbRead,
	"write":     stream.VerbWrite,
	"edit":      stream.VerbWrite,
	"patch":     stream.VerbWrite,
	"bash":      stream.VerbExecute,
	"grep":      stream.VerbSearch,
	"glob":      stream.VerbSearch,
	"websearch": stream.VerbSearch,
	"question":  stream.VerbAsk,
}

func (o OpenCode) Open(s Session) ([]stream.Record, error) {
	dbPath, id, ok := strings.Cut(s.Locator, "#")
	if !ok {
		return nil, fmt.Errorf("opencode: locator %q names no session", s.Locator)
	}
	// The identifier is a generated key of a known shape. Checking it rather
	// than quoting it keeps a session id from reaching the statement text at
	// all.
	if !validOpencodeID(id) {
		return nil, fmt.Errorf("opencode: session id %q is not a well-formed identifier", id)
	}
	sql := fmt.Sprintf(`SELECT message_id, time_created, data, kind FROM (
		SELECT id AS message_id, time_created, data, 'message' AS kind FROM message WHERE session_id = '%[1]s'
		UNION ALL
		SELECT message_id, time_created, data, 'part' AS kind FROM part WHERE session_id = '%[1]s'
	) ORDER BY time_created, kind DESC, message_id;`, id)

	raw, err := o.query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var rows []opencodeRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("opencode: decode rows: %w", err)
	}

	// A message row establishes the role its parts belong to; the parts carry
	// the content. They are folded into one record per message so a turn is a
	// turn rather than a scattering of fragments.
	roles := map[string]string{}
	order := []string{}
	texts := map[string][]string{}
	actions := map[string][]stream.Action{}
	created := map[string]int64{}

	for _, r := range rows {
		if _, seen := created[r.MessageID]; !seen {
			order = append(order, r.MessageID)
			created[r.MessageID] = r.Created
		}
		switch r.Kind {
		case "message":
			var m opencodeMessage
			if json.Unmarshal([]byte(r.Data), &m) == nil {
				roles[r.MessageID] = m.Role
			}
		case "part":
			var p opencodePart
			if json.Unmarshal([]byte(r.Data), &p) != nil {
				continue
			}
			switch p.Type {
			case "text":
				// Synthetic text is the harness talking, not the operator.
				if p.Synthetic || strings.TrimSpace(p.Text) == "" {
					continue
				}
				texts[r.MessageID] = append(texts[r.MessageID], p.Text)
			case "tool":
				if a, ok := opencodeAction(p, s.Root); ok {
					actions[r.MessageID] = append(actions[r.MessageID], a)
				}
			}
		}
	}

	sort.SliceStable(order, func(i, j int) bool { return created[order[i]] < created[order[j]] })

	out := make([]stream.Record, 0, len(order))
	var seq uint64
	for _, id := range order {
		role := roles[id]
		text := strings.Join(texts[id], "\n")
		acts := actions[id]
		if text == "" && len(acts) == 0 {
			continue
		}
		seq++
		rec := stream.Record{
			StreamID:  s.ID,
			TurnID:    id,
			Seq:       seq,
			Timestamp: time.UnixMilli(created[id]).UTC(),
			Text:      text,
			Actions:   acts,
			Source:    stream.SourceRef{Adapter: o.Name(), Path: dbPath},
			Metadata:  stream.Metadata{stream.MetaRecordType: role},
		}
		switch role {
		case "user":
			rec.Speaker, rec.SpeakerClass = "user", stream.SpeakerHuman
			rec.Metadata[stream.MetaInputMode] = stream.InputTyped
		case "assistant":
			rec.Speaker, rec.SpeakerClass = "assistant", stream.SpeakerAgent
		default:
			rec.Speaker, rec.SpeakerClass = role, stream.SpeakerUnknown
		}
		out = append(out, rec)
	}
	return out, nil
}

// opencodePoll is how often a followed session is re-queried.
//
// The query is bounded by session, and both tables are indexed on it, so the
// cost does not grow with the size of the store. It is the session's own length
// that is re-read each time.
const opencodePoll = 700 * time.Millisecond

// Follow re-queries a session on an interval.
//
// There is nothing to tail: opencode keeps the transcript as rows, and the
// newest message grows in place while the agent streams parts into it. Each
// poll rebuilds the session's records and hands back the ones not yet returned.
//
// The trailing record is held back while it belongs to the agent, because that
// is the message still being written — releasing it early would freeze a turn
// before its tool calls landed, and the projector has no way to amend a record
// it has already consumed. An operator's turn is inserted complete, so it is
// never held.
func (o OpenCode) Follow(s Session, offset int64) (Live, error) {
	l := &opencodeLive{h: o, session: s, interval: opencodePoll}
	recs, err := o.Open(s)
	if err != nil {
		return nil, err
	}
	l.initial = len(recs)
	switch {
	case offset == stream.TailFromEnd:
		// Start from whatever the session already holds and report only what
		// arrives after it.
		l.sent = len(recs)
	case offset > 0:
		l.sent = int(offset)
	}
	return l, nil
}

// opencodeLive is one session being followed by re-query.
type opencodeLive struct {
	h        OpenCode
	session  Session
	sent     int
	initial  int
	interval time.Duration
}

func (l *opencodeLive) Next(ctx context.Context) ([]stream.Record, error) {
	for {
		recs, err := l.h.Open(l.session)
		if err != nil {
			return nil, err
		}
		ready := len(recs)
		if ready > 0 && recs[ready-1].SpeakerClass != stream.SpeakerHuman {
			ready--
		}
		if ready > l.sent {
			out := recs[l.sent:ready]
			l.sent = ready
			return out, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(l.interval):
		}
	}
}

// Position is the count of records handed out. The store addresses messages by
// key, not by byte, so there is no offset to report.
func (l *opencodeLive) Position() int64                 { return int64(l.sent) }
func (l *opencodeLive) Historical(r stream.Record) bool { return r.Seq <= uint64(l.initial) }

// Mark counts records, not bytes: opencode addresses messages by key.
func (l *opencodeLive) Mark() string {
	if l.sent == 1 {
		return "1 record"
	}
	return fmt.Sprintf("%d records", l.sent)
}

func (l *opencodeLive) Close() error { return nil }

// opencodeAction reads one tool part into the action vocabulary.
func opencodeAction(p opencodePart, root string) (stream.Action, bool) {
	if p.Tool == "" || p.Tool == "invalid" {
		// "invalid" is not a tool. opencode emits it when the model's arguments
		// fail validation, and counting it as a call would put a verb on a
		// thing that never ran.
		return stream.Action{}, false
	}
	verb, ok := opencodeVerbs[p.Tool]
	if !ok {
		verb = stream.VerbUnknown
	}
	a := stream.Action{Verb: verb}
	if p.State == nil {
		return a, true
	}
	a.Failed = p.State.Status == "error"

	var in opencodeToolInput
	if len(p.State.Input) > 0 {
		_ = json.Unmarshal(p.State.Input, &in)
	}
	for _, candidate := range []string{in.FilePath, in.Path} {
		if t := stream.NormalizeTarget(root, candidate); t != "" {
			a.Targets = append(a.Targets, t)
		}
	}
	if in.Command != "" {
		a.Argv = []string{in.Command}
	}
	// A command that exited non-zero failed, whatever the status says.
	if len(p.State.Metadata) > 0 {
		var md opencodeToolMetadata
		if json.Unmarshal(p.State.Metadata, &md) == nil && md.Exit != nil && *md.Exit != 0 {
			a.Failed = true
		}
	}
	return a, true
}

// validOpencodeID reports that an identifier is the generated shape opencode
// produces: a short prefix, an underscore, then alphanumerics.
func validOpencodeID(id string) bool {
	if len(id) < 5 || len(id) > 64 {
		return false
	}
	for i := range len(id) {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
	}
	return true
}
