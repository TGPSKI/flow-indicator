package classify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// PromptVersion versions the one classifier prompt and the context object sent
// with it. There is one prompt and one schema: prompt variation would make
// stored classifications incomparable.
const PromptVersion = "4"

// Prompt is the whole instruction sent to the model.
const Prompt = `You label one turn of an operator/agent transcript for a measurement tool.

Return strict JSON only, matching this schema:
{"segments":[{"label":"","start":0,"end":0}],
 "pointer":{"is_pointer":false,"type":"unknown"},
 "correction":{"is_correction":false,"target_type":"unknown"},
 "obligations":[{"key":"","kind":"","text":""}],
 "resolutions":[{"key":"","kind":"","evidence":""}],
 "repair":{"target_repaired":null,"new_scope":0,"new_tasks":0,"new_validation":0,"new_constraints":0},
 "confidence":0.0}

Rules:
- segment labels: forward_work, new_task, new_evidence, correction, scope_constraint,
  negative_constraint, positive_constraint, stop_condition, meta_process, handoff,
  referent_disambiguation, temporal_disambiguation, namespace_disambiguation,
  restart_reconstruction, restated_prior_state, other.
- start and end are byte offsets into the turn text.
- obligation kinds: stop, scope, negative, positive.
- resolutions report requirements that no longer stand. Each key must be copied
  from unresolved_obligation_candidates. Resolution kinds:
  released (the operator withdrew the requirement),
  superseded (a later requirement replaced this one),
  satisfied (the requirement was met, and something other than the agent's own
  account of its work establishes that).
  An agent stating it did the thing is a claim, not verification: it is never
  satisfied. Return an empty list when nothing in the turn resolves anything.
- evidence is one sentence naming what in the turn establishes the resolution.
- target_repaired is null unless the turn establishes it.
- Report unknown rather than guessing. Do not explain. Do not add fields.`

// MaxTurnBytes bounds the turn text sent for classification. A turn past this
// size keeps its heuristic classification and the log records why: silently
// truncating would move every segment offset the model returned.
const MaxTurnBytes = 32 * 1024

// MaxPriorTurns bounds how much earlier operator text travels with a turn.
// Prior turns are context for referent and restatement questions, and the
// recent ones carry that; the whole rolling window does not.
const MaxPriorTurns = 4

// worthSending reports whether a record has anything for a semantic classifier
// to judge that can still reach a metric.
//
// Tool results and harness bookkeeping contain text, but no classification of
// that text feeds anything: operator serialization and agent repair claims are
// the whole semantic surface. An agent record is narrower still. Its semantic
// fields — the repair claim, verified repair, and the expansion counts — are
// folded only into an open correction response cycle, so an agent record with no
// episode open contributes nothing whatever the model says about it. Sending it
// would buy a request and change no number.
func worthSending(in Input) bool {
	switch {
	case in.Turn.IsOperatorTurn():
		return true
	case in.Turn.SpeakerClass == stream.SpeakerAgent:
		return in.ActiveRepair != nil
	default:
		return false
	}
}

// OpenAI is a classifier that talks to any OpenAI-compatible chat completions
// endpoint. No provider SDK is used.
//
// Marker-level facts (reset, stop, acceptance, near repeats) stay with the
// heuristic classifier; the model supplies the semantic fields. Provenance
// records both.
type OpenAI struct {
	Endpoint string
	Model    string
	APIKey   string
	Client   *http.Client
	Markers  Heuristic
}

// NewOpenAI returns a classifier for endpoint and model.
func NewOpenAI(endpoint, model, apiKey string) *OpenAI {
	return &OpenAI{
		Endpoint: endpoint,
		Model:    model,
		APIKey:   apiKey,
		Client:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (o *OpenAI) Name() string    { return "openai-compatible" }
func (o *OpenAI) Version() string { return PromptVersion }

// Capabilities is every fact the prompt asks for. The model is given the
// outstanding inventory and the open episode and is asked to judge both, so it
// can reach the verification questions the marker tier cannot. Whether it
// answers any given turn is a separate matter: the schema keeps every one of
// them nullable.
func (o *OpenAI) Capabilities() Capabilities {
	return Capabilities{
		CapVerifiedRepair,
		CapObligationRelease,
		CapObligationSupersession,
		CapObligationSatisfaction,
	}
}

func (o *OpenAI) Hash() string {
	// Model names are not artifact identities: two local runtimes can expose
	// the same name with different weights or templates. The endpoint is part
	// of the classifier identity so those outputs are never silently merged.
	h := sha256.Sum256([]byte(PromptVersion + "\x00" + o.Endpoint + "\x00" + o.Model + "\x00" + Prompt))
	return hex.EncodeToString(h[:])[:16]
}

// turnPayload is the context object sent with the prompt.
type turnPayload struct {
	Turn struct {
		Seq     uint64 `json:"seq"`
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
	} `json:"turn"`
	PriorTurns            []string        `json:"prior_turns"`
	UnresolvedObligations []ObligationRef `json:"unresolved_obligation_candidates"`
	ActiveRepair          *RepairRef      `json:"active_repair"`
}

// modelOutput is the strict schema the model must return.
type modelOutput struct {
	Segments []struct {
		Label SegmentLabel `json:"label"`
		Start int          `json:"start"`
		End   int          `json:"end"`
	} `json:"segments"`
	Pointer struct {
		IsPointer bool        `json:"is_pointer"`
		Type      PointerType `json:"type"`
		Text      string      `json:"text"`
	} `json:"pointer"`
	Correction struct {
		IsCorrection bool   `json:"is_correction"`
		TargetType   string `json:"target_type"`
		TargetKey    string `json:"target_key"`
	} `json:"correction"`
	Obligations []struct {
		Key  string `json:"key"`
		Kind string `json:"kind"`
		Text string `json:"text"`
	} `json:"obligations"`
	Resolutions []struct {
		Key      string `json:"key"`
		Kind     string `json:"kind"`
		Evidence string `json:"evidence"`
	} `json:"resolutions"`
	Repair struct {
		TargetRepaired *bool `json:"target_repaired"`
		NewScope       int   `json:"new_scope"`
		NewTasks       int   `json:"new_tasks"`
		NewValidation  int   `json:"new_validation"`
		NewConstraints int   `json:"new_constraints"`
	} `json:"repair"`
	Confidence float64 `json:"confidence"`
}

// Classify sends one turn and folds the response into a Result. Invalid output
// is rejected and returned as an error; the projector records the failure and
// keeps going. Malformed JSON is never repaired with a second model call.
func (o *OpenAI) Classify(ctx context.Context, in Input) (Result, error) {
	base, err := o.Markers.Classify(ctx, in)
	if err != nil {
		return Result{}, err
	}
	if in.Turn.Text == "" || !worthSending(in) {
		return base, nil
	}
	if n := len(in.Turn.Text); n > MaxTurnBytes {
		return base, fmt.Errorf("openai: turn %d is %d bytes, over the %d-byte limit; kept the heuristic classification",
			in.Turn.Seq, n, MaxTurnBytes)
	}

	raw, err := o.request(ctx, in)
	if err != nil {
		return base, err
	}
	out, err := decodeModelOutput(raw)
	if err != nil {
		return base, err
	}
	outstanding := make(map[string]struct{}, len(in.UnresolvedObligations))
	for _, ob := range in.UnresolvedObligations {
		outstanding[ob.Key] = struct{}{}
	}
	if err := validate(out, in.Turn.Text, outstanding); err != nil {
		return base, err
	}

	res := base
	res.Segments = tile(out, in.Turn.Text)
	res.Pointer = Pointer{
		IsPointer: out.Pointer.IsPointer,
		Type:      out.Pointer.Type,
		Text:      out.Pointer.Text,
		Chars:     len([]rune(out.Pointer.Text)),
	}
	res.Correction = Correction{
		IsCorrection: out.Correction.IsCorrection,
		TargetType:   out.Correction.TargetType,
		TargetKey:    out.Correction.TargetKey,
	}
	res.Obligations = nil
	for _, ob := range out.Obligations {
		key := ob.Key
		if key == "" {
			key = stream.Normalize(ob.Text)
		}
		res.Obligations = append(res.Obligations, ObligationCandidate{Key: key, Kind: ob.Kind, Text: ob.Text})
	}
	// The marker tier's releases are kept only when the model returned none.
	// Both tiers answer the same question, and the model saw the whole
	// inventory; letting them merge would apply one resolution twice under two
	// different premises.
	res.Resolutions = nil
	for _, r := range out.Resolutions {
		res.Resolutions = append(res.Resolutions, ObligationResolution{
			Key: r.Key, Kind: r.Kind, Evidence: r.Evidence,
		})
	}
	if len(res.Resolutions) == 0 {
		res.Resolutions = base.Resolutions
	}
	res.Repair = RepairSignal{
		// The marker classifier saw whether the agent claimed the repair. The
		// model is asked for verification, which is a different question, so
		// both answers are kept.
		ClaimedRepaired: base.Repair.ClaimedRepaired,
		TargetRepaired:  out.Repair.TargetRepaired,
		NewScope:        out.Repair.NewScope,
		NewTasks:        out.Repair.NewTasks,
		NewValidation:   out.Repair.NewValidation,
		NewConstraints:  out.Repair.NewConstraints,
	}
	res.Confidence = out.Confidence
	res.Provenance = Provenance{
		Classifier: o.Name(),
		Version:    o.Version(),
		Hash:       o.Hash(),
		SourceTurn: in.Turn.Seq,
		Confidence: out.Confidence,
		Markers:    fmt.Sprintf("%s/%s", o.Markers.Name(), o.Markers.Version()),
	}
	return res, nil
}

// decodeModelOutput accepts one strict JSON object and nothing after it.
// A second JSON value would make the classifier response ambiguous: no event
// can name which of two competing interpretations was applied.
func decodeModelOutput(raw []byte) (modelOutput, error) {
	var out modelOutput
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return modelOutput{}, fmt.Errorf("openai: decode classifier output: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return modelOutput{}, fmt.Errorf("openai: classifier output carried more than one JSON value")
		}
		return modelOutput{}, fmt.Errorf("openai: decode trailing classifier output: %w", err)
	}
	return out, nil
}

// validate rejects output that does not fit the turn it claims to describe.
//
// Segment offsets decide every bucket share, so a model that overlaps two spans
// or hands back spans out of order would inflate a numerator against an
// unchanged denominator. Rather than repair such output, reject it: the turn
// keeps the heuristic classification and the log records the failure.
func validate(out modelOutput, text string, outstanding map[string]struct{}) error {
	if math.IsNaN(out.Confidence) || math.IsInf(out.Confidence, 0) || out.Confidence < 0 || out.Confidence > 1 {
		return fmt.Errorf("openai: confidence %v outside [0,1]", out.Confidence)
	}
	prevEnd := 0
	for i, s := range out.Segments {
		if s.Start < 0 || s.End > len(text) || s.Start > s.End {
			return fmt.Errorf("openai: segment %d offsets [%d,%d] outside turn of %d bytes", i, s.Start, s.End, len(text))
		}
		if s.Start < prevEnd {
			return fmt.Errorf("openai: segment %d starts at %d, inside or before segment %d which ended at %d", i, s.Start, i-1, prevEnd)
		}
		if !boundary(text, s.Start) || !boundary(text, s.End) {
			return fmt.Errorf("openai: segment %d offsets [%d,%d] split a UTF-8 character", i, s.Start, s.End)
		}
		if s.Label.Bucket() == BucketOther && s.Label != LabelOther {
			return fmt.Errorf("openai: segment %d has unknown label %q", i, s.Label)
		}
		prevEnd = s.End
	}
	if !validPointerType(out.Pointer.Type) {
		return fmt.Errorf("openai: pointer has unknown type %q", out.Pointer.Type)
	}
	if !out.Pointer.IsPointer && (out.Pointer.Text != "" || out.Pointer.Type != PointerUnknown) {
		return fmt.Errorf("openai: non-pointer carries type %q or text", out.Pointer.Type)
	}
	if out.Correction.IsCorrection && !validPointerType(PointerType(out.Correction.TargetType)) {
		return fmt.Errorf("openai: correction has unknown target type %q", out.Correction.TargetType)
	}
	if !out.Correction.IsCorrection && (out.Correction.TargetKey != "" || (out.Correction.TargetType != "" && out.Correction.TargetType != "unknown")) {
		return fmt.Errorf("openai: non-correction carries target type %q or key", out.Correction.TargetType)
	}
	if out.Repair.NewScope < 0 || out.Repair.NewTasks < 0 || out.Repair.NewValidation < 0 || out.Repair.NewConstraints < 0 {
		return fmt.Errorf("openai: repair expansion counts must be non-negative")
	}
	obligationKeys := make(map[string]struct{}, len(out.Obligations))
	for i, ob := range out.Obligations {
		switch ob.Kind {
		case ObligationStop, ObligationScope, ObligationNegative, ObligationPositive:
		default:
			return fmt.Errorf("openai: obligation %d has unknown kind %q", i, ob.Kind)
		}
		key := ob.Key
		if key == "" {
			key = stream.Normalize(ob.Text)
		}
		if key == "" {
			return fmt.Errorf("openai: obligation %d has no key or text", i)
		}
		if _, ok := obligationKeys[key]; ok {
			return fmt.Errorf("openai: obligation %d duplicates key %q", i, key)
		}
		obligationKeys[key] = struct{}{}
	}
	// A resolution removes a requirement from the inventory, so it is held to
	// the strictest check of anything here: it must name a candidate the model
	// was shown, and it must state what established it. A resolution with no
	// premise is the failure this vocabulary exists to catch, and the projector
	// would have nothing to store as its license.
	resolved := make(map[string]struct{}, len(out.Resolutions))
	for i, r := range out.Resolutions {
		switch r.Kind {
		case ResolutionReleased, ResolutionSuperseded, ResolutionSatisfied:
		default:
			return fmt.Errorf("openai: resolution %d has unknown kind %q", i, r.Kind)
		}
		if r.Key == "" {
			return fmt.Errorf("openai: resolution %d names no obligation key", i)
		}
		if _, ok := outstanding[r.Key]; !ok {
			return fmt.Errorf("openai: resolution %d names %q, which is not an outstanding candidate", i, r.Key)
		}
		if strings.TrimSpace(r.Evidence) == "" {
			return fmt.Errorf("openai: resolution %d of %q states no evidence", i, r.Key)
		}
		if _, ok := resolved[r.Key]; ok {
			return fmt.Errorf("openai: resolution %d duplicates key %q", i, r.Key)
		}
		resolved[r.Key] = struct{}{}
	}
	return nil
}

func validPointerType(t PointerType) bool {
	switch t {
	case PointerNode, PointerAlias, PointerFile, PointerPath, PointerTask,
		PointerTemporal, PointerQuote, PointerNamespace, PointerRelation,
		PointerOperation, PointerUnknown:
		return true
	default:
		return false
	}
}

// boundary reports whether i is a rune boundary of text. len(text) is one.
func boundary(text string, i int) bool {
	return i == len(text) || utf8.RuneStart(text[i])
}

// tile turns validated segments into spans that cover the turn exactly once.
//
// Text the model left uncovered becomes OTHER rather than disappearing. The
// alternative is a denominator that shrinks whenever the model skips a
// sentence, which would raise every share it did label.
func tile(out modelOutput, text string) []Segment {
	var segs []Segment
	at := 0
	add := func(label SegmentLabel, start, end int) {
		if start >= end {
			return
		}
		segs = append(segs, Segment{
			Label: label, Start: start, End: end,
			Chars: utf8.RuneCountInString(text[start:end]),
		})
	}
	for _, s := range out.Segments {
		add(LabelOther, at, s.Start)
		add(s.Label, s.Start, s.End)
		at = s.End
	}
	add(LabelOther, at, len(text))
	return segs
}

type chatRequest struct {
	Model       string        `json:"model"`
	Temperature float64       `json:"temperature"`
	Messages    []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// request posts one turn and returns the model's raw content.
func (o *OpenAI) request(ctx context.Context, in Input) ([]byte, error) {
	var payload turnPayload
	payload.Turn.Seq = in.Turn.Seq
	payload.Turn.Speaker = string(in.Turn.SpeakerClass)
	payload.Turn.Text = in.Turn.Text
	payload.PriorTurns = in.PriorOperatorText
	if len(payload.PriorTurns) > MaxPriorTurns {
		payload.PriorTurns = payload.PriorTurns[len(payload.PriorTurns)-MaxPriorTurns:]
	}
	payload.UnresolvedObligations = in.UnresolvedObligations
	payload.ActiveRepair = in.ActiveRepair

	turnContext, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal turn context: %w", err)
	}
	body, err := json.Marshal(chatRequest{
		Model:       o.Model,
		Temperature: 0,
		Messages: []chatMessage{
			{Role: "system", Content: Prompt},
			{Role: "user", Content: string(turnContext)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: build request for %s: %w", o.Endpoint, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: post %s: %w", o.Endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: post %s: status %d", o.Endpoint, resp.StatusCode)
	}
	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("openai: decode response from %s: %w", o.Endpoint, err)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("openai: response from %s carried no choices", o.Endpoint)
	}
	return []byte(strings.TrimSpace(parsed.Choices[0].Message.Content)), nil
}
