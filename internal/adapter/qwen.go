package adapter

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Qwen decodes Qwen Code CLI session JSONL. Every Qwen-specific field name in
// this program appears in this file and nowhere else.
//
// The transcript shape descends from the same lineage as Claude Code's — one
// JSONL file per session, a uuid/parentUuid chain, a cwd on every record —
// but the message envelope does not agree with it: parts instead of content
// blocks, functionCall/functionResponse instead of tool_use/tool_result. That
// is enough to earn its own adapter rather than reusing Claude's.
type Qwen struct{}

func (Qwen) Name() string { return "qwen" }

type qwenRecord struct {
	Type       string `json:"type"`
	Subtype    string `json:"subtype"`
	UUID       string `json:"uuid"`
	ParentUUID string `json:"parentUuid"`
	SessionID  string `json:"sessionId"`
	// CWD is the directory the CLI was running in. Action targets are made
	// relative to it, same as every other file-backed harness here.
	CWD       string `json:"cwd"`
	Timestamp string `json:"timestamp"`
	// Provenance says who authored the record on builds that carry it. Older
	// sessions on this machine omit the field entirely; only "real_user" is
	// read as a positive signal, so an absent field falls through to the
	// other tests rather than being misread as the operator.
	Provenance    string          `json:"provenance"`
	IsSidechain   bool            `json:"isSidechain"`
	AgentID       string          `json:"agentId"`
	Message       *qwenMessage    `json:"message"`
	SystemPayload json.RawMessage `json:"systemPayload"`
}

type qwenMessage struct {
	Role  string     `json:"role"`
	Parts []qwenPart `json:"parts"`
}

// qwenPart is one element of a message's parts array. Exactly one field is
// populated per part: Text (plain or reasoning), FunctionCall (a tool
// invocation) or FunctionResponse (its result).
type qwenPart struct {
	Text string `json:"text"`
	// Thought marks a reasoning part. It is hidden model state, not something
	// said to anyone, so it is excluded from Text the same way Claude Code's
	// thinking blocks are.
	Thought          bool                  `json:"thought"`
	FunctionCall     *qwenFunctionCall     `json:"functionCall"`
	FunctionResponse *qwenFunctionResponse `json:"functionResponse"`
}

type qwenFunctionCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type qwenFunctionResponse struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Response qwenFunctionResult `json:"response"`
}

type qwenFunctionResult struct {
	Output string `json:"output"`
	Error  string `json:"error"`
}

// qwenToolArgs carries the argument names this adapter reads out of a
// function call. Each is Qwen Code's own field name, so like every other one
// it appears here and nowhere else in the program.
type qwenToolArgs struct {
	FilePath string `json:"file_path"`
	Path     string `json:"path"`
	URL      string `json:"url"`
	Command  string `json:"command"`
}

// qwenToolVerbs maps each Qwen Code tool this adapter recognizes to the
// action vocabulary. A tool absent from the table maps to the unknown verb:
// "agent" spawns a sub-agent thread rather than touching a target, "skill"
// and "todo_write" carry no target this program measures.
var qwenToolVerbs = map[string]stream.ActionVerb{
	"read_file":         stream.VerbRead,
	"list_directory":    stream.VerbRead,
	"web_fetch":         stream.VerbRead,
	"write_file":        stream.VerbWrite,
	"edit":              stream.VerbWrite,
	"run_shell_command": stream.VerbExecute,
	"grep_search":       stream.VerbSearch,
	"glob":              stream.VerbSearch,
	"ask_user_question": stream.VerbAsk,
}

// slashInvocation is the phase of a slash_command system record that carries
// the operator's own typed command. The paired "result" phase is the
// harness's response and is not operator input.
const slashInvocation = "invocation"

type qwenSlashPayload struct {
	Phase      string `json:"phase"`
	RawCommand string `json:"rawCommand"`
}

// Decode reads a chunk of session JSONL into canonical records.
func (q Qwen) Decode(raw []byte, source stream.SourceRef) ([]stream.Record, error) {
	lines := splitLines(raw)
	out := make([]stream.Record, 0, len(lines))
	// actionByCallID locates the action a function call produced, so a later
	// error response can mark it failed. A response whose call is in an
	// earlier chunk is not in this map, same limitation Codex and Claude Code
	// carry for the same reason: replay hands the whole file to one call, so
	// every failure resolves; a tail split across chunks may leave one call
	// unmarked rather than misreported.
	actionByCallID := make(map[string]actionSite)
	var root string
	for i, ls := range lines {
		var qr qwenRecord
		if err := json.Unmarshal(ls.bytes, &qr); err != nil {
			return nil, fmt.Errorf("qwen: decode record at offset %d: %w", source.Offset+ls.offset, err)
		}
		if root == "" {
			root = qr.CWD
		}
		for _, id := range failedQwenCallIDs(qr.Message) {
			if site, ok := actionByCallID[id]; ok {
				out[site.record].Actions[site.action].Failed = true
			}
		}
		rec := stream.Record{
			StreamID:  qr.SessionID,
			TurnID:    qr.UUID,
			Seq:       uint64(i + 1),
			Timestamp: parseTimestamp(qr.Timestamp),
			ReplyTo:   qr.ParentUUID,
			Source:    sourceFor(source, q.Name(), ls),
			Metadata:  stream.Metadata{stream.MetaRecordType: qr.Type},
		}
		if qr.IsSidechain {
			rec.Metadata[stream.MetaSidechain] = "true"
		}
		if qr.Subtype != "" {
			rec.Metadata["record_subtype"] = qr.Subtype
		}
		q.classify(&qr, &rec)
		if rec.TurnID == "" {
			rec.TurnID = fmt.Sprintf("seq-%d", rec.Seq)
		}
		if qr.Type == "assistant" {
			var ids []string
			rec.Actions, ids = qwenActionsOf(qr.Message, root)
			for j, id := range ids {
				if id != "" {
					actionByCallID[id] = actionSite{record: len(out), action: j}
				}
			}
		}
		out = append(out, rec)
	}
	return out, nil
}

// classify sets speaker, speaker class and text. An unrecognized record type
// is not an error: it becomes an unknown-class record that keeps its
// provenance.
func (q Qwen) classify(qr *qwenRecord, rec *stream.Record) {
	switch {
	case qr.Type == "system" && qr.Subtype == "slash_command":
		var payload qwenSlashPayload
		_ = json.Unmarshal(qr.SystemPayload, &payload)
		if payload.Phase == slashInvocation && payload.RawCommand != "" {
			// The operator typed the command; the paired "result" record is
			// the harness answering it.
			rec.Speaker, rec.SpeakerClass = "user", stream.SpeakerHuman
			rec.Metadata[stream.MetaInputMode] = stream.InputCLICommand
			rec.Text = payload.RawCommand
			return
		}
		rec.Speaker, rec.SpeakerClass = "system", stream.SpeakerSystem

	case qr.Type == "system":
		// ui_telemetry, attribution and file-history snapshots are harness
		// bookkeeping. They are retained with provenance and carry no text
		// and no turn weight.
		rec.Speaker, rec.SpeakerClass = "system", stream.SpeakerSystem

	case qr.Type == "user" && qr.Provenance == "real_user":
		rec.Speaker, rec.SpeakerClass = "user", stream.SpeakerHuman
		rec.Metadata[stream.MetaInputMode] = stream.InputTyped
		rec.Text = qwenText(qr.Message)

	case qr.Type == "user":
		// A user-typed record with no real_user provenance is a sub-agent's
		// task prompt: the harness composed it to spawn the sub-agent, and
		// the operator typed none of it.
		rec.Speaker, rec.SpeakerClass = "system", stream.SpeakerSystem
		rec.Text = qwenText(qr.Message)

	case qr.Type == "assistant":
		rec.Speaker, rec.SpeakerClass = "assistant", stream.SpeakerAgent
		rec.Text = qwenText(qr.Message)
		if names := qwenToolNames(qr.Message); names != "" {
			rec.Metadata[stream.MetaToolName] = names
		}

	case qr.Type == "tool_result":
		rec.Speaker, rec.SpeakerClass = "tool", stream.SpeakerTool
		rec.Text = qwenToolResultText(qr.Message)

	default:
		rec.Speaker, rec.SpeakerClass = qr.Type, stream.SpeakerUnknown
	}
}

// qwenText joins a message's authored text parts. Reasoning parts (Thought)
// are excluded: they are hidden model state, not something said to anyone.
func qwenText(m *qwenMessage) string {
	if m == nil {
		return ""
	}
	var parts []string
	for _, p := range m.Parts {
		if p.Thought || p.Text == "" {
			continue
		}
		parts = append(parts, p.Text)
	}
	return strings.Join(parts, "\n")
}

// qwenToolResultText joins the output or error text of a tool_result
// message's function responses.
func qwenToolResultText(m *qwenMessage) string {
	if m == nil {
		return ""
	}
	var parts []string
	for _, p := range m.Parts {
		fr := p.FunctionResponse
		if fr == nil {
			continue
		}
		if fr.Response.Output != "" {
			parts = append(parts, fr.Response.Output)
		} else if fr.Response.Error != "" {
			parts = append(parts, fr.Response.Error)
		}
	}
	return strings.Join(parts, "\n")
}

// failedQwenCallIDs lists the function-call identifiers a tool_result record
// answers with an error.
func failedQwenCallIDs(m *qwenMessage) []string {
	if m == nil {
		return nil
	}
	var out []string
	for _, p := range m.Parts {
		fr := p.FunctionResponse
		if fr != nil && fr.Response.Error != "" && fr.ID != "" {
			out = append(out, fr.ID)
		}
	}
	return out
}

// qwenActionsOf reads the tool calls of one assistant message into the
// action vocabulary, in source order, and returns the call identifier of
// each so a later error response can be attributed to the call it answers.
func qwenActionsOf(m *qwenMessage, root string) ([]stream.Action, []string) {
	if m == nil {
		return nil, nil
	}
	var actions []stream.Action
	var ids []string
	for _, p := range m.Parts {
		fc := p.FunctionCall
		if fc == nil || fc.Name == "" {
			continue
		}
		verb, ok := qwenToolVerbs[fc.Name]
		if !ok {
			verb = stream.VerbUnknown
		}
		var args qwenToolArgs
		if len(fc.Args) > 0 {
			// A malformed args payload leaves the action with its verb and no
			// targets. The call still happened; only what it named is
			// unavailable.
			_ = json.Unmarshal(fc.Args, &args)
		}
		a := stream.Action{Verb: verb}
		for _, t := range []string{args.FilePath, args.Path, args.URL} {
			if norm := stream.NormalizeTarget(root, t); norm != "" {
				a.Targets = append(a.Targets, norm)
			}
		}
		if args.Command != "" {
			// Qwen Code records a shell string, not an argument vector. It is
			// carried whole rather than split: splitting would be a shell
			// parse, and a wrong split is a wrong argv rather than a missing
			// one.
			a.Argv = []string{args.Command}
		}
		actions = append(actions, a)
		ids = append(ids, fc.ID)
	}
	return actions, ids
}

// qwenToolNames lists the tools an assistant record called, in order.
func qwenToolNames(m *qwenMessage) string {
	if m == nil {
		return ""
	}
	var names []string
	for _, p := range m.Parts {
		if p.FunctionCall != nil && p.FunctionCall.Name != "" {
			names = append(names, p.FunctionCall.Name)
		}
	}
	return strings.Join(names, ",")
}
