package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/TGPSKI/flow-indicator/internal/event"
)

// Inspect renders the drilldown for one turn: where it came from, what was
// observed, what was classified, and what it contributed. This is source
// drilldown, not navigation.
func (s *Session) Inspect(turn uint64) (string, error) {
	var events []event.Event
	for _, e := range s.events {
		if e.Seq == turn {
			events = append(events, e)
		}
	}
	if len(events) == 0 {
		return "", fmt.Errorf("render: turn %d not found in session %s", turn, s.StreamID)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "turn %d of stream %s\n\n", turn, s.StreamID)

	src := events[0].Source
	fmt.Fprintf(&b, "source\n  adapter %s\n  path    %s\n  offset  %d\n  sha256  %s\n  epoch   %d\n\n",
		src.Adapter, src.Path, src.Offset, src.RecordSHA256, events[0].Epoch)

	for _, e := range events {
		if e.Kind != event.KindRecordObserved {
			continue
		}
		var p struct {
			TurnID       string          `json:"turn_id"`
			Speaker      string          `json:"speaker"`
			SpeakerClass string          `json:"speaker_class"`
			Timestamp    string          `json:"timestamp"`
			Observation  json.RawMessage `json:"observation"`
			Snippet      string          `json:"snippet"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return "", fmt.Errorf("render: decode observation of turn %d: %w", turn, err)
		}
		fmt.Fprintf(&b, "record\n  turn id %s\n  speaker %s (%s)\n  time    %s\n\n",
			p.TurnID, p.Speaker, p.SpeakerClass, orUnknown(p.Timestamp))
		if p.Snippet != "" {
			fmt.Fprintf(&b, "snippet\n%s\n\n", indent(p.Snippet, "  "))
		}
		fmt.Fprintf(&b, "observed\n%s\n\n", indent(pretty(p.Observation), "  "))
	}

	b.WriteString("classified\n")
	var classified int
	for _, e := range events {
		if e.Class != event.ClassClassified {
			continue
		}
		classified++
		fmt.Fprintf(&b, "  %s\n%s\n", e.Kind, indent(pretty(e.Payload), "    "))
	}
	if classified == 0 {
		b.WriteString("  none\n")
	}

	b.WriteString("\nstate transitions\n")
	var transitions int
	for _, e := range events {
		if e.Class != event.ClassDerived || e.Kind == event.KindMetricsComputed {
			continue
		}
		transitions++
		fmt.Fprintf(&b, "  %s\n%s\n", e.Kind, indent(pretty(e.Payload), "    "))
	}
	if transitions == 0 {
		b.WriteString("  none\n")
	}

	b.WriteString("\ntransition license\n")
	b.WriteString(licenses(events))

	b.WriteString("\nmetric contribution\n")
	var measured bool
	for _, e := range events {
		if e.Kind != event.KindMetricsComputed {
			continue
		}
		measured = true
		fmt.Fprintf(&b, "%s\n", indent(pretty(e.Payload), "  "))
	}
	if !measured {
		b.WriteString("  none: this record is not an operator turn, so no metrics were computed for it\n")
	}
	return b.String(), nil
}

// licenses lists, for each derived transition of a turn, the premise the
// projector required before concluding it.
//
// A transition whose premise is weaker than its conclusion is the defect this
// section exists to expose, so the premise is printed next to the transition
// rather than left to be reconstructed from the projector's source. A transition
// that prints no premise is one to look at.
func licenses(events []event.Event) string {
	var b strings.Builder
	var n int
	for _, e := range events {
		if e.Class != event.ClassDerived || e.Kind == event.KindMetricsComputed {
			continue
		}
		var p struct {
			License  string `json:"license"`
			Evidence string `json:"evidence"`
			Reason   string `json:"reason"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		premise := firstNonEmpty(p.License, p.Evidence, p.Reason)
		if premise == "" {
			premise = "none stored with this transition"
		}
		n++
		fmt.Fprintf(&b, "  %s\n    %s\n", e.Kind, premise)
	}
	if n == 0 {
		return "  none: this record derived no state transition\n"
	}
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func pretty(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func readFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("render: read %s: %w", path, err)
	}
	return raw, nil
}
