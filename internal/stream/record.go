// Package stream defines the canonical record every adapter produces and the
// normalization helpers the rest of the program is allowed to use.
//
// Core packages read this shape only. Adapter-native fields stay inside the
// adapter that knows them, except for the canonical metadata keys below.
package stream

import "time"

// SpeakerClass is the canonical role of a record's author.
type SpeakerClass string

const (
	SpeakerHuman   SpeakerClass = "human"
	SpeakerAgent   SpeakerClass = "agent"
	SpeakerTool    SpeakerClass = "tool"
	SpeakerSystem  SpeakerClass = "system"
	SpeakerUnknown SpeakerClass = "unknown"
)

// Metadata carries adapter-supplied facts under canonical keys. Core packages
// read the keys declared here and no others.
type Metadata map[string]string

const (
	// MetaRecordType is the adapter-native record type, verbatim. Retained so
	// an unknown record keeps provenance instead of being dropped.
	MetaRecordType = "record_type"

	// MetaSidechain is "true" when the record belongs to a nested agent stream
	// rather than the operator's own stream. Sidechain human records are not
	// operator turns: the text was written by an agent.
	MetaSidechain = "sidechain"

	// MetaToolName names the tool a record invoked or returned from.
	MetaToolName = "tool_name"

	// MetaInputMode names how an operator-authored record reached the stream.
	// Absent means ordinary typed input.
	MetaInputMode = "input_mode"

	// MetaStreamName is a human-readable name the harness gave the stream. A
	// harness may revise it as the conversation goes, so a reader takes the
	// most recent record carrying the key and not the first.
	MetaStreamName = "stream_name"
)

// Operator input modes. A harness records more than typing under its user
// record type, and the modes differ in what they are evidence of.
const (
	// InputTyped is text the operator typed to the agent.
	InputTyped = "typed"
	// InputStructuredAnswer is the operator's answer to a question the agent
	// asked. The harness may carry it inside tool plumbing; it is still the
	// operator exercising control, and only the answer is theirs.
	InputStructuredAnswer = "structured_answer"
	// InputShellEscape is a command the operator ran in the session's shell.
	// The characters were typed at a shell, not spent steering the agent.
	InputShellEscape = "shell_escape"
	// InputCLICommand is a command aimed at the harness, such as a slash
	// command. Also not serialization to the agent.
	InputCLICommand = "cli_command"
	// InputInterrupt marks a harness-authored record noting that the operator
	// interrupted. The operator authored no text: the marker is evidence that
	// an interruption happened and nothing more.
	InputInterrupt = "interrupt"
	// InputCompactSummary marks the context summary a harness writes when the
	// conversation is compacted. The operator asked for the compaction; the
	// harness composed the text, so none of it is operator serialization.
	InputCompactSummary = "compact_summary"
)

// operatorInputModes are the modes that count as operator serialization aimed
// at the agent.
var operatorInputModes = map[string]bool{
	"":                    true,
	InputTyped:            true,
	InputStructuredAnswer: true,
}

// SourceRef locates a record in the bytes it came from.
type SourceRef struct {
	Adapter      string `json:"adapter"`
	Path         string `json:"path,omitempty"`
	Offset       int64  `json:"offset,omitempty"`
	RecordSHA256 string `json:"record_sha256"`
}

// Record is the canonical stream record. Text is the adapter's extraction of
// the record's operator- or agent-authored content, preserved byte for byte.
// Normalization is derived at use, never stored here.
type Record struct {
	StreamID  string
	TurnID    string
	Seq       uint64
	Timestamp time.Time

	Speaker      string
	SpeakerClass SpeakerClass
	Text         string

	// Actions are the things the record establishes the agent did, in source
	// order. They are observed, not classified: the adapter read them out of the
	// source and nothing interpreted them.
	//
	// Text and Actions answer different questions. Text is what was said;
	// Actions are what was done. A rule that asks whether the agent repaired a
	// file has been asking Text, and the answer was never in there.
	Actions []Action

	ReplyTo  string
	Source   SourceRef
	Metadata Metadata
}

// IsOperatorTurn reports whether the record is operator serialization aimed at
// the agent.
//
// A human record inside a sidechain was authored by an agent, so it does not
// count. Neither does a shell escape or a slash command: the operator typed
// those characters at the harness, and charging them to the cost of steering
// the agent would inflate every transmission metric.
func (r Record) IsOperatorTurn() bool {
	return r.SpeakerClass == SpeakerHuman &&
		r.Metadata[MetaSidechain] != "true" &&
		operatorInputModes[r.Metadata[MetaInputMode]]
}
