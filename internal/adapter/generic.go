package adapter

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Generic decodes JSONL where each line is one turn. Only stream_id, seq,
// speaker and text are read; unknown fields are ignored.
type Generic struct{}

func (Generic) Name() string { return "generic" }

type genericRecord struct {
	StreamID  string `json:"stream_id"`
	TurnID    string `json:"turn_id"`
	Seq       uint64 `json:"seq"`
	Timestamp string `json:"timestamp"`
	Speaker   string `json:"speaker"`
	Text      string `json:"text"`
	ReplyTo   string `json:"reply_to"`
	Sidechain bool   `json:"sidechain"`
	// Root is the session root action targets are made relative to. A source
	// that already writes relative targets omits it.
	Root string `json:"root"`
	// Actions are what the record establishes the agent did. The generic format
	// is the vocabulary's own format, so a producer states the verb directly
	// rather than being mapped into it.
	Actions []genericAction `json:"actions"`
}

// genericAction is one action as the generic format states it.
type genericAction struct {
	Verb    string   `json:"verb"`
	Targets []string `json:"targets"`
	Argv    []string `json:"argv"`
	Failed  bool     `json:"failed"`
}

// genericVerbs is the vocabulary a generic source may name. A verb outside it
// becomes unknown rather than being accepted: the vocabulary is closed, and a
// source inventing a verb would put targets into a write set on its own say-so.
var genericVerbs = map[string]stream.ActionVerb{
	string(stream.VerbRead):    stream.VerbRead,
	string(stream.VerbWrite):   stream.VerbWrite,
	string(stream.VerbExecute): stream.VerbExecute,
	string(stream.VerbSearch):  stream.VerbSearch,
	string(stream.VerbAsk):     stream.VerbAsk,
}

// genericActions maps the stated actions into the vocabulary.
func genericActions(root string, in []genericAction) []stream.Action {
	if len(in) == 0 {
		return nil
	}
	out := make([]stream.Action, 0, len(in))
	for _, a := range in {
		verb, ok := genericVerbs[a.Verb]
		if !ok {
			verb = stream.VerbUnknown
		}
		act := stream.Action{Verb: verb, Argv: a.Argv, Failed: a.Failed}
		for _, t := range a.Targets {
			if n := stream.NormalizeTarget(root, t); n != "" {
				act.Targets = append(act.Targets, n)
			}
		}
		out = append(out, act)
	}
	return out
}

func (g Generic) Decode(raw []byte, source stream.SourceRef) ([]stream.Record, error) {
	lines := splitLines(raw)
	out := make([]stream.Record, 0, len(lines))
	for i, ls := range lines {
		var gr genericRecord
		if err := json.Unmarshal(ls.bytes, &gr); err != nil {
			return nil, fmt.Errorf("generic: decode record at offset %d: %w", source.Offset+ls.offset, err)
		}
		md := stream.Metadata{}
		if gr.Seq != 0 {
			md["source_seq"] = fmt.Sprintf("%d", gr.Seq)
		}
		if gr.Sidechain {
			md[stream.MetaSidechain] = "true"
		}
		rec := stream.Record{
			StreamID:     gr.StreamID,
			TurnID:       gr.TurnID,
			Seq:          uint64(i + 1),
			Timestamp:    parseTimestamp(gr.Timestamp),
			Speaker:      gr.Speaker,
			SpeakerClass: genericSpeakerClass(gr.Speaker),
			Text:         gr.Text,
			Actions:      genericActions(gr.Root, gr.Actions),
			ReplyTo:      gr.ReplyTo,
			Source:       sourceFor(source, g.Name(), ls),
			Metadata:     md,
		}
		if rec.TurnID == "" {
			rec.TurnID = fmt.Sprintf("seq-%d", rec.Seq)
		}
		out = append(out, rec)
	}
	return out, nil
}

// genericSpeakerClass maps the speaker string. An unrecognized speaker is
// unknown, never guessed into a class that carries metric weight.
func genericSpeakerClass(speaker string) stream.SpeakerClass {
	switch speaker {
	case "user", "human", "operator":
		return stream.SpeakerHuman
	case "assistant", "agent", "model":
		return stream.SpeakerAgent
	case "tool", "tool_result", "function":
		return stream.SpeakerTool
	case "system":
		return stream.SpeakerSystem
	default:
		return stream.SpeakerUnknown
	}
}

// parseTimestamp returns the zero time when the field is absent or unparsable.
// A missing timestamp is unknown, not an error: timing observations degrade,
// nothing else does.
func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
