// Package labeler produces candidate labels from a model.
//
// Everything it emits is a CANDIDATE. A model's output is not ground truth, and
// this package never calls it that: labels carry the model's name and the pass
// they came from, so a calibration run scored against them says in its own run
// record what judged the corpus.
//
// Two passes are run per family, and they are made to differ. A second pass that
// is byte-identical to the first reports perfect agreement and measures nothing;
// what agreement is meant to test is whether the codebook decides the case, so
// the passes see the units in different orders, in different batches, and under
// differently worded instructions carrying the same rules. Where they disagree,
// the codebook did not decide.
package labeler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/calibrate"
	"github.com/TGPSKI/flow-indicator/internal/labels"
)

// Client talks to an OpenAI-compatible chat completions endpoint. No provider
// SDK is used, for the same reason the classifier uses none.
type Client struct {
	Endpoint string
	Model    string
	APIKey   string
	HTTP     *http.Client
}

// New returns a client for endpoint and model.
func New(endpoint, model, apiKey string) *Client {
	return &Client{
		Endpoint: endpoint,
		Model:    model,
		APIKey:   apiKey,
		HTTP:     &http.Client{Timeout: 10 * time.Minute},
	}
}

// Pass names one labeling pass. The two differ in unit order, batch boundaries
// and instruction wording, so their agreement is evidence about the codebook
// rather than about the sampler.
type Pass struct {
	Name string
	// Reverse presents the units back to front. Batch boundaries fall in
	// different places, so no unit is judged beside the same neighbours twice.
	Reverse bool
	// Framing selects the wording of the instruction.
	Framing int
}

// Passes are the two runs made per family.
var Passes = []Pass{
	{Name: "pass1", Framing: 0},
	{Name: "pass2", Reverse: true, Framing: 1},
}

// batchSize is how many units travel in one request.
//
// It is a throughput choice with a correctness bound: too many and the model
// starts losing track of the numbering, which shows up as a short or misindexed
// reply and is rejected rather than repaired. Twenty-five sentences is well
// inside what the model keeps straight and is one request per two seconds at
// this endpoint.
const batchSize = 25

// Options configure one labeling run.
type Options struct {
	Family string
	Units  []calibrate.Unit
	Pass   Pass
	// Progress is called after each batch with the number of units done.
	Progress func(done, total int)
	// Abandoned is called once at the end of a pass if any units came back
	// unlabeled. A shortfall visible only as a smaller file is a shortfall
	// nobody notices until a kappa is computed over the wrong denominator.
	Abandoned func(units, total int)
}

// Label runs one pass over one family's units and returns candidate labels.
//
// A batch whose reply cannot be read is retried once and then abandoned: its
// units come back unlabeled rather than guessed at. A unit nobody labeled is
// visible in the coverage counts; a unit labeled by a malformed reply is not.
func (c *Client) Label(ctx context.Context, opts Options) ([]labels.Label, error) {
	units := append([]calibrate.Unit(nil), opts.Units...)
	if opts.Pass.Reverse {
		for i, j := 0, len(units)-1; i < j; i, j = i+1, j-1 {
			units[i], units[j] = units[j], units[i]
		}
	}

	var out []labels.Label
	var done, abandoned int
	for start := 0; start < len(units); start += batchSize {
		end := min(start+batchSize, len(units))
		batch := units[start:end]

		got, err := c.labelBatch(ctx, opts.Family, opts.Pass, batch)
		if err != nil {
			// Abandoned. The units stay unlabeled and the caller sees the
			// shortfall in the counts.
			abandoned += len(batch)
			done += len(batch)
			if opts.Progress != nil {
				opts.Progress(done, len(units))
			}
			continue
		}
		out = append(out, got...)
		done += len(batch)
		if opts.Progress != nil {
			opts.Progress(done, len(units))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Record != out[j].Record {
			return out[i].Record < out[j].Record
		}
		return out[i].Start < out[j].Start
	})
	if abandoned > 0 && opts.Abandoned != nil {
		opts.Abandoned(abandoned, len(units))
	}
	return out, nil
}

// labelBatch labels one batch, retrying once and splitting when the reply ran
// out of room.
//
// A truncated reply is not a model that cannot follow the schema; it is a batch
// whose answer did not fit. Retrying the same request reproduces it exactly, so
// the batch is halved instead, down to a single unit. A unit that cannot be
// answered inside the token limit on its own is the one case where giving up is
// the right answer.
func (c *Client) labelBatch(ctx context.Context, family string, pass Pass, batch []calibrate.Unit) ([]labels.Label, error) {
	got, err := c.batch(ctx, family, pass, batch)
	if err == nil {
		return got, nil
	}
	if errors.Is(err, errTruncated) && len(batch) > 1 {
		half := len(batch) / 2
		left, lerr := c.labelBatch(ctx, family, pass, batch[:half])
		right, rerr := c.labelBatch(ctx, family, pass, batch[half:])
		if lerr != nil && rerr != nil {
			return nil, fmt.Errorf("labeler: both halves failed: %w", lerr)
		}
		return append(left, right...), nil
	}
	return c.batch(ctx, family, pass, batch)
}

// reply is the schema a batch must come back in.
type reply struct {
	Labels []struct {
		I     int    `json:"i"`
		Label string `json:"label"`
		Kind  string `json:"kind"`
		Why   string `json:"why"`
	} `json:"labels"`
}

// batch labels one group of units.
func (c *Client) batch(ctx context.Context, family string, pass Pass, units []calibrate.Unit) ([]labels.Label, error) {
	prompt, err := renderPrompt(family, pass, units)
	if err != nil {
		return nil, err
	}
	raw, err := c.complete(ctx, systemFor(family, pass), prompt)
	if err != nil {
		return nil, err
	}
	var r reply
	if err := json.Unmarshal([]byte(extractJSON(raw)), &r); err != nil {
		return nil, fmt.Errorf("labeler: reply is not the schema: %w", err)
	}

	vocab := vocabularyOf(family)
	labeler := c.Model + "/" + pass.Name
	out := make([]labels.Label, 0, len(r.Labels))
	seen := make(map[int]struct{}, len(r.Labels))
	for _, l := range r.Labels {
		if l.I < 1 || l.I > len(units) {
			return nil, fmt.Errorf("labeler: reply index %d outside batch of %d", l.I, len(units))
		}
		if _, ok := seen[l.I]; ok {
			return nil, fmt.Errorf("labeler: reply labels unit %d more than once", l.I)
		}
		seen[l.I] = struct{}{}
		value := strings.ToLower(strings.TrimSpace(l.Label))
		if !vocab[value] {
			return nil, fmt.Errorf("labeler: reply labels unit %d with unknown class %q", l.I, l.Label)
		}
		u := units[l.I-1]
		note := strings.TrimSpace(l.Why)
		if value == labels.Unlabelable && note == "" {
			note = "the labeler gave no reason; a unit marked unlabelable without one is not a finding"
		}
		out = append(out, labels.Label{
			Session: u.Session,
			Record:  u.Record,
			Family:  family,
			Start:   u.Start,
			End:     u.End,
			Label:   value,
			Kind:    strings.ToLower(strings.TrimSpace(l.Kind)),
			Labeler: labeler,
			Text:    u.Text,
			Note:    note,
		})
	}
	if len(seen) != len(units) {
		return nil, fmt.Errorf("labeler: reply labeled %d of %d units", len(seen), len(units))
	}
	return out, nil
}

// complete sends one chat completion and returns the message content.
func (c *Client) complete(ctx context.Context, system, user string) (string, error) {
	body := map[string]any{
		"model": c.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		// Zero temperature is not determinism across passes — the passes differ
		// in wording and order — but it removes sampling noise from within one.
		"temperature": 0,
		"max_tokens":  4096,
		// Reasoning traces cost tokens and latency and change nothing about a
		// classification task with a fixed vocabulary.
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("labeler: request: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("labeler: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("labeler: endpoint returned %s: %s", resp.Status, excerpt(string(payload), 200))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", fmt.Errorf("labeler: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("labeler: endpoint returned no choices")
	}
	// A reply cut off at the token limit parses as malformed JSON, which reads
	// like a model that cannot follow the schema. It is not: it is a batch too
	// large for the answer to fit. Naming it is what lets the caller shrink the
	// batch instead of retrying the same request.
	if reason := out.Choices[0].FinishReason; reason == "length" {
		return "", errTruncated
	} else if reason != "" && reason != "stop" {
		return "", fmt.Errorf("labeler: endpoint stopped for %q", reason)
	}
	if strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("labeler: endpoint returned an empty message")
	}
	return out.Choices[0].Message.Content, nil
}

// errTruncated marks a reply the model ran out of room to finish.
var errTruncated = errors.New("labeler: reply hit the token limit")

// extractJSON pulls the JSON object out of a reply that may be fenced or
// prefaced. It is not a repair: a reply with no object still fails to parse.
func extractJSON(s string) string {
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if j := strings.IndexByte(rest, '\n'); j >= 0 {
			rest = rest[j+1:]
		}
		if k := strings.Index(rest, "```"); k >= 0 {
			s = rest[:k]
		} else {
			s = rest
		}
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return s
	}
	return s[start : end+1]
}

// vocabularyOf is the set of labels a family accepts, mirroring the label
// package's own check so a bad class is dropped here rather than failing a load
// much later.
func vocabularyOf(family string) map[string]bool {
	switch family {
	case labels.FamilyObligation:
		return map[string]bool{labels.Directive: true, labels.Description: true, labels.Task: true,
			labels.Unlabelable: true}
	case labels.FamilyObligationPair:
		return map[string]bool{labels.Repeat: true, labels.Supersedes: true, labels.Releases: true,
			labels.Unrelated: true, labels.Unlabelable: true}
	case labels.FamilyRecovery:
		return map[string]bool{labels.Opens: true, labels.Deepens: true, labels.Unrelated: true, labels.Unlabelable: true}
	case labels.FamilyPollution:
		return map[string]bool{labels.Clean: true, labels.Polluted: true, labels.Failed: true,
			labels.Unknown: true, labels.Unlabelable: true}
	default:
		return nil
	}
}

func excerpt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
