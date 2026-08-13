package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/adapter"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func init() { Register(Codex{}) }

// Codex reads OpenAI Codex CLI rollout files. Every Codex-specific field name in
// this program appears in this file and nowhere else.
//
// Codex records the same conversation three times over: a wire log of what went
// to the model, an event stream of what the interface showed, and on newer
// versions a per-item completion feed. Only one of them is read for any given
// fact, because two of them replay history.
//
// The operator's own words are the hardest part. A record with role "user" in
// the wire log is usually not the operator at all — it is an AGENTS.md
// injection, an environment block, an approval-request envelope, or a
// transcript delta. Reading those as typed input inflates operator serialization
// by roughly ten times. Operator text is taken from the event stream alone,
// where the interface recorded what a person actually submitted.
type Codex struct{}

func (Codex) Name() string { return "codex" }

// codexHomeEnv is the documented override for where Codex keeps its state.
const codexHomeEnv = "CODEX_HOME"

func (Codex) DefaultRoot() (string, error) {
	if dir := os.Getenv(codexHomeEnv); dir != "" {
		return filepath.Join(dir, "sessions"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("codex: locate home directory: %w", err)
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// codexLine is the envelope every rollout record carries.
type codexLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// codexMeta is the session_meta payload, the first line of every rollout.
type codexMeta struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
	// ThreadSource is "user" for an operator's own thread and "subagent" for a
	// thread an agent spawned. ParentThreadID is set only on the latter.
	ThreadSource   string `json:"thread_source"`
	ParentThreadID string `json:"parent_thread_id"`
}

// codexEvent is the event_msg payload. Its own type field selects the shape.
type codexEvent struct {
	Type string `json:"type"`
	// Message carries operator text on releases up to 0.146.
	Message string `json:"message"`
	// Item carries it from 0.147 onward, along with every other item kind.
	Item   *codexItem `json:"item"`
	TurnID string     `json:"turn_id"`
	// Reason and NumTurns describe an aborted turn and a rollback.
	Reason   string `json:"reason"`
	NumTurns int    `json:"num_turns"`
	// Changes and Success describe a patch applied on releases up to 0.146,
	// where file writes are their own event rather than a completed item.
	Changes map[string]codexChange `json:"changes"`
	Success *bool                  `json:"success"`
	Status  string                 `json:"status"`
}

// codexItem is one completed item on 0.147 and later.
type codexItem struct {
	Type     string                 `json:"type"`
	ID       string                 `json:"id"`
	Content  []codexContent         `json:"content"`
	Command  []string               `json:"command"`
	CWD      string                 `json:"cwd"`
	Status   string                 `json:"status"`
	ExitCode *int                   `json:"exit_code"`
	Changes  map[string]codexChange `json:"changes"`
	Path     string                 `json:"path"`
}

// codexChange is one file a patch touched, keyed by absolute path.
type codexChange struct {
	Type string `json:"type"`
}

// codexContent is a content block. The type is spelled "text" inside a user item
// and "Text" inside an agent item, so the comparison folds case.
type codexContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Path string `json:"path"`
}

// operatorItem and agentItem are the item kinds that carry authored text.
const (
	codexUserItem  = "usermessage"
	codexAgentItem = "agentmessage"
	codexExecItem  = "commandexecution"
	codexFileItem  = "filechange"
)

func (c Codex) Discover(root string) ([]Session, error) {
	var out []Session
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		meta, ok := codexMetaOf(path)
		if !ok {
			return nil
		}
		id := meta.ID
		if id == "" {
			id = strings.TrimSuffix(d.Name(), ".jsonl")
		}
		s := Session{
			ID:       id,
			Locator:  path,
			Root:     meta.CWD,
			Project:  meta.CWD,
			Modified: info.ModTime(),
			Bytes:    info.Size(),
		}
		// A sub-agent thread names its parent; on such a thread the session_id
		// field holds the parent's identifier and id holds this thread's, which
		// is the reverse of a top-level thread. Keying off id keeps the two
		// straight.
		if meta.ThreadSource == "subagent" || (meta.ParentThreadID != "" && meta.ParentThreadID != meta.ID) {
			s.Parent = meta.ParentThreadID
			if s.Parent == "" {
				s.Parent = meta.SessionID
			}
		}
		out = append(out, s)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("codex: discover %s: %w", root, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Locator < out[j].Locator })
	return out, nil
}

// codexMetaOf reads a rollout's opening session_meta without decoding the rest.
func codexMetaOf(path string) (codexMeta, bool) {
	f, err := os.Open(path)
	if err != nil {
		return codexMeta{}, false
	}
	defer f.Close()
	sc := newLineScanner(f)
	for sc.Scan() {
		var line codexLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.Type != "session_meta" {
			continue
		}
		var m codexMeta
		if json.Unmarshal(line.Payload, &m) != nil {
			return codexMeta{}, false
		}
		return m, true
	}
	return codexMeta{}, false
}

func (c Codex) Open(s Session) ([]stream.Record, error) {
	f, err := os.Open(s.Locator)
	if err != nil {
		return nil, fmt.Errorf("codex: open %s: %w", s.Locator, err)
	}
	defer f.Close()

	d := &codexDecoder{streamID: s.ID, root: s.Root}
	src := stream.SourceRef{Adapter: c.Name(), Path: s.Locator}
	var out []stream.Record
	sc := newLineScanner(f)
	for sc.Scan() {
		if rec, ok := d.line(sc.Bytes(), src); ok {
			out = append(out, rec)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("codex: read %s: %w", s.Locator, err)
	}
	return out, nil
}

// Chunks returns a decoder for one rollout.
//
// The working directory is on the rollout's first line, and a tail told to
// start at the end never sees it. It is read off the head of the file here so
// that --tail-only still makes action targets relative, rather than silently
// reporting absolute paths for half the harnesses.
func (c Codex) Chunks(s Session) adapter.Adapter {
	root := s.Root
	if root == "" {
		if m, ok := codexMetaOf(s.Locator); ok {
			root = m.CWD
		}
	}
	return &codexDecoder{streamID: s.ID, root: root}
}

// codexDecoder turns rollout lines into records.
//
// It carries what the lines do not repeat: the working directory session_meta
// names, and the running ordinal. One instance decodes one rollout, whether
// that is the whole file at once or a chunk at a time behind the live meter.
type codexDecoder struct {
	streamID string
	root     string
	seq      uint64
}

func (d *codexDecoder) Name() string { return Codex{}.Name() }

// Decode reads a chunk of whole rollout lines. The caller guarantees the chunk
// ends on a record boundary.
func (d *codexDecoder) Decode(raw []byte, src stream.SourceRef) ([]stream.Record, error) {
	var out []stream.Record
	var at int64
	sc := newLineScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := sc.Bytes()
		lineSrc := src
		lineSrc.Offset = src.Offset + at
		// The scanner drops the newline it split on; the next line begins one
		// byte past this one.
		at += int64(len(line)) + 1
		if rec, ok := d.line(line, lineSrc); ok {
			out = append(out, rec)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("codex: read %s: %w", src.Path, err)
	}
	return out, nil
}

// line reads one rollout line, reporting whether it carried anything measured.
func (d *codexDecoder) line(raw []byte, src stream.SourceRef) (stream.Record, bool) {
	var line codexLine
	if json.Unmarshal(raw, &line) != nil {
		// A line that is not JSON is skipped rather than failing the file.
		// A rollout is appended to live and can be caught mid-write.
		return stream.Record{}, false
	}
	switch line.Type {
	case "session_meta":
		var m codexMeta
		if json.Unmarshal(line.Payload, &m) == nil && d.root == "" {
			d.root = m.CWD
		}
		return stream.Record{}, false
	case "event_msg":
		// The event stream is the only lane read. The wire log replays
		// history and injects context under a user role; the compaction
		// record embeds an entire prior conversation.
	default:
		return stream.Record{}, false
	}

	var ev codexEvent
	if json.Unmarshal(line.Payload, &ev) != nil {
		return stream.Record{}, false
	}
	rec, ok := codexRecord(ev, d.root, line.Timestamp)
	if !ok {
		return stream.Record{}, false
	}
	d.seq++
	rec.StreamID = d.streamID
	rec.Seq = d.seq
	rec.Source = src
	if rec.TurnID == "" {
		rec.TurnID = fmt.Sprintf("seq-%d", d.seq)
	}
	return rec, true
}

// codexRecord turns one event into a canonical record, or reports that the event
// carries nothing this program measures.
func codexRecord(ev codexEvent, root, timestamp string) (stream.Record, bool) {
	rec := stream.Record{
		Timestamp: parseCodexTime(timestamp),
		Metadata:  stream.Metadata{stream.MetaRecordType: ev.Type},
	}
	switch ev.Type {
	// Releases up to 0.146 emit the operator's message flat.
	case "user_message":
		if strings.TrimSpace(ev.Message) == "" {
			return rec, false
		}
		rec.Speaker, rec.SpeakerClass = "user", stream.SpeakerHuman
		rec.Metadata[stream.MetaInputMode] = stream.InputTyped
		rec.Text = ev.Message
		rec.TurnID = ev.TurnID
		return rec, true

	case "agent_message":
		if strings.TrimSpace(ev.Message) == "" {
			return rec, false
		}
		rec.Speaker, rec.SpeakerClass = "assistant", stream.SpeakerAgent
		rec.Text = ev.Message
		rec.TurnID = ev.TurnID
		return rec, true

	// Releases up to 0.146 report an applied patch as its own event. It is the
	// only place a write is typed on that format; without it an older session
	// yields turns and no actions at all, and every rule that reads a write set
	// silently reports nothing rather than reporting that it could not look.
	case "patch_apply_end":
		a := stream.Action{Verb: stream.VerbWrite}
		a.Failed = (ev.Success != nil && !*ev.Success) || ev.Status == "failed"
		paths := make([]string, 0, len(ev.Changes))
		for path := range ev.Changes {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			if t := stream.NormalizeTarget(root, codexPath(path)); t != "" {
				a.Targets = append(a.Targets, t)
			}
		}
		if len(a.Targets) == 0 {
			return rec, false
		}
		rec.Speaker, rec.SpeakerClass = "assistant", stream.SpeakerAgent
		rec.Actions = []stream.Action{a}
		rec.TurnID = ev.TurnID
		return rec, true

	// An interruption is the operator stopping the agent mid-turn. The harness
	// wrote the marker, so the record carries no operator characters.
	case "turn_aborted":
		rec.Speaker, rec.SpeakerClass = "system", stream.SpeakerSystem
		rec.Metadata[stream.MetaInputMode] = stream.InputInterrupt
		rec.Text = "turn aborted: " + ev.Reason
		rec.TurnID = ev.TurnID
		return rec, true

	// 0.147 and later route everything through completed items.
	case "item_completed":
		if ev.Item == nil {
			return rec, false
		}
		return codexItemRecord(rec, ev, root)
	}
	return rec, false
}

// codexItemRecord reads one completed item.
func codexItemRecord(rec stream.Record, ev codexEvent, root string) (stream.Record, bool) {
	item := ev.Item
	rec.TurnID = item.ID
	if rec.TurnID == "" {
		rec.TurnID = ev.TurnID
	}
	rec.Metadata[stream.MetaRecordType] = "item:" + item.Type

	switch strings.ToLower(item.Type) {
	case codexUserItem:
		text := codexText(item.Content)
		if strings.TrimSpace(text) == "" {
			return rec, false
		}
		rec.Speaker, rec.SpeakerClass = "user", stream.SpeakerHuman
		rec.Metadata[stream.MetaInputMode] = stream.InputTyped
		rec.Text = text
		return rec, true

	case codexAgentItem:
		rec.Speaker, rec.SpeakerClass = "assistant", stream.SpeakerAgent
		rec.Text = codexText(item.Content)
		return rec, true

	case codexExecItem:
		// A command is an execute action. Codex has no read, grep or glob tool:
		// every file it reads, it reads by running a program. That makes its
		// verb histogram incomparable with a harness that has those tools, and
		// nothing here pretends otherwise — a command is recorded as a command.
		rec.Speaker, rec.SpeakerClass = "assistant", stream.SpeakerAgent
		a := stream.Action{Verb: stream.VerbExecute}
		if len(item.Command) > 0 {
			a.Argv = item.Command
		}
		// The exit code is typed here, unlike the wire log where failure is
		// prose inside the output. A non-zero exit is a failed call whatever
		// the status string says.
		a.Failed = item.Status == "failed" || (item.ExitCode != nil && *item.ExitCode != 0)
		rec.Actions = []stream.Action{a}
		return rec, true

	case codexFileItem:
		rec.Speaker, rec.SpeakerClass = "assistant", stream.SpeakerAgent
		paths := make([]string, 0, len(item.Changes))
		for p := range item.Changes {
			paths = append(paths, p)
		}
		// The changes arrive in a map, which has no order. Sorting makes a
		// replay of the same file produce the same records; it is not evidence
		// about the order the agent wrote them.
		sort.Strings(paths)
		a := stream.Action{Verb: stream.VerbWrite, Failed: item.Status == "failed"}
		for _, p := range paths {
			if t := stream.NormalizeTarget(root, codexPath(p)); t != "" {
				a.Targets = append(a.Targets, t)
			}
		}
		if len(a.Targets) == 0 {
			return rec, false
		}
		rec.Actions = []stream.Action{a}
		return rec, true
	}
	return rec, false
}

// codexText joins a content array's text blocks. The block type is spelled
// "text" inside a user item and "Text" inside an agent one.
func codexText(blocks []codexContent) string {
	var parts []string
	for _, b := range blocks {
		if strings.EqualFold(b.Type, "text") && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// codexPath strips the file:// scheme Codex uses on some paths and not others.
func codexPath(p string) string { return strings.TrimPrefix(p, "file://") }

// parseCodexTime reads the RFC3339 stamp on a rollout line. Codex also writes
// unix seconds and unix milliseconds on various payloads; only the envelope's
// stamp is read, so there is one format to handle.
func parseCodexTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
