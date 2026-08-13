package adapter

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Claude decodes Claude Code session JSONL. Every Claude-specific field name
// in this program appears in this file and nowhere else.
type Claude struct{}

func (Claude) Name() string { return "claude-code" }

type claudeRecord struct {
	Type       string `json:"type"`
	Subtype    string `json:"subtype"`
	UUID       string `json:"uuid"`
	ParentUUID string `json:"parentUuid"`
	SessionID  string `json:"sessionId"`
	// CWD is the directory the CLI was running in. It is the session root that
	// action targets are made relative to, so a path inside the project reads
	// the same across sessions and no absolute home directory reaches the log.
	CWD           string          `json:"cwd"`
	Timestamp     string          `json:"timestamp"`
	IsSidechain   bool            `json:"isSidechain"`
	IsMeta        bool            `json:"isMeta"`
	Message       *claudeMessage  `json:"message"`
	Content       json.RawMessage `json:"content"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	Summary       string          `json:"summary"`
	// AITitle is the name the CLI gives the session, on its own record type.
	// It is rewritten as the conversation develops, so the last one seen is
	// the current name.
	AITitle string `json:"aiTitle"`
	// Attachment carries a queued operator message, among other things.
	Attachment *claudeAttachment `json:"attachment"`
	// IsCompactSummary marks the context summary the CLI writes when a
	// conversation is compacted. The record has the user type, and the
	// operator typed none of it.
	IsCompactSummary bool `json:"isCompactSummary"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// claudeAttachment is the CLI's envelope for something delivered into the
// conversation alongside a turn. A message the operator typed while the agent
// was still working arrives this way.
type claudeAttachment struct {
	Type string `json:"type"`
	// Prompt is the message the operator queued. It is a bare string when they
	// typed only text, and an array of content blocks when they attached
	// something — an image pasted alongside the words, most often.
	//
	// It is held raw because the two shapes are not interchangeable and a struct
	// that declared either one would fail to decode the other. That failure is
	// not local: an unmarshal error abandons the whole chunk, so one queued
	// screenshot made an entire session unreadable.
	Prompt      json.RawMessage `json:"prompt"`
	CommandMode string          `json:"commandMode"`
	Origin      struct {
		Kind string `json:"kind"`
	} `json:"origin"`
}

// attachmentPrompt is the operator-authored text of a queued message.
//
// Only the text blocks are read. An attached image arrives as base64 in the same
// array, and it is not characters the operator typed: counting it as operator
// serialization would charge them tens of thousands of characters for one paste
// and make a routine screenshot the largest steering turn of the session.
func attachmentPrompt(raw json.RawMessage) string {
	if s, ok := rawStringOK(raw); ok {
		return s
	}
	return nestedText(raw)
}

// queuedCommand is the attachment type carrying a message the operator sent
// mid-response.
//
// The CLI records such a message three times and never as a user record: an
// `enqueue` and a `remove` queue-operation, both carrying the text, and this
// attachment when it is delivered. The attachment is the one read, because it
// appears once and is the point the message entered the conversation. Reading
// the queue operations instead would count the same characters twice.
const queuedCommand = "queued_command"

// originHuman marks an attachment the operator authored. A queued command with
// any other origin was composed by the harness or an agent.
const originHuman = "human"

// commandModePrompt is the queued mode for ordinary typed text. Any other mode
// is a command aimed at the harness.
const commandModePrompt = "prompt"

// claudeToolUseResult carries the fields of toolUseResult this adapter reads.
// AskUserQuestion puts the operator's chosen answers in Answers, keyed by the
// question the agent asked.
type claudeToolUseResult struct {
	Answers     map[string]string `json:"answers"`
	Annotations map[string]struct {
		Notes string `json:"notes"`
	} `json:"annotations"`
}

// askUserQuestion is the tool whose result carries operator answers. A result
// is only read as operator input when it is linked to a call of this tool.
const askUserQuestion = "AskUserQuestion"

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	// Input is the tool call's arguments, read for action targets.
	Input json.RawMessage `json:"input"`
	// IsError marks a tool result the harness reported as a failure.
	IsError bool `json:"is_error"`
}

// claudeToolInput carries the tool-call argument names this adapter reads. Each
// is a Claude Code field name, so like every other one it appears here and
// nowhere else in the program.
type claudeToolInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
	Command      string `json:"command"`
}

// claudeToolVerbs maps each Claude Code tool this adapter recognizes to the
// action vocabulary. A tool absent from the table maps to the unknown verb and
// is counted: guessing a verb for an unrecognized call would put paths into a
// write set that nothing established were written.
var claudeToolVerbs = map[string]stream.ActionVerb{
	"Read":            stream.VerbRead,
	"Edit":            stream.VerbWrite,
	"Write":           stream.VerbWrite,
	"NotebookEdit":    stream.VerbWrite,
	"Bash":            stream.VerbExecute,
	"Grep":            stream.VerbSearch,
	"Glob":            stream.VerbSearch,
	"WebSearch":       stream.VerbSearch,
	"WebFetch":        stream.VerbRead,
	"AskUserQuestion": stream.VerbAsk,
}

// actionsOf reads the tool calls of one assistant message into the action
// vocabulary, in source order, and returns the call identifier of each so a
// later error result can be attributed to the call it answers.
func actionsOf(m *claudeMessage, root string) ([]stream.Action, []string) {
	if m == nil || len(m.Content) == 0 {
		return nil, nil
	}
	var blocks []claudeBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil, nil
	}
	var actions []stream.Action
	var ids []string
	for _, b := range blocks {
		if b.Type != "tool_use" || b.Name == "" {
			continue
		}
		verb, ok := claudeToolVerbs[b.Name]
		if !ok {
			verb = stream.VerbUnknown
		}
		var in claudeToolInput
		if len(b.Input) > 0 {
			// A malformed input leaves the action with its verb and no targets.
			// The call still happened; only what it named is unavailable.
			_ = json.Unmarshal(b.Input, &in)
		}
		a := stream.Action{Verb: verb}
		for _, p := range []string{in.FilePath, in.NotebookPath, in.Path} {
			if t := stream.NormalizeTarget(root, p); t != "" {
				a.Targets = append(a.Targets, t)
			}
		}
		if in.Command != "" {
			// The CLI records a shell string, not an argument vector. It is
			// carried whole rather than split: splitting would be a shell parse,
			// and a wrong split is a wrong argv rather than a missing one.
			a.Argv = []string{in.Command}
		}
		actions = append(actions, a)
		ids = append(ids, b.ID)
	}
	return actions, ids
}

// failedCallIDs lists the call identifiers whose results this record reports as
// errors.
func failedCallIDs(m *claudeMessage) []string {
	if m == nil || len(m.Content) == 0 {
		return nil
	}
	var blocks []claudeBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "tool_result" && b.IsError && b.ToolUseID != "" {
			out = append(out, b.ToolUseID)
		}
	}
	return out
}

// nonTurnTypes are Claude Code bookkeeping records: session metadata, file
// history, queue and link entries. They are retained with provenance and
// classed as system so they carry no turn weight.
var nonTurnTypes = map[string]bool{
	"summary":               true,
	"attachment":            true,
	"mode":                  true,
	"ai-title":              true,
	"agent-name":            true,
	"last-prompt":           true,
	"permission-mode":       true,
	"queue-operation":       true,
	"file-history-snapshot": true,
	"file-history-delta":    true,
	"pr-link":               true,
	"frame-link":            true,
}

// Decode reads a chunk of session JSONL into canonical records.
//
// One fact is chunk-scoped: a tool call is marked failed by the error result
// that answers it, and a result lands in a later record than its call. Replay
// hands the whole file to one call, so every failure resolves. A tail delivering
// the call in one chunk and the result in the next leaves that call unmarked —
// the failure is not misreported, it is missing, and the live meter reads no
// rule off it. Calibration replays whole files, so it is unaffected.
func (c Claude) Decode(raw []byte, source stream.SourceRef) ([]stream.Record, error) {
	lines := splitLines(raw)
	out := make([]stream.Record, 0, len(lines))
	// toolNameByUseID links a tool result back to the call that produced it.
	// The identifiers are the source's own, and a result always follows its
	// call, so the map is complete by the time any result is read.
	toolNameByUseID := make(map[string]string)
	// actionByUseID locates the action a call produced, so the error result that
	// answers it can mark it failed. A result whose call is in an earlier chunk
	// is not in this map: see the note on Decode.
	actionByUseID := make(map[string]actionSite)
	// root is the session's working directory, taken from the first record that
	// carries one. Action targets are made relative to it.
	var root string
	for i, ls := range lines {
		var cr claudeRecord
		if err := json.Unmarshal(ls.bytes, &cr); err != nil {
			return nil, fmt.Errorf("claude-code: decode record at offset %d: %w", source.Offset+ls.offset, err)
		}
		if root == "" {
			root = cr.CWD
		}
		if cr.Type == "assistant" {
			maps.Copy(toolNameByUseID, toolCalls(cr.Message))
		}
		for _, id := range failedCallIDs(cr.Message) {
			if site, ok := actionByUseID[id]; ok {
				out[site.record].Actions[site.action].Failed = true
			}
		}
		rec := stream.Record{
			StreamID:  cr.SessionID,
			TurnID:    cr.UUID,
			Seq:       uint64(i + 1),
			Timestamp: parseTimestamp(cr.Timestamp),
			ReplyTo:   cr.ParentUUID,
			Source:    sourceFor(source, c.Name(), ls),
			Metadata:  stream.Metadata{stream.MetaRecordType: cr.Type},
		}
		if cr.IsSidechain {
			rec.Metadata[stream.MetaSidechain] = "true"
		}
		if cr.Subtype != "" {
			rec.Metadata["record_subtype"] = cr.Subtype
		}
		c.classify(&cr, &rec, toolNameByUseID)
		if rec.TurnID == "" {
			rec.TurnID = fmt.Sprintf("seq-%d", rec.Seq)
		}
		if cr.Type == "assistant" {
			var ids []string
			rec.Actions, ids = actionsOf(cr.Message, root)
			for j, id := range ids {
				if id != "" {
					actionByUseID[id] = actionSite{record: len(out), action: j}
				}
			}
		}
		out = append(out, rec)
	}
	return out, nil
}

// actionSite locates one action within the records this call is building, so an
// error result can be attributed to the call it answers.
type actionSite struct{ record, action int }

// classify sets speaker, speaker class and text. An unrecognized record type is
// not an error: it becomes an unknown-class record that keeps its provenance.
func (c Claude) classify(cr *claudeRecord, rec *stream.Record, toolNameByUseID map[string]string) {
	switch {
	case cr.Type == "user" && cr.IsMeta:
		// Meta user records are injected by the CLI (command caveats, hook
		// output). They are not operator serialization.
		rec.Speaker, rec.SpeakerClass = "system", stream.SpeakerSystem
		rec.Text = textOfMessage(cr.Message)

	case cr.Type == "user" && cr.IsCompactSummary:
		// The CLI writes the context summary under the user type after a
		// compaction. The operator typed the command; the harness wrote the
		// summary, which runs to tens of thousands of characters. Counting it
		// as operator serialization makes a routine compaction look like the
		// largest steering turn of the session.
		rec.Speaker, rec.SpeakerClass = "system", stream.SpeakerSystem
		rec.Metadata[stream.MetaInputMode] = stream.InputCompactSummary
		rec.Text = textOfMessage(cr.Message)

	case cr.Type == "user":
		text, tools, hasText := blocksOfMessage(cr.Message)

		// The operator's answers to an AskUserQuestion arrive inside the tool
		// result for that call. They are operator control, not tool output, and
		// only the answers are the operator's: the questions were the agent's.
		//
		// The originating tool is established, not guessed: the result block
		// names the call it answers, and the call named its tool. Any other
		// tool's result that happens to carry an "answers" object stays tool
		// output, because nothing about it was typed by the operator.
		if answers, ok := operatorAnswers(cr.ToolUseResult, cr.Message, toolNameByUseID); ok {
			rec.Speaker, rec.SpeakerClass = "user", stream.SpeakerHuman
			rec.Metadata[stream.MetaInputMode] = stream.InputStructuredAnswer
			rec.Text = answers
			return
		}
		if len(cr.ToolUseResult) > 0 || (!hasText && tools > 0) {
			// Tool results arrive as user records. Counting them as operator
			// input would inflate every transmission metric.
			rec.Speaker, rec.SpeakerClass = "tool", stream.SpeakerTool
			rec.Text = text
			return
		}

		// The CLI records several non-typed things under the user type, each
		// wrapped in its own tag. They are separated here because they are
		// evidence of different acts.
		switch mode, body := cliWrapper(text); mode {
		case stream.InputShellEscape, stream.InputCLICommand:
			rec.Speaker, rec.SpeakerClass = "user", stream.SpeakerHuman
			rec.Metadata[stream.MetaInputMode] = mode
			rec.Text = body
		case commandOutput:
			// Shell and slash-command output. The operator typed none of it.
			rec.Speaker, rec.SpeakerClass = "tool", stream.SpeakerTool
			rec.Text = body
		case stream.InputInterrupt:
			// The CLI wrote this line, not the operator. It establishes that an
			// interruption happened; what the operator meant by it is not in
			// the record.
			rec.Speaker, rec.SpeakerClass = "system", stream.SpeakerSystem
			rec.Metadata[stream.MetaInputMode] = stream.InputInterrupt
			rec.Text = text
		default:
			rec.Speaker, rec.SpeakerClass = "user", stream.SpeakerHuman
			rec.Metadata[stream.MetaInputMode] = stream.InputTyped
			rec.Text = text
		}

	case cr.Type == "assistant":
		text, _, _ := blocksOfMessage(cr.Message)
		rec.Speaker, rec.SpeakerClass = "assistant", stream.SpeakerAgent
		rec.Text = text
		if names := toolNames(cr.Message); names != "" {
			rec.Metadata[stream.MetaToolName] = names
		}

	case cr.Type == "system":
		rec.Speaker, rec.SpeakerClass = "system", stream.SpeakerSystem
		rec.Text = rawString(cr.Content)

	case cr.Type == "attachment" && cr.Attachment != nil &&
		cr.Attachment.Type == queuedCommand && cr.Attachment.Origin.Kind == originHuman:
		// A message typed while the agent was still working is operator
		// serialization like any other. It reaches the stream by a different
		// route, not by a different author, and counting it as bookkeeping
		// loses the turn along with every character the operator spent on it.
		rec.Speaker, rec.SpeakerClass = "user", stream.SpeakerHuman
		mode := stream.InputTyped
		if cr.Attachment.CommandMode != commandModePrompt {
			mode = stream.InputCLICommand
		}
		rec.Metadata[stream.MetaInputMode] = mode
		rec.Text = attachmentPrompt(cr.Attachment.Prompt)

	case nonTurnTypes[cr.Type]:
		rec.Speaker, rec.SpeakerClass = "system", stream.SpeakerSystem
		if cr.Type == "summary" {
			rec.Text = cr.Summary
		}
		// The session name is carried as metadata rather than text: it was
		// authored by the harness, and putting it in Text would offer it to
		// anything that measures characters.
		if cr.AITitle != "" {
			rec.Metadata[stream.MetaStreamName] = cr.AITitle
		}

	default:
		rec.Speaker, rec.SpeakerClass = cr.Type, stream.SpeakerUnknown
	}
}

// commandOutput is the local mode for shell and slash-command output. It is not
// an operator input mode: nothing about it was authored by the operator.
const commandOutput = "command_output"

// cliWrapperTags map the CLI's wrapper tags to what the record is evidence of.
// The tags are Claude Code's, so they live in this file with the rest of the
// Claude-specific vocabulary.
var cliWrapperTags = []struct {
	open, close string
	mode        string
}{
	{"<bash-input>", "</bash-input>", stream.InputShellEscape},
	{"<bash-stdout>", "</bash-stdout>", commandOutput},
	{"<bash-stderr>", "</bash-stderr>", commandOutput},
	{"<local-command-stdout>", "</local-command-stdout>", commandOutput},
	{"<local-command-stderr>", "</local-command-stderr>", commandOutput},
	{"<command-name>", "</command-name>", stream.InputCLICommand},
	{"<task-notification>", "</task-notification>", commandOutput},
}

// interruptMarker is the line the CLI writes when the operator interrupts.
const interruptMarker = "[Request interrupted by user"

// cliWrapper reports which wrapper tag a user record carries and the text
// inside it. An unwrapped record returns an empty mode and the text unchanged.
func cliWrapper(text string) (mode, body string) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, interruptMarker) {
		return stream.InputInterrupt, trimmed
	}
	for _, w := range cliWrapperTags {
		if !strings.HasPrefix(trimmed, w.open) {
			continue
		}
		inner := strings.TrimPrefix(trimmed, w.open)
		if i := strings.Index(inner, w.close); i >= 0 {
			inner = inner[:i]
		}
		return w.mode, inner
	}
	return "", text
}

// operatorAnswers extracts the operator's answers to an AskUserQuestion from a
// tool result, when the result is linked to a call of that tool.
//
// Answers arrive as a JSON object keyed by the agent's question. A JSON object
// has no order, so the answers are joined in lexical order of those keys, which
// is not the order the questions were asked or the order the source presents
// them. The source does not carry a question order to preserve; lexical order is
// chosen only because a replay of the same file must produce the same text, and
// it is not evidence of anything about the operator's sequence.
//
// Only the answers and the operator's own notes are returned. The questions
// were written by the agent, and counting them as operator serialization would
// charge the operator for the agent's prose.
func operatorAnswers(raw json.RawMessage, m *claudeMessage, toolNameByUseID map[string]string) (string, bool) {
	if len(raw) == 0 || raw[0] != '{' {
		return "", false
	}
	if !linkedTo(m, toolNameByUseID, askUserQuestion) {
		return "", false
	}
	var tur claudeToolUseResult
	if err := json.Unmarshal(raw, &tur); err != nil {
		return "", false
	}
	if len(tur.Answers) == 0 {
		return "", false
	}
	keys := make([]string, 0, len(tur.Answers))
	for k := range tur.Answers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)+len(tur.Annotations))
	for _, k := range keys {
		parts = append(parts, tur.Answers[k])
	}
	notes := make([]string, 0, len(tur.Annotations))
	for _, a := range tur.Annotations {
		if a.Notes != "" {
			notes = append(notes, a.Notes)
		}
	}
	sort.Strings(notes)
	parts = append(parts, notes...)
	return strings.Join(parts, "\n"), true
}

// textOfMessage returns the joined text of a message, ignoring block kinds that
// are not authored text.
func textOfMessage(m *claudeMessage) string {
	text, _, _ := blocksOfMessage(m)
	return text
}

// blocksOfMessage returns the joined text blocks, the number of tool_result
// blocks, and whether any text block carried content.
//
// thinking and tool_use blocks are excluded from text: the first is hidden
// model state, the second is a call, not a message.
func blocksOfMessage(m *claudeMessage) (text string, toolResults int, hasText bool) {
	if m == nil || len(m.Content) == 0 {
		return "", 0, false
	}
	if s, ok := rawStringOK(m.Content); ok {
		return s, 0, strings.TrimSpace(s) != ""
	}
	var blocks []claudeBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return "", 0, false
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, b.Text)
				hasText = true
			}
		case "tool_result":
			toolResults++
			if s, ok := rawStringOK(b.Content); ok {
				parts = append(parts, s)
			} else if nested := nestedText(b.Content); nested != "" {
				parts = append(parts, nested)
			}
		}
	}
	return strings.Join(parts, "\n"), toolResults, hasText
}

// nestedText flattens a tool_result content array of text blocks.
func nestedText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var blocks []claudeBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// toolCalls maps each tool_use identifier in an assistant record to the tool it
// called.
func toolCalls(m *claudeMessage) map[string]string {
	out := make(map[string]string)
	if m == nil || len(m.Content) == 0 {
		return out
	}
	var blocks []claudeBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return out
	}
	for _, b := range blocks {
		if b.Type == "tool_use" && b.ID != "" && b.Name != "" {
			out[b.ID] = b.Name
		}
	}
	return out
}

// linkedTo reports whether a record's tool results answer a call of tool.
//
// A result whose call is not in the map is not linked: the call was never seen,
// so the originating tool is not established and the result is left as tool
// output rather than guessed at.
func linkedTo(m *claudeMessage, toolNameByUseID map[string]string, tool string) bool {
	if m == nil || len(m.Content) == 0 {
		return false
	}
	var blocks []claudeBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" && toolNameByUseID[b.ToolUseID] == tool {
			return true
		}
	}
	return false
}

// toolNames lists the tools an assistant record called, in order.
func toolNames(m *claudeMessage) string {
	if m == nil || len(m.Content) == 0 {
		return ""
	}
	var blocks []claudeBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return ""
	}
	var names []string
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name != "" {
			names = append(names, b.Name)
		}
	}
	return strings.Join(names, ",")
}

func rawString(raw json.RawMessage) string {
	s, _ := rawStringOK(raw)
	return s
}

func rawStringOK(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}
