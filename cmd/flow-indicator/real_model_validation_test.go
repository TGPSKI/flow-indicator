package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/harness"
	"github.com/TGPSKI/flow-indicator/internal/render"
	"github.com/TGPSKI/flow-indicator/internal/state"
	"github.com/TGPSKI/flow-indicator/internal/store"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

type modelValidation struct {
	classify.Classifier
	Outcomes []classify.Completion
}

type schemaOrderTransport struct{ base http.RoundTripper }

func (s schemaOrderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	format := body["response_format"].(map[string]any)
	schema := format["json_schema"].(map[string]any)["schema"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	var ordered bytes.Buffer
	ordered.WriteByte('{')
	for i, key := range []string{"segments", "pointer", "correction", "obligations", "resolutions", "repair", "confidence"} {
		if i > 0 {
			ordered.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		value, _ := json.Marshal(properties[key])
		ordered.Write(name)
		ordered.WriteByte(':')
		ordered.Write(value)
	}
	ordered.WriteByte('}')
	schema["properties"] = json.RawMessage(ordered.Bytes())
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	req.ContentLength = int64(len(raw))
	return s.base.RoundTrip(req)
}

func TestRealSchemaOrder(t *testing.T) {
	path := os.Getenv("FLOW_INDICATOR_TEST_MODEL_CONFIG")
	if path == "" {
		t.Skip("actual model opt-in")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Classifier.ConstrainedJSON = true
	for _, ordered := range []bool{false, true} {
		cls, err := semanticClassifierFor(cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		if ordered {
			cls.(*classify.OpenAI).Client.Transport = schemaOrderTransport{http.DefaultTransport}
		}
		for _, text := range []string{"No, revert the changes to parser.go.", "Use @docs/PLAN.md for the next step.", "Implement the parser."} {
			r, err := cls.Classify(context.Background(), classify.Input{Turn: stream.Record{Seq: 1, SpeakerClass: stream.SpeakerHuman, Text: text}})
			t.Logf("ordered=%t text=%q correction=%t pointer=%t error=%v", ordered, text, r.Correction.IsCorrection, r.Pointer.IsPointer, err)
		}
	}
}

func TestRealHybridWatch(t *testing.T) {
	configPath := os.Getenv("FLOW_INDICATOR_TEST_MODEL_CONFIG")
	if configPath == "" {
		t.Skip("set FLOW_INDICATOR_TEST_MODEL_CONFIG and FLOW_INDICATOR_TEST_SOURCE_COPY")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Classifier.Mode, cfg.Classifier.ConstrainedJSON = config.ModeHybrid, true
	common := commonFlags{configPath: configPath}
	rules, err := common.rules()
	if err != nil {
		t.Fatal(err)
	}
	cls, err := classifierFor(cfg, rules, true)
	if err != nil {
		t.Fatal(err)
	}
	hybrid := cls.(*classify.Hybrid)
	defer hybrid.Close()
	raw, err := os.ReadFile(os.Getenv("FLOW_INDICATOR_TEST_SOURCE_COPY"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 32504875 {
		t.Fatal("copy shorter than audited prefix")
	}
	raw = raw[:32504875]
	root, err := os.MkdirTemp("", "flow-indicator-real-hybrid-")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source-copy.jsonl")
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	recs, err := harness.DecodeBytes("claude-code", source, "", "actual-hybrid", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual hybrid artifacts: %s; profile=%s classifier=%s/%s/%s", root, hybrid.Markers.Hash(), hybrid.Semantic().Name(), hybrid.Semantic().Version(), hybrid.Semantic().Hash())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- watchFile(ctx, cfg, root, "claude-code", cls, source, "actual-hybrid", "", 0, false, display{micro: true})
	}()
	dir := store.SessionDir(root, "actual-hybrid")
	for {
		es, readErr := store.ReadKind(dir, event.KindRecordObserved)
		if readErr == nil && len(es) == len(recs) {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("bootstrap did not finish")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if hybrid.Operational().Requested != 0 {
		t.Fatal("bootstrap sent model requests")
	}
	for i, text := range []string{"Only edit parser.go.", "Drop the parser restriction.", "Keep the literal \\n in the output.\n"} {
		f, err := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fmt.Fprintf(f, "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":%q}}\n", text)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		for {
			s := hybrid.Operational()
			if s.Completed+s.Failed+s.TimedOut >= i+1 && s.Applied == s.Completed {
				break
			}
			if ctx.Err() != nil {
				t.Fatalf("actual request/publication did not finish: %+v", s)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	time.Sleep(1100 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	loaded, err := render.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := loaded.Semantic
	t.Logf("actual hybrid requested=%d validated=%d failed=%d timed_out=%d dropped=%d applied=%d changed=%d", s.Requested, s.Validated, s.Failed, s.TimedOut, s.Dropped, s.Applied, s.Changed)
	if s.Requested != 3 || s.Validated != 3 || s.Applied != 3 || s.Pending != 0 {
		t.Fatalf("actual hybrid coverage = %+v", s)
	}
}

func (v *modelValidation) Classify(ctx context.Context, in classify.Input) (classify.Result, error) {
	start := time.Now()
	r, err := v.Classifier.Classify(ctx, in)
	if (in.Turn.IsOperatorTurn() || in.Turn.SpeakerClass == stream.SpeakerAgent && in.ActiveRepair != nil) && in.Turn.Text != "" && len(in.Turn.Text) <= classify.MaxTurnBytes {
		c := classify.Completion{StreamID: in.Turn.StreamID, Seq: in.Turn.Seq, Source: in.Turn.Source,
			InputHash: classify.InputHash(in), Classifier: v.Name(), ClassifierVersion: v.Version(), ClassifierHash: v.Hash(),
			OperatorTurn: in.Turn.IsOperatorTurn(), Requested: true, LatencyMS: time.Since(start).Milliseconds(), Status: classify.CompletionCompleted}
		c.JobID = event.IDWithIdentity(c.StreamID, c.Seq, event.KindSemanticCompleted, c.InputHash+v.Hash(), event.Version)
		if err != nil {
			c.Status, c.Error = classify.CompletionFailed, err.Error()
		} else {
			c.Result, c.Confidence = &r, &r.Confidence
		}
		v.Outcomes = append(v.Outcomes, c)
	}
	return r, err
}

// This opt-in test measures actual endpoint validation, not ground-truth accuracy.
func TestRealModelValidation(t *testing.T) {
	configPath := os.Getenv("FLOW_INDICATOR_TEST_MODEL_CONFIG")
	if configPath == "" {
		t.Skip("set FLOW_INDICATOR_TEST_MODEL_CONFIG and FLOW_INDICATOR_TEST_SOURCE_COPY for actual model validation")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(os.Getenv("FLOW_INDICATOR_TEST_SOURCE_COPY"))
	if err != nil {
		t.Fatal(err)
	}
	const auditBytes = 32504875
	if len(raw) < auditBytes {
		t.Fatal("source copy is shorter than audited prefix")
	}
	root, err := os.MkdirTemp("", "flow-indicator-real-model-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("validation artifacts: %s", root)
	for _, constrained := range []bool{false, true} {
		name := "unconstrained"
		if constrained {
			name = "constrained"
		}
		t.Run(name, func(t *testing.T) {
			cfg.Classifier.ConstrainedJSON = constrained
			cls, err := semanticClassifierFor(cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			v := &modelValidation{Classifier: cls}
			for i, text := range []string{"Implement the parser.", "Only edit parser.go.", "Only edit café.go.\n", "No, revert the changes to parser.go.", "Drop the parser restriction.", "Keep the literal \\n in the output."} {
				in := classify.Input{Turn: stream.Record{StreamID: "probe", Seq: uint64(i + 1), SpeakerClass: stream.SpeakerHuman, Text: text}}
				if i == 4 {
					in.UnresolvedObligations = []classify.ObligationRef{{ID: "obl-6", Key: "only edit parser.go", Kind: classify.ObligationScope, Text: "Only edit parser.go."}}
				}
				_, err := v.Classify(context.Background(), in)
				t.Logf("probe %d validated=%t error=%v", i+1, err == nil, err)
			}
			if err := store.WriteJSON(filepath.Join(root, name+"-probes.json"), v.Outcomes); err != nil {
				t.Fatal(err)
			}
			v.Outcomes = nil
			records, err := harness.DecodeBytes("claude-code", "copied-audit-prefix", "", name, raw[:auditBytes])
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.OpenSession(root, name, false)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			p := state.New(cfg, v, name)
			for _, rec := range records {
				if err := session.AppendAll(p.Push(context.Background(), rec)); err != nil {
					t.Fatal(err)
				}
			}
			if err := session.AppendAll(p.Finish()); err != nil {
				t.Fatal(err)
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			if err := writeProjections(session.Dir); err != nil {
				t.Fatal(err)
			}
			if err := store.WriteJSON(filepath.Join(root, name+"-outcomes.json"), v.Outcomes); err != nil {
				t.Fatal(err)
			}
			validated := 0
			for _, c := range v.Outcomes {
				if c.Status == classify.CompletionCompleted {
					validated++
				}
			}
			t.Logf("classifier=%s/%s/%s requests=%d validated=%d rejected=%d", v.Name(), v.Version(), v.Hash(), len(v.Outcomes), validated, len(v.Outcomes)-validated)
			summary := map[string]any{"classifier": v.Name(), "version": v.Version(), "hash": v.Hash(), "requests": len(v.Outcomes), "validated": validated, "rejected": len(v.Outcomes) - validated, "source_bytes": auditBytes, "source_records": len(records)}
			if err := store.WriteJSON(filepath.Join(root, name+"-validation.json"), summary); err != nil {
				t.Fatal(err)
			}
		})
	}
}
