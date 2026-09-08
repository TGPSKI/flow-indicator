package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/harness"
	"github.com/TGPSKI/flow-indicator/internal/state"
	"github.com/TGPSKI/flow-indicator/internal/store"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

type contractTransport func(*http.Request) (*http.Response, error)

func (f contractTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type captureContractInput struct {
	classify.Classifier
	markers classify.Heuristic
	seq     uint64
	input   classify.Input
}

func (c *captureContractInput) Classify(ctx context.Context, in classify.Input) (classify.Result, error) {
	if in.Turn.Seq == c.seq {
		c.input = in
	}
	return c.markers.Classify(ctx, in)
}

// Reconstruct a first-live-request failure after marker-only bootstrap from
// copies. Matching the retained input hash prevents testing a different context.
func TestRealCapturedContract(t *testing.T) {
	incident := os.Getenv("FLOW_INDICATOR_TEST_FAILURE_COPY")
	if incident == "" {
		t.Skip("set FLOW_INDICATOR_TEST_FAILURE_COPY, FLOW_INDICATOR_TEST_SOURCE_COPY and FLOW_INDICATOR_TEST_MODEL_CONFIG")
	}
	path := os.Getenv("FLOW_INDICATOR_TEST_MODEL_CONFIG")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	common := commonFlags{configPath: path}
	rules, err := common.rules()
	if err != nil {
		t.Fatal(err)
	}
	cls, err := semanticClassifierFor(cfg, rules)
	if err != nil {
		t.Fatal(err)
	}
	var responseContent string
	cls.(*classify.OpenAI).Client.Transport = contractTransport(func(req *http.Request) (*http.Response, error) {
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		var wire struct {
			Choices []struct{ Message struct{ Content string } }
		}
		if err := json.Unmarshal(raw, &wire); err == nil && len(wire.Choices) > 0 {
			responseContent = wire.Choices[0].Message.Content
		}
		return resp, nil
	})
	completions, err := readSemanticCompletions(incident)
	if err != nil {
		t.Fatal(err)
	}
	if len(completions) == 0 || completions[0].Status != classify.CompletionFailed {
		t.Fatal("copy has no first-request failure")
	}
	failed := completions[0]
	observed, err := store.ReadKind(incident, event.KindRecordObserved)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(os.Getenv("FLOW_INDICATOR_TEST_SOURCE_COPY"))
	if err != nil {
		t.Fatal(err)
	}
	records, err := harness.DecodeBytes(failed.Source.Adapter, failed.Source.Path, "", failed.StreamID, raw)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[uint64]stream.SourceRef{}
	for _, e := range observed {
		sources[e.Seq] = e.Source
	}
	capture := &captureContractInput{Classifier: cls, markers: classify.Heuristic{Rules: rules}, seq: failed.Seq}
	p := state.New(cfg, capture, failed.StreamID)
	for _, rec := range records {
		if rec.Seq > failed.Seq {
			break
		}
		rec.Source = sources[rec.Seq]
		p.Push(context.Background(), rec)
	}
	if got := classify.InputHash(capture.input); got != failed.InputHash {
		t.Fatalf("reconstructed context hash %s != recorded %s", got, failed.InputHash)
	}
	for attempt := 0; attempt < 3; attempt++ {
		result, err := cls.Classify(context.Background(), capture.input)
		if err != nil {
			t.Errorf("captured request attempt %d: %v; response=%s", attempt+1, err, responseContent)
			continue
		}
		raw, _ := json.Marshal(result.Repair)
		t.Logf("captured input=%s classifier=%s/%s/%s attempt=%d repair=%s", failed.InputHash, cls.Name(), cls.Version(), cls.Hash(), attempt+1, raw)
	}
}
