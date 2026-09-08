package classify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

func TestSemanticObligationIdentity(t *testing.T) {
	for _, key := range []string{"obligation_1", "obl-6", "obl-14", "only edit parser.go"} {
		t.Run(key, func(t *testing.T) {
			raw := `{"pointer":{"type":"unknown"},"obligations":[{"key":` + strconvJSON(key) + `,"kind":"scope","text":"Only edit parser.go."}]}`
			out, err := decodeModelOutput([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			if err := validate(out, "Only edit parser.go.", nil); err == nil {
				t.Fatal("invented identity accepted")
			}
			if key == "only edit parser.go" {
				if err := validate(out, "Only edit parser.go.", map[string]struct{}{key: {}}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	for _, text := range []string{"Only edit parser.go.", "Only edit worker.go."} {
		out, err := decodeModelOutput([]byte(`{"pointer":{"type":"unknown"},"obligations":[{"key":"","kind":"scope","text":` + strconvJSON(text) + `}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := validate(out, text, nil); err != nil {
			t.Fatal(err)
		}
	}
	out, _ := decodeModelOutput([]byte(`{"pointer":{"type":"unknown"},"obligations":[{"key":"","kind":"scope","text":"invented requirement"}]}`))
	if validate(out, "Only edit parser.go.", nil) == nil {
		t.Fatal("new identity without source span accepted")
	}
}

func strconvJSON(s string) string { raw, _ := json.Marshal(s); return string(raw) }

type schemaTransport func(*http.Request) (*http.Response, error)

func (f schemaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSemanticContextAndConstrainedSchema(t *testing.T) {
	o := NewOpenAI("http://model.invalid/v1/chat/completions", "test", "")
	o.ConstrainedJSON = true
	in := Input{Turn: stream.Record{Seq: 2, SpeakerClass: stream.SpeakerHuman, Text: "Drop the parser restriction.\n"}, UnresolvedObligations: []ObligationRef{{ID: "obl-6", Key: "only edit parser.go", Kind: ObligationScope, Text: "Only edit parser.go."}}, ActiveRepair: &RepairRef{ID: "rep-1", TargetKey: "parser.go"}}
	o.Client = &http.Client{Transport: schemaTransport(func(r *http.Request) (*http.Response, error) {
		var body chatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.ResponseFormat["type"] != "json_schema" {
			t.Fatal("missing constrained decoding")
		}
		text := body.Messages[1].Content
		if strings.Contains(text, "obl-6") || strings.Contains(text, `"Key"`) {
			t.Fatalf("display identity leaked: %s", text)
		}
		var payload turnPayload
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Turn.ByteLength != len(in.Turn.Text) || payload.ActiveRepair.TargetKey != "parser.go" {
			t.Fatalf("context = %+v", payload)
		}
		response := `{"pointer":{"type":"unknown"},"resolutions":[{"key":` + strconvJSON(payload.UnresolvedObligations[0].Key) + `,"kind":"released","evidence":"Operator withdrew the restriction."}]}`
		wire := `{"choices":[{"message":{"content":` + strconvJSON(response) + `}}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(wire)), Header: make(http.Header)}, nil
	})}
	res, err := o.Classify(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resolutions) != 1 || res.Resolutions[0].Key != "only edit parser.go" {
		t.Fatalf("resolution = %+v", res.Resolutions)
	}
	if res.Provenance.Version != "5" {
		t.Fatal("old prompt identity")
	}
}

func TestSemanticOffsetsAndCorrectionVocabulary(t *testing.T) {
	for _, text := range []string{"ASCII", "café", "x\n", "x\\n"} {
		out, _ := decodeModelOutput([]byte(`{"pointer":{"type":"unknown"},"segments":[{"label":"other","start":0,"end":0}]}`))
		out.Segments[0].End = len(text)
		if err := validate(out, text, nil); err != nil {
			t.Fatal(err)
		}
		out.Segments[0].End++
		if validate(out, text, nil) == nil {
			t.Fatal("out-of-range segment accepted")
		}
	}
	for _, target := range []string{"obligation", "behavior", "response", "scope", "tool", "file_path", "", "FILE"} {
		out, _ := decodeModelOutput([]byte(`{"pointer":{"type":"unknown"},"correction":{"is_correction":true}}`))
		out.Correction.TargetType = target
		if validate(out, "No.", nil) == nil {
			t.Fatalf("target %q accepted", target)
		}
	}
}
