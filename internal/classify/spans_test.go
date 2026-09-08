package classify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// A semantic classifier decides every bucket share through its segment offsets.
// These tests fix what the projector will accept from one.

// modelServer returns an endpoint that answers every call with body.
func modelServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": body}}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func classifyWith(t *testing.T, body, text string) (Result, error) {
	t.Helper()
	srv := modelServer(t, body)
	c := NewOpenAI(srv.URL, "test-model", "")
	return c.Classify(context.Background(), Input{Turn: operator(text)})
}

func TestDisableThinkingIsExplicitInRequest(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "{}"}}}})
	}))
	defer srv.Close()
	c := NewOpenAI(srv.URL, "qwen", "")
	c.DisableThinking = true
	if _, err := c.request(context.Background(), Input{Turn: operator("test")}); err != nil {
		t.Fatal(err)
	}
	if enabled, ok := got.ChatTemplateKwargs["enable_thinking"]; !ok || enabled {
		t.Fatalf("chat_template_kwargs = %#v, want enable_thinking false", got.ChatTemplateKwargs)
	}
}

func TestSemanticSpansRejectOverlap(t *testing.T) {
	const text = "Revert the workflow. Add the backoff test."
	body := `{"segments":[{"label":"correction","start":0,"end":25},{"label":"forward_work","start":20,"end":42}],
	  "pointer":{"is_pointer":false,"type":"unknown","text":""},
	  "correction":{"is_correction":true,"target_type":"unknown","target_key":""},
	  "obligations":[],"repair":{"target_repaired":null,"new_scope":0,"new_tasks":0,"new_validation":0,"new_constraints":0},
	  "confidence":0.9}`
	_, err := classifyWith(t, body, text)
	if err == nil {
		t.Fatal("overlapping spans were accepted; the same characters would be counted twice")
	}
	if !strings.Contains(err.Error(), "inside or before") {
		t.Errorf("error does not name the overlap: %v", err)
	}
}

func TestSemanticSpansRejectOutOfOrder(t *testing.T) {
	const text = "Revert the workflow. Add the backoff test."
	body := `{"segments":[{"label":"forward_work","start":21,"end":42},{"label":"correction","start":0,"end":20}],
	  "pointer":{"is_pointer":false,"type":"unknown","text":""},
	  "correction":{"is_correction":false,"target_type":"unknown","target_key":""},
	  "obligations":[],"repair":{"target_repaired":null,"new_scope":0,"new_tasks":0,"new_validation":0,"new_constraints":0},
	  "confidence":0.9}`
	if _, err := classifyWith(t, body, text); err == nil {
		t.Fatal("out-of-order spans were accepted")
	}
}

func TestSemanticSpansRejectOffsetsOutsideTheTurn(t *testing.T) {
	const text = "Revert the workflow."
	body := `{"segments":[{"label":"correction","start":0,"end":9999}],
	  "pointer":{"is_pointer":false,"type":"unknown","text":""},
	  "correction":{"is_correction":true,"target_type":"unknown","target_key":""},
	  "obligations":[],"repair":{"target_repaired":null,"new_scope":0,"new_tasks":0,"new_validation":0,"new_constraints":0},
	  "confidence":0.5}`
	if _, err := classifyWith(t, body, text); err == nil {
		t.Fatal("offsets past the end of the turn were accepted")
	}
}

func TestSemanticSpansRejectSplitRunes(t *testing.T) {
	const text = "Revert the naïve workflow."
	body := `{"segments":[{"label":"correction","start":0,"end":14}],
	  "pointer":{"is_pointer":false,"type":"unknown","text":""},
	  "correction":{"is_correction":true,"target_type":"unknown","target_key":""},
	  "obligations":[],"repair":{"target_repaired":null,"new_scope":0,"new_tasks":0,"new_validation":0,"new_constraints":0},
	  "confidence":0.5}`
	if _, err := classifyWith(t, body, text); err == nil {
		t.Fatal("offsets that split a UTF-8 character were accepted")
	}
}

// Text the model skipped stays in the denominator as OTHER. Dropping it would
// raise every share the model did label.
func TestUncoveredTextBecomesOther(t *testing.T) {
	const text = "Revert the workflow. Add the backoff test."
	body := `{"segments":[{"label":"forward_work","start":21,"end":41}],
	  "pointer":{"is_pointer":false,"type":"unknown","text":""},
	  "correction":{"is_correction":false,"target_type":"unknown","target_key":""},
	  "obligations":[],"repair":{"target_repaired":null,"new_scope":0,"new_tasks":0,"new_validation":0,"new_constraints":0},
	  "confidence":0.9}`
	res, err := classifyWith(t, body, text)
	if err != nil {
		t.Fatal(err)
	}
	var covered int
	prevEnd := 0
	for _, s := range res.Segments {
		if s.Start != prevEnd {
			t.Fatalf("segments do not tile the turn: gap at %d", prevEnd)
		}
		prevEnd = s.End
		covered += s.Chars
	}
	if prevEnd != len(text) {
		t.Errorf("segments cover %d of %d bytes", prevEnd, len(text))
	}
	if covered != len([]rune(text)) {
		t.Errorf("segment characters = %d, want %d", covered, len([]rune(text)))
	}
	var other int
	for _, s := range res.Segments {
		if s.Label == LabelOther {
			other += s.Chars
		}
	}
	if other == 0 {
		t.Error("the uncovered sentence disappeared instead of becoming OTHER")
	}
}

// Records that carry text but no semantic surface are never sent.
func TestIrrelevantRecordsAreNotSent(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewOpenAI(srv.URL, "test-model", "")

	toolResult := stream.Record{
		SpeakerClass: stream.SpeakerTool,
		Text:         strings.Repeat("tool output line\n", 400),
		Metadata:     stream.Metadata{},
	}
	if _, err := c.Classify(context.Background(), Input{Turn: toolResult}); err != nil {
		t.Fatalf("a tool result produced an error instead of being skipped: %v", err)
	}
	shellEscape := stream.Record{
		SpeakerClass: stream.SpeakerHuman,
		Text:         "printf 'SHELL=%s\\n' \"$SHELL\"",
		Metadata:     stream.Metadata{stream.MetaInputMode: stream.InputShellEscape},
	}
	if _, err := c.Classify(context.Background(), Input{Turn: shellEscape}); err != nil {
		t.Fatalf("a shell escape produced an error instead of being skipped: %v", err)
	}
	system := stream.Record{
		SpeakerClass: stream.SpeakerSystem,
		Text:         "[Request interrupted by user]",
		Metadata:     stream.Metadata{},
	}
	if _, err := c.Classify(context.Background(), Input{Turn: system}); err != nil {
		t.Fatalf("a system record produced an error instead of being skipped: %v", err)
	}
	if calls != 0 {
		t.Errorf("%d requests were sent for records with no semantic surface", calls)
	}
}

// An agent record's semantic fields are folded only into an open correction
// response cycle. With no episode open there is nothing for the model's answer
// to reach, so the record stays on the heuristic path.
func TestAgentRecordsOutsideARepairAreNotSent(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewOpenAI(srv.URL, "test-model", "")

	speaking := stream.Record{
		SpeakerClass: stream.SpeakerAgent,
		Text:         "Added the retry loop and also updated the release workflow.",
		Metadata:     stream.Metadata{},
	}
	res, err := c.Classify(context.Background(), Input{Turn: speaking})
	if err != nil {
		t.Fatalf("an agent record with no episode open produced an error instead of being skipped: %v", err)
	}
	if calls != 0 {
		t.Errorf("%d requests were sent for an agent record with no episode open", calls)
	}
	if res.Provenance.Classifier != (Heuristic{}).Name() {
		t.Errorf("provenance names %s, want the heuristic path", res.Provenance.Classifier)
	}

	// With an episode open the same record is sent: its repair claim and
	// expansion counts now reach the cycle.
	if _, err := c.Classify(context.Background(), Input{
		Turn:         speaking,
		ActiveRepair: &RepairRef{ID: "rep-1", Depth: 1, Status: "open"},
	}); err == nil {
		t.Fatal("an agent record inside an open episode was not sent")
	}
	if calls != 1 {
		t.Errorf("requests sent for an agent record inside an open episode = %d, want 1", calls)
	}
}

// A turn past the size bound keeps its heuristic classification and says so.
func TestOversizeTurnIsNotSent(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewOpenAI(srv.URL, "test-model", "")

	huge := operator("No. " + strings.Repeat("x", MaxTurnBytes))
	res, err := c.Classify(context.Background(), Input{Turn: huge})
	if err == nil {
		t.Fatal("an oversize turn was sent without comment")
	}
	if calls != 0 {
		t.Errorf("%d requests were sent for an oversize turn", calls)
	}
	if !res.Correction.IsCorrection {
		t.Error("the heuristic classification was not kept")
	}
}

func TestDecodeModelOutputRejectsTrailingJSON(t *testing.T) {
	_, err := decodeModelOutput([]byte(`{"segments":[],"pointer":{"is_pointer":false,"type":"unknown","text":""},"correction":{"is_correction":false,"target_type":"unknown","target_key":""},"obligations":[],"resolutions":[],"repair":{"target_repaired":null,"new_scope":0,"new_tasks":0,"new_validation":0,"new_constraints":0},"confidence":0} {}`))
	if err == nil || !strings.Contains(err.Error(), "more than one JSON value") {
		t.Fatalf("error = %v, want trailing JSON rejection", err)
	}
}

func TestValidateRejectsUnsafeSemanticValues(t *testing.T) {
	base := modelOutput{}
	base.Pointer.Type = PointerUnknown
	base.Correction.TargetType = "unknown"
	cases := []struct {
		name string
		mut  func(*modelOutput)
	}{
		{"negative expansion", func(out *modelOutput) { out.Repair.NewScope = -1 }},
		{"unknown pointer", func(out *modelOutput) { out.Pointer.Type = "invented" }},
		{"non-pointer text", func(out *modelOutput) { out.Pointer.Text = "internal/a.go" }},
		{"unknown correction target", func(out *modelOutput) { out.Correction.IsCorrection = true; out.Correction.TargetType = "invented" }},
		{"non-correction target key", func(out *modelOutput) { out.Correction.TargetKey = "internal/a.go" }},
		{"duplicate resolutions", func(out *modelOutput) {
			out.Resolutions = append(out.Resolutions,
				struct {
					Key      string `json:"key"`
					Kind     string `json:"kind"`
					Evidence string `json:"evidence"`
				}{Key: "k", Kind: ResolutionReleased, Evidence: "operator withdrew it"},
				struct {
					Key      string `json:"key"`
					Kind     string `json:"kind"`
					Evidence string `json:"evidence"`
				}{Key: "k", Kind: ResolutionReleased, Evidence: "operator withdrew it"},
			)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := base
			tc.mut(&out)
			outstanding := map[string]struct{}{"k": {}}
			if err := validate(out, "text", outstanding); err == nil {
				t.Fatal("unsafe model output was accepted")
			}
		})
	}
}

func TestSemanticHashIncludesEndpoint(t *testing.T) {
	a := NewOpenAI("http://127.0.0.1:8000/v1/chat/completions", "local", "")
	b := NewOpenAI("http://127.0.0.1:9000/v1/chat/completions", "local", "")
	if a.Hash() == b.Hash() {
		t.Fatal("two local runtimes with the same model name share classifier provenance")
	}
}

func TestSemanticHashIncludesThinkingMode(t *testing.T) {
	baseline := NewOpenAI("http://127.0.0.1:8000/v1/chat/completions", "qwen", "")
	direct := NewOpenAI("http://127.0.0.1:8000/v1/chat/completions", "qwen", "")
	direct.DisableThinking = true
	if baseline.Hash() == direct.Hash() {
		t.Fatal("thinking request mode did not change classifier provenance")
	}
}
