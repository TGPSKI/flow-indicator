package calibrate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/harness"
	"github.com/TGPSKI/flow-indicator/internal/labels"
	"github.com/TGPSKI/flow-indicator/internal/state"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Options is one calibration run.
type Options struct {
	Manifest *Manifest
	Labels   *labels.Set
	Config   config.Config
	// Classifier is the evidence producer under evaluation. Nil preserves the
	// shipped heuristic baseline, so calibration callers must opt in before a
	// local model can spend an endpoint request.
	Classifier classify.Classifier
	Split      string
	// Reason is why the holdout was opened. It is required for a holdout run and
	// ignored otherwise: an evaluation that spends budget has to say what gate
	// it was spent at.
	Reason string
}

// Report is one calibration run's whole result.
//
// The first block is what the run was about. Nothing below it means anything
// without it, which is why they travel in one structure and are printed
// together.
type Report struct {
	Artifact      string `json:"artifact_sha256"`
	ManifestHash  string `json:"corpus_manifest_sha256"`
	LabelHash     string `json:"label_set_sha256"`
	RuleVersion   string `json:"rule_version"`
	RuleHash      string `json:"rule_hash"`
	Classifier    string `json:"classifier"`
	ProfileHash   string `json:"profile_hash"`
	PromptVersion string `json:"prompt_version,omitempty"`
	PromptHash    string `json:"prompt_sha256,omitempty"`

	Split    string   `json:"split"`
	Sessions []string `json:"sessions"`
	Domains  []string `json:"domains"`
	Reason   string   `json:"holdout_reason,omitempty"`

	Scores         []labels.Score      `json:"scores"`
	Discriminators []Discriminator     `json:"discriminators"`
	Agreements     []labels.Agreement  `json:"label_agreement"`
	Budget         classify.Budget     `json:"budget"`
	Coverage       map[string]Coverage `json:"coverage"`
}

// Coverage is how much of what the build predicted the labels reach. A score
// over four labeled units is a number about four units, and a report that hides
// the denominator invites it to be read as a number about the family.
type Coverage struct {
	PredictedUnits int `json:"predicted_units"`
	LabeledUnits   int `json:"labeled_units"`
	Unlabelable    int `json:"unlabelable_units"`
}

// Discriminator is one structural test scored on its own, before any are
// combined. Knowing what each contributes is the difference between a rule
// whose parts are understood and one whose parts are assumed.
type Discriminator struct {
	Name  string       `json:"name"`
	Score labels.Score `json:"score"`
}

// Run replays the selected sessions and scores them against the labels.
func Run(ctx context.Context, opts Options) (*Report, error) {
	sessions := opts.Manifest.Select(opts.Split)
	if len(sessions) == 0 {
		return nil, fmt.Errorf("calibrate: the manifest holds no session on the %s split", opts.Split)
	}
	if opts.Split == SplitHoldout && opts.Reason == "" {
		return nil, fmt.Errorf("calibrate: opening the holdout spends budget and requires a reason naming the gate")
	}
	if err := opts.Manifest.Verify(sessions); err != nil {
		return nil, err
	}

	var predictions []labels.Prediction
	classifier := opts.Classifier
	if classifier == nil {
		classifier = classify.Heuristic{}
	}
	var ids []string
	for _, s := range sessions {
		ps, err := predict(ctx, opts.Config, opts.Manifest, s, classifier)
		if err != nil {
			return nil, err
		}
		predictions = append(predictions, ps...)
		ids = append(ids, s.ID)
	}

	// Only the labels for the sessions on this split are in scope. Scoring a dev
	// run against a holdout session's labels would open the holdout by accident.
	inScope := make(map[string]bool, len(ids))
	for _, id := range ids {
		inScope[id] = true
	}
	var scoped []labels.Label
	for _, l := range opts.Labels.Labels {
		if l.Session == "" || inScope[l.Session] {
			scoped = append(scoped, l)
		}
	}

	rep := &Report{
		Artifact:     ArtifactHash(),
		ManifestHash: opts.Manifest.SHA256,
		LabelHash:    opts.Labels.SHA256,
		RuleVersion:  classifier.Version(),
		RuleHash:     classifier.Hash(),
		Classifier:   classifier.Name(),
		Split:        opts.Split,
		Sessions:     ids,
		Domains:      Domains(sessions),
		Reason:       opts.Reason,
		// The budget is a property of the rules in force, so it is read off the
		// classifier under evaluation. Reading the shipped baseline instead
		// reported an overlay run's budget as the baseline's, which is the one
		// number in the record that would not have named its own subject.
		Budget:   budgetOf(classifier),
		Coverage: map[string]Coverage{},
	}

	switch c := classifier.(type) {
	case classify.Heuristic:
		rep.ProfileHash = c.Hash()
	case *classify.OpenAI:
		rep.ProfileHash = c.Markers.Hash()
		rep.PromptVersion = c.Version()
		rep.PromptHash = c.PromptHash()
	}
	for _, family := range []string{
		labels.FamilyObligation,
		labels.FamilyObligationPair,
		labels.FamilyRecovery,
		labels.FamilyPollution,
	} {
		pairs := labels.Align(scoped, predictions, family)
		score := labels.Evaluate(family, pairs)
		rep.Scores = append(rep.Scores, score)
		rep.Coverage[family] = Coverage{
			PredictedUnits: countPredictions(predictions, family),
			LabeledUnits:   score.Labeled,
			Unlabelable:    score.Unlabelable,
		}
	}

	rep.Discriminators = scoreDiscriminators(scoped, predictions)
	rep.Agreements = agreements(opts.Labels)
	return rep, nil
}

// scoreDiscriminators scores each structural test on its own against the
// obligation labels, using the verdict each recorded at prediction time.
//
// The shipped rule combines two of them. Scoring each alone is what makes the
// combination a decision rather than an assumption, and it is what will say
// whether the code-density measure earns a threshold or stays inert.
func scoreDiscriminators(ls []labels.Label, ps []labels.Prediction) []Discriminator {
	names := map[string]struct{}{}
	for _, p := range ps {
		if p.Family != labels.FamilyObligation {
			continue
		}
		for k := range p.Detail {
			names[k] = struct{}{}
		}
	}
	sorted := make([]string, 0, len(names))
	for k := range names {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	out := make([]Discriminator, 0, len(sorted))
	for _, name := range sorted {
		alt := make([]labels.Prediction, 0, len(ps))
		for _, p := range ps {
			if p.Family != labels.FamilyObligation {
				continue
			}
			v, ok := p.Detail[name]
			if !ok {
				continue
			}
			q := p
			q.Value = v
			q.Detail = nil
			alt = append(alt, q)
		}
		out = append(out, Discriminator{
			Name:  name,
			Score: labels.Evaluate(labels.FamilyObligation, labels.Align(ls, alt, labels.FamilyObligation)),
		})
	}
	return out
}

// agreements computes Cohen's kappa between every pair of passes, per family.
func agreements(set *labels.Set) []labels.Agreement {
	var out []labels.Agreement
	for _, family := range set.Families() {
		ls := set.Labelers(family)
		for i := 0; i < len(ls); i++ {
			for j := i + 1; j < len(ls); j++ {
				out = append(out, labels.Kappa(set.Labels, family, ls[i], ls[j]))
			}
		}
	}
	return out
}

func countPredictions(ps []labels.Prediction, family string) int {
	var n int
	for _, p := range ps {
		if p.Family == family {
			n++
		}
	}
	return n
}

// recorder wraps a classifier and keeps every result, so predictions can be read
// without the projector knowing calibration exists.
type recorder struct {
	inner   classify.Classifier
	results map[string]classify.Result
	order   []string
}

func newRecorder(inner classify.Classifier) *recorder {
	return &recorder{inner: inner, results: map[string]classify.Result{}}
}

func (r *recorder) Name() string                        { return r.inner.Name() }
func (r *recorder) Version() string                     { return r.inner.Version() }
func (r *recorder) Hash() string                        { return r.inner.Hash() }
func (r *recorder) Capabilities() classify.Capabilities { return r.inner.Capabilities() }

func (r *recorder) Classify(ctx context.Context, in classify.Input) (classify.Result, error) {
	res, err := r.inner.Classify(ctx, in)
	if err == nil && in.Turn.TurnID != "" {
		if _, seen := r.results[in.Turn.TurnID]; !seen {
			r.order = append(r.order, in.Turn.TurnID)
		}
		r.results[in.Turn.TurnID] = res
	}
	return res, err
}

// predict replays one session and reads out what the build claimed, in the same
// coordinates the labels use.
func predict(ctx context.Context, cfg config.Config, m *Manifest, s ManifestSession, classifier classify.Classifier) ([]labels.Prediction, error) {
	records, err := harness.OpenPath(s.Adapter, m.Resolve(s), s.Root)
	if err != nil {
		return nil, err
	}

	rec := newRecorder(classifier)
	p := state.New(cfg, rec, s.ID)

	var out []labels.Prediction
	// The projector's transitions carry no record identifier, so each is
	// attributed to the operator turn in play when it was emitted. That is the
	// unit a labeler judges: a repair episode is judged against the turn that
	// opened it, and a pollution assessment against the turn whose cycle it
	// covers.
	var lastOperator string
	byRecord := map[string]stream.Record{}

	for _, r := range records {
		byRecord[r.TurnID] = r
		before := lastOperator
		if r.IsOperatorTurn() {
			lastOperator = r.TurnID
		}
		for _, e := range p.Push(ctx, r) {
			// A settlement emitted while folding an operator turn covers the
			// cycle that ended with the previous turn, so it is attributed
			// there. Everything else belongs to the turn now in play.
			at := lastOperator
			if r.IsOperatorTurn() && settlesPriorCycle(e.Kind) {
				at = before
			}
			out = appendStatePrediction(out, s.ID, at, e)
		}
	}
	for _, e := range p.Finish() {
		out = appendStatePrediction(out, s.ID, lastOperator, e)
	}

	for _, id := range rec.order {
		r, ok := byRecord[id]
		if !ok || !r.IsOperatorTurn() {
			continue
		}
		out = append(out, obligationPredictions(s.ID, id, rec.results[id])...)
	}
	return out, nil
}

// obligationPredictions turns one operator turn's candidates into predictions,
// kept and rejected alike.
//
// A rejected sentence is a prediction of "description", not an absence. The
// build looked at it and decided; recording only what it kept would make every
// correct rejection invisible and every wrong one a silent recall loss.
func obligationPredictions(session, record string, res classify.Result) []labels.Prediction {
	var out []labels.Prediction
	add := func(c classify.ObligationCandidate, value string) {
		out = append(out, labels.Prediction{
			Session: session,
			Record:  record,
			Family:  labels.FamilyObligation,
			Start:   c.Start,
			End:     c.End,
			Value:   value,
			Text:    c.Text,
			Detail:  discriminatorVerdicts(c.Directive),
		})
	}
	for _, c := range res.Obligations {
		add(c, labels.Directive)
	}
	for _, c := range res.Rejected {
		add(c, labels.Description)
	}
	return out
}

// discriminatorVerdicts is what each structural test alone would have concluded
// about a sentence.
//
// Each is stated as the same directive/description vocabulary the labels use, so
// each can be scored with the same scorer and compared to the combined rule
// without a second code path.
func discriminatorVerdicts(d classify.Directive) map[string]string {
	verdict := func(describes bool) string {
		if describes {
			return labels.Description
		}
		return labels.Directive
	}
	return map[string]string{
		"subject_before_marker": verdict(d.SubjectBeforeMarker),
		"not_second_person":     verdict(!d.SecondPerson),
		"contained":             verdict(d.Contained),
		"combined_shipped_rule": verdict(d.Describes()),
	}
}

// settlesPriorCycle reports that a transition emitted while folding an operator
// turn is about the cycle that ended with the previous turn rather than about
// this one. The projector settles pollution before it projects the turn.
func settlesPriorCycle(kind string) bool { return kind == event.KindRepairStatus }

// appendStatePrediction reads the projector's transitions as predictions about
// recovery and pollution.
func appendStatePrediction(out []labels.Prediction, session, record string, e event.Event) []labels.Prediction {
	if record == "" {
		return out
	}
	switch e.Kind {
	case event.KindRepairOpened:
		return append(out, labels.Prediction{
			Session: session, Record: record, Family: labels.FamilyRecovery, Value: labels.Opens,
		})
	case event.KindRepairDeepened, event.KindRepairRecurred:
		return append(out, labels.Prediction{
			Session: session, Record: record, Family: labels.FamilyRecovery, Value: labels.Deepens,
		})
	case event.KindObligationRepeated:
		return append(out, labels.Prediction{
			Session: session, Record: record, Family: labels.FamilyObligationPair, Value: labels.Repeat,
		})
	case event.KindObligationReleased:
		return append(out, labels.Prediction{
			Session: session, Record: record, Family: labels.FamilyObligationPair, Value: labels.Releases,
		})
	case event.KindRepairStatus:
		var pl struct {
			Status string `json:"pollution_status"`
		}
		if json.Unmarshal(e.Payload, &pl) != nil || pl.Status == "" {
			return out
		}
		return append(out, labels.Prediction{
			Session: session, Record: record, Family: labels.FamilyPollution, Value: pl.Status,
		})
	}
	return out
}

// budgetOf reports what the classifier under evaluation spends. A classifier
// with no marker table of its own — a semantic one — spends the baseline's,
// because the marker tier still runs underneath it.
func budgetOf(c classify.Classifier) classify.Budget {
	if h, ok := c.(classify.Heuristic); ok {
		return classify.BudgetOf(h)
	}
	return classify.CurrentBudget()
}
