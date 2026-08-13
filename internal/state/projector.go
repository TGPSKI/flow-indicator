package state

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
	"github.com/TGPSKI/flow-indicator/internal/observe"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// Pointer outcomes.
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
	OutcomeUnknown = "unknown"
)

// Projector folds records into events and state. It enforces the processing
// order: observe, classify, project state, measure. Callers push records in
// source order and write the returned events.
type Projector struct {
	cfg config.Config
	cls classify.Classifier
	// caps is what cls can establish. The projector reads it to refuse a
	// resolution the classifier had no way to reach, and stores it on every
	// snapshot so a reader can tell an unmeasurable fact from an unknown one.
	caps classify.Capabilities

	streamID  string
	seq       uint64
	epoch     uint64
	turnIndex int

	observer    *observe.Observer
	obligations *Obligations
	trends      *trendTracker

	repairs     []*Repair
	active      *Repair
	closing     []*Repair
	repairCount int

	baseline        []int
	pendingPointers []*pendingPointer
	window          []turnSample
	corrections     []int
	failures        []int
	interrupts      []int

	pointerSuccess  int
	pointerFailure  int
	pointerNoAnswer int

	lastAgentSeq  uint64
	lastAgentTime time.Time
	cycle         *responseCycle

	// cycleWrites is the set of paths written since the last operator turn, and
	// priorWrites is the same set for the cycle before it. Their intersection is
	// the agent re-editing what it just produced, immediately after the operator
	// spoke, which is the shape of a correction and needs no vocabulary at all.
	cycleWrites map[string]struct{}
	priorWrites map[string]struct{}
	// operatorSeq and operatorTime locate the turn the current cycle answers, so
	// an episode opened by a re-write is attributed to the turn that prompted it
	// rather than to the agent record that revealed it.
	operatorSeq  uint64
	operatorTime time.Time

	priorOperatorText []string
	obligationRepeats []int
	resetPending      bool

	regime   Regime
	snapshot metrics.Snapshot
}

// pendingPointer is a compact reference waiting for its outcome.
//
// A pointer's outcome opportunity is its own, and it is bounded. The turn that
// carried the reference is recorded so that the operator's first evaluation of
// the agent's response to it can be told apart from every later turn, and the
// normalized reference text is recorded so that a later correction naming the
// same target can still be linked to it.
type pendingPointer struct {
	seq          uint64
	turnIndex    int
	key          string
	pointerType  string
	pointerChars int
	turnChars    int
	remaining    int
	baseline     bool
	// evaluated reports that the reference has already had its own eligible
	// evaluation: an operator turn that followed an agent response to it.
	evaluated bool
}

// responseCycle is the whole agent and tool response to one correction: every
// record from the correction up to the record immediately before the next
// operator turn. Reading only the first assistant record would miss expansions
// announced three tool calls later.
type responseCycle struct {
	repair     *Repair
	firstAgent uint64
	records    int
	agentTurns int
	// claimed reports that some agent record in the cycle asserted the repair.
	// It is a claim, never verification.
	claimed bool
	// repaired is verified repair. It is set only from evidence that the target
	// the operator named was written; nothing the agent says about its own work
	// reaches it.
	repaired   *bool
	expansions int
	// writes counts the write actions the cycle carried. It separates the two
	// unknowns: a cycle with no writes observed had no evidence to read, and a
	// cycle with writes that missed the target was measured and came back
	// negative. Both render as an absent value and they are different facts.
	writes int
	// interrupts and failedActions are observed facts about the cycle, recorded
	// so they can be scored as recovery discriminators. No rule fires on them:
	// they are candidate evidence, and candidate evidence is measured before it
	// is trusted.
	interrupts    int
	failedActions int
}

// turnSample is one operator turn's contribution to the rolling window.
type turnSample struct {
	chars   int
	buckets metrics.Buckets
}

// New returns a Projector for one stream.
func New(cfg config.Config, cls classify.Classifier, streamID string) *Projector {
	return &Projector{
		cfg:         cfg,
		cls:         cls,
		caps:        cls.Capabilities(),
		streamID:    streamID,
		observer:    observe.New(cfg.Window.RollingTurns),
		obligations: NewObligations(),
		trends:      newTrendTracker(),
		regime:      RegimeFlow,
	}
}

// StreamID reports the stream the projector is folding.
func (p *Projector) StreamID() string { return p.streamID }

// Snapshot returns the most recent derived state.
func (p *Projector) Snapshot() metrics.Snapshot { return p.snapshot }

// Regime returns the current interaction regime.
func (p *Projector) Regime() Regime { return p.regime }

// Epoch returns the current epoch.
func (p *Projector) Epoch() uint64 { return p.epoch }

// Repairs returns every episode seen, in order.
func (p *Projector) Repairs() []*Repair { return p.repairs }

// Obligations returns the obligation surface.
func (p *Projector) Obligations() *Obligations { return p.obligations }

// Push folds one record and returns the events it produced, in pipeline order:
// observed, classified, state transitions, derived.
func (p *Projector) Push(ctx context.Context, rec stream.Record) []event.Event {
	p.seq++
	rec.Seq = p.seq
	if p.streamID == "" {
		p.streamID = rec.StreamID
	}
	rec.StreamID = p.streamID

	b := event.NewBuilder(p.streamID, rec.Seq, p.epoch, rec.Source)
	var out []event.Event

	obs := p.observer.Observe(rec)
	out = append(out, b.Emit(event.KindRecordObserved, event.ClassObserved, observedPayload{
		TurnID:       rec.TurnID,
		Speaker:      rec.Speaker,
		SpeakerClass: rec.SpeakerClass,
		Timestamp:    timestamp(rec.Timestamp),
		Observation:  obs,
		Snippet:      p.snippet(rec.Text),
	}))

	if stamped, ok := p.cls.(interface{ SetEpoch(uint64) }); ok {
		stamped.SetEpoch(p.epoch)
	}
	res, err := p.cls.Classify(ctx, p.input(rec))
	if err != nil {
		// A classifier failure is recorded and processing continues. Classifiers
		// may return a safe fallback alongside the error; OpenAI returns the
		// marker result when semantic inference could not be used. Keeping that
		// result preserves evidence the source and deterministic tier established.
		out = append(out, b.Emit(event.KindClassifierFailed, event.ClassClassified, failurePayload{
			Classifier: p.cls.Name(),
			Version:    p.cls.Version(),
			Error:      err.Error(),
		}))
	}
	out = append(out, p.classifiedEvents(b, rec, res)...)

	switch {
	case rec.IsOperatorTurn():
		// The cycle boundary is the operator turn. What the agent wrote before
		// this turn becomes the prior cycle, and the next writes start a fresh
		// set that will be compared against it.
		p.priorWrites, p.cycleWrites = p.cycleWrites, make(map[string]struct{})
		p.operatorSeq, p.operatorTime = rec.Seq, rec.Timestamp
		out = append(out, p.projectOperator(b, rec, obs, res)...)
	case rec.SpeakerClass == stream.SpeakerAgent:
		out = append(out, p.observeWrites(b, rec)...)
		p.projectAgent(rec, res)
		p.attribute(rec, 0)
	default:
		// An interruption is the operator exercising control over the agent
		// mid-turn. The harness wrote the marker, so the record carries no
		// operator characters and is not an operator turn; what it establishes
		// is that the operator stopped the work. That is control burden with no
		// inference attached, so it is counted alongside corrections rather
		// than being read for meaning it does not carry.
		if rec.Metadata[stream.MetaInputMode] == stream.InputInterrupt {
			p.interrupts = append(p.interrupts, p.turnIndex)
			if p.cycle != nil {
				p.cycle.interrupts++
			}
			out = append(out, b.Emit(event.KindOperatorInterrupt, event.ClassObserved, interruptPayload{
				TurnIndex: p.turnIndex,
				License:   licenseOperatorInterrupt,
			}))
		}
		if p.cycle != nil {
			p.cycle.records++
			p.cycle.failedActions += rec.FailedActions()
		}
		p.attribute(rec, 0)
	}
	return out
}

// Finish closes state that the end of the source leaves open.
//
// It is for a real source end: the file was read to its last byte. Everything
// still open becomes unknown, because the source stopped before the evidence
// arrived. Nothing here concludes that the interaction ended a particular way.
//
// An observer being stopped is not a source end. Use StopObservation for that.
func (p *Projector) Finish() []event.Event {
	b := event.NewBuilder(p.streamID, p.seq, p.epoch, stream.SourceRef{Adapter: "projector"})
	var out []event.Event

	out = append(out, p.settlePollution(b, false)...)

	for _, pp := range p.pendingPointers {
		p.pointerNoAnswer++
		if pp.baseline {
			p.addBaseline(pp.turnChars)
		}
		out = append(out, b.Emit(event.KindPointerResolved, event.ClassDerived, pointerOutcomePayload{
			PointerTurn: pp.seq,
			Type:        pp.pointerType,
			Outcome:     OutcomeUnknown,
			Evidence:    "the source ended before the outcome was known; the turn still joined the control baseline",
		}))
	}
	p.pendingPointers = nil

	if p.active != nil {
		p.active.close(RepairUnknown, p.active.LastActivity)
		out = append(out, b.Emit(event.KindRepairStatus, event.ClassDerived, repairStatusPayload{
			RepairID: p.active.ID, Status: RepairUnknown, Repair: p.active,
			Reason: "the source ended before the episode resolved",
		}))
		p.active = nil
		p.cycle = nil
	}
	return out
}

// StopObservation records that watching stopped. It draws no conclusion about
// the interaction.
//
// A watcher stop means observation stopped. It does not mean the interaction
// ended: the repair that was open is still open, the compact reference that
// had no outcome yet may get one, the obligations are whatever they were. The
// session may well be running in another terminal right now.
//
// Two events come out, because two different kinds of fact are involved. The
// observed one is that observation ended, why, and at which byte of which
// record. The derived one is the projector's inventory of state left open, so
// the next reader knows what was not settled rather than finding it settled
// wrongly. Reporting the inventory as observed would class projected state as
// something read out of the source.
func (p *Projector) StopObservation(offset int64, reason string) []event.Event {
	b := event.NewBuilder(p.streamID, p.seq, p.epoch, stream.SourceRef{Adapter: "projector"})
	inv := stateAtObservationStopPayload{
		Epoch:                p.epoch,
		PendingPointers:      len(p.pendingPointers),
		AwaitingDurability:   len(p.closing),
		UnresolvedCandidates: len(p.obligations.Unresolved()),
	}
	if p.active != nil {
		inv.OpenRepair = p.active.ID
	}
	return []event.Event{
		b.Emit(event.KindObservationStopped, event.ClassObserved, observationStoppedPayload{
			Reason:     reason,
			Offset:     offset,
			LastRecord: p.seq,
		}),
		b.Emit(event.KindStateAtObservationStop, event.ClassDerived, inv),
	}
}

// input builds the classifier's view of the current record.
func (p *Projector) input(rec stream.Record) classify.Input {
	in := classify.Input{Turn: rec, PriorOperatorText: p.priorOperatorText}
	for _, ob := range p.obligations.Unresolved() {
		in.UnresolvedObligations = append(in.UnresolvedObligations, classify.ObligationRef{
			ID: ob.ID, Key: ob.Key, RepeatKey: ob.RepeatKey, Kind: ob.Kind, Text: ob.SourceText,
		})
	}
	if p.active != nil {
		in.ActiveRepair = &classify.RepairRef{
			ID:          p.active.ID,
			Depth:       p.active.Depth,
			Status:      p.active.Status,
			TargetKey:   p.active.TargetKey,
			TargetPaths: p.active.TargetPaths,
			TriggerSeq:  p.active.TriggerAgentTurn,
		}
	}
	return in
}

// projectOperator applies one operator turn to state and measures it.
func (p *Projector) projectOperator(b *event.Builder, rec stream.Record, obs observe.Observation, res classify.Result) []event.Event {
	var out []event.Event
	p.turnIndex++

	// A reset clears rolling state, but the turn that carried the reset marker
	// is still measured against the epoch it closed. The clearing therefore
	// lands on the first turn of the new epoch.
	if p.resetPending {
		p.clearRolling()
	}

	// Settle what the previous turns left open, in a fixed order: pollution,
	// then pointer outcomes, then durability.
	out = append(out, p.settlePollution(b, res.Correction.IsCorrection)...)
	out = append(out, p.resolvePointers(b, res)...)
	out = append(out, p.settleDurability(b)...)

	// Resolutions are applied before the turn's own candidates, so a turn that
	// both withdraws a requirement and states it again ends with the
	// requirement standing. The two readings contradict each other, and keeping
	// the requirement is the one that does not drop something the operator
	// asked for on ambiguous evidence.
	out = append(out, p.resolveObligations(b, rec, res)...)

	introduced, repeated, revived := p.obligations.Apply(rec.Seq, p.epoch, p.turnIndex, res.Obligations)
	for _, ob := range introduced {
		out = append(out, b.Emit(event.KindObligationIntroduced, event.ClassDerived, obligationPayload{
			Obligation: ob, License: licenseObligationIntroduced,
		}))
	}
	for _, r := range revived {
		out = append(out, b.Emit(event.KindObligationRevived, event.ClassDerived, obligationRevivedPayload{
			Obligation: r.Obligation, From: r.From, License: licenseObligationRevived,
		}))
	}
	if len(repeated) > 0 {
		// One mark per turn. The rule that reads this asks whether a repeat
		// happened recently, not how many candidates the turn touched.
		p.obligationRepeats = append(p.obligationRepeats, p.turnIndex)
	}
	for _, ob := range repeated {
		out = append(out, b.Emit(event.KindObligationRepeated, event.ClassDerived, obligationRepeatPayload{
			Obligation:       ob,
			DuringCorrection: res.Correction.IsCorrection,
			License:          licenseObligationRepeated,
		}))
		// A repeat inside a correction turn is only evidence that THIS
		// obligation was not met when the correction is about this obligation.
		// An operator who corrects a file path while restating an unrelated
		// scope rule has said nothing about the scope rule; the supported
		// statement is the repeat, which the event above already carries.
		if res.Correction.IsCorrection && res.Correction.TargetKey == ob.Key {
			if v := p.obligations.Violate(ob.Key); v != nil {
				out = append(out, b.Emit(event.KindObligationViolated, event.ClassDerived, obligationPayload{
					Obligation: v, License: licenseObligationViolated,
				}))
			}
		}
	}

	out = append(out, p.projectRepair(b, rec, res)...)

	// Attribution runs after the transition, so the turn that closed the
	// episode is outside it: it spends no repair characters or records, and it
	// does not move the episode's last activity. The moment it closed is the
	// episode's own timestamp, written by close.
	p.attribute(rec, obs.Chars)

	// A pointer's outcome is not known yet, so its turn cannot enter the
	// control baseline until it resolves.
	if res.Pointer.IsPointer {
		p.pendingPointers = append(p.pendingPointers, &pendingPointer{
			seq:          rec.Seq,
			turnIndex:    p.turnIndex,
			key:          stream.Normalize(res.Pointer.Text),
			pointerType:  string(res.Pointer.Type),
			pointerChars: res.Pointer.Chars,
			turnChars:    obs.Chars,
			remaining:    p.cfg.Window.DereferenceOutcomeTurns,
			baseline:     p.baselineEligible(res),
		})
	}

	p.window = append(p.window, turnSample{chars: obs.Chars, buckets: bucketsOf(res)})
	if len(p.window) > p.cfg.Window.RollingTurns {
		p.window = p.window[len(p.window)-p.cfg.Window.RollingTurns:]
	}
	if res.Correction.IsCorrection {
		p.corrections = append(p.corrections, p.turnIndex)
	}
	p.priorOperatorText = append(p.priorOperatorText, rec.Text)
	if len(p.priorOperatorText) > p.cfg.Window.RollingTurns {
		p.priorOperatorText = p.priorOperatorText[len(p.priorOperatorText)-p.cfg.Window.RollingTurns:]
	}

	// A reset ends the epoch after this turn's evidence is recorded.
	if res.Reset {
		out = append(out, p.applyReset(b, rec)...)
	}

	snapshot := p.measure(rec, obs, len(introduced))

	// The current turn joins the baseline only after it has been measured
	// against the prior ones. A turn that enters its own denominator measures
	// nothing: its inflation is pulled toward 1 by its own size.
	if !res.Pointer.IsPointer && p.baselineEligible(res) {
		p.addBaseline(obs.Chars)
	}

	d := Decide(p.regimeInput(res.Reset, snapshot))
	if d.Regime != p.regime {
		out = append(out, b.Emit(event.KindRegimeChanged, event.ClassDerived, regimePayload{
			From: string(p.regime), To: string(d.Regime), Rule: d.Rule, License: d.License,
		}))
		p.regime = d.Regime
	}
	snapshot.Regime = string(d.Regime)
	snapshot.RegimeRule = d.Rule
	snapshot.RegimeLicense = d.License
	p.snapshot = snapshot
	out = append(out, b.Emit(event.KindMetricsComputed, event.ClassDerived, snapshot))

	// Trends are read off the snapshot that was just computed, after it is
	// recorded. They observe the metrics; nothing observes them back.
	for _, t := range p.trends.observe(snapshot, p.cfg) {
		out = append(out, b.Emit(event.KindTrendEmerged, event.ClassDerived, trendPayload{
			Metric:    t.Metric,
			From:      t.From.String(),
			To:        t.To.String(),
			Direction: trendDirection(t),
			Turns:     t.Turns,
			TurnIndex: p.turnIndex,
			License: fmt.Sprintf(
				"%s was confirmed at %s and held %s for %d consecutive operator turns, at or past the configured durability of %d",
				t.Metric, t.From, t.To, t.Turns, p.cfg.Window.TrendDurabilityTurns),
		}))
	}
	return out
}

// resolveObligations applies the resolutions a turn established, removing each
// resolved candidate from the inventory.
//
// Three guards stand between a classified resolution and the inventory, because
// this is the only path that drops something the operator asked for.
//
// The first is structural: this runs from the operator path alone. An agent
// turn never resolves an obligation, whatever it says about its own work. That
// is the same rule as ClaimedRepaired against TargetRepaired, and it is the
// reason the constraint cannot be got around by a classifier that reports
// satisfaction on an agent turn.
//
// The second is the capability check. A classifier that cannot establish a kind
// of resolution does not get to assert one; without this, extending the marker
// tier by accident would silently start emptying the inventory.
//
// The third is the premise. A resolution that states no evidence is refused
// outright rather than stored with an empty license, because the license is
// what a reader audits when a requirement stops being counted.
func (p *Projector) resolveObligations(b *event.Builder, rec stream.Record, res classify.Result) []event.Event {
	var out []event.Event
	for _, r := range res.Resolutions {
		if !p.canResolve(r.Kind) || strings.TrimSpace(r.Evidence) == "" {
			continue
		}
		ob := p.obligations.Resolve(rec.Seq, r.Key, r.Kind)
		if ob == nil {
			continue
		}
		out = append(out, b.Emit(resolutionEventKind(r.Kind), event.ClassDerived, obligationResolvedPayload{
			Obligation: ob,
			Kind:       r.Kind,
			Provenance: res.Provenance,
			License:    r.Evidence,
		}))
	}
	return out
}

// canResolve reports whether the configured classifier can establish this kind
// of resolution. An unrecognized kind establishes nothing.
func (p *Projector) canResolve(kind string) bool {
	switch kind {
	case classify.ResolutionReleased:
		return p.caps.Can(classify.CapObligationRelease)
	case classify.ResolutionSuperseded:
		return p.caps.Can(classify.CapObligationSupersession)
	case classify.ResolutionSatisfied:
		return p.caps.Can(classify.CapObligationSatisfaction)
	default:
		return false
	}
}

// resolutionEventKind maps a resolution to the transition it records.
func resolutionEventKind(kind string) string {
	switch kind {
	case classify.ResolutionReleased:
		return event.KindObligationReleased
	case classify.ResolutionSuperseded:
		return event.KindObligationSuperseded
	default:
		return event.KindObligationSatisfied
	}
}

// trendDirection names which way the crossing went, in cost terms.
func trendDirection(t trend) string {
	if t.Degrading() {
		return "degrading"
	}
	return "improving"
}

// attribute folds one record into the open episode: its characters, its place
// in the record count, and the time it arrived.
//
// One boundary decides all three. A record inside the episode is a record the
// episode's duration covers, so the accounting and the timestamp cannot
// disagree about which records belong to it.
func (p *Projector) attribute(rec stream.Record, chars int) {
	if p.active == nil {
		return
	}
	p.active.Chars += chars
	p.active.Records++
	p.active.touch(rec.Timestamp)
}

// observeWrites folds an agent record's write set into the current cycle and
// opens a repair episode when the agent re-edits what the previous cycle wrote.
//
// The rule, whole:
//
//	A repair opens when the response cycle following an operator turn writes to
//	a path the immediately preceding cycle also wrote.
//
// The agent re-editing what it just produced, immediately after the operator
// spoke, is the shape of a correction. It has no free parameter: the lookback is
// one cycle, and the comparison is set membership. It reads no words, so it
// finds the corrections that carry no correction vocabulary — which in the seed
// corpus was all of them in the session the operator named RESCUE.
//
// It fires once per cycle. The first overlapping write establishes the episode;
// the rest of the cycle's writes are that episode's work, not further evidence.
func (p *Projector) observeWrites(b *event.Builder, rec stream.Record) []event.Event {
	writes := rec.WriteTargets()
	if len(writes) == 0 {
		return nil
	}
	var overlap []string
	for _, w := range writes {
		if _, ok := p.priorWrites[w]; ok {
			overlap = append(overlap, w)
		}
		p.cycleWrites[w] = struct{}{}
	}
	if p.cycle != nil {
		p.cycle.writes += len(writes)
		p.cycle.failedActions += rec.FailedActions()
	}
	// An episode already open, or an operator turn that never happened, leaves
	// nothing for this rule to establish.
	if len(overlap) == 0 || p.active != nil || p.operatorSeq == 0 {
		return nil
	}
	// A repeat within the same cycle is not a second correction. Clearing the
	// prior set spends the evidence once.
	p.priorWrites = nil

	res := classify.Result{Correction: classify.Correction{
		IsCorrection: true,
		Structural:   true,
		TargetType:   string(classify.PointerPath),
		TargetKey:    overlap[0],
		TargetPaths:  overlap,
	}}
	return append(
		[]event.Event{b.Emit(event.KindCorrectionCandidate, event.ClassClassified, correctionPayload{
			Provenance: classify.Provenance{
				Classifier: "structural",
				Version:    classify.HeuristicVersion,
				SourceTurn: p.operatorSeq,
			},
			Correction: res.Correction,
			TriggerSeq: p.lastAgentSeq,
		})},
		p.openStructuralRepair(b, rec, res, overlap)...,
	)
}

// openStructuralRepair opens an episode attributed to the operator turn the
// cycle answers, not to the agent record that revealed it. The operator's turn
// is what began the recovery; the write is only when it became visible.
func (p *Projector) openStructuralRepair(b *event.Builder, rec stream.Record, res classify.Result, overlap []string) []event.Event {
	p.repairCount++
	start := p.operatorTime
	if start.IsZero() {
		start = rec.Timestamp
	}
	p.active = newRepair(p.repairCount, p.epoch, p.lastAgentSeq, p.operatorSeq,
		res.Correction.TargetType, res.Correction.TargetKey, 0, start)
	p.active.TargetPaths = overlap
	p.active.Structural = true
	p.active.activeIndex = p.turnIndex
	p.repairs = append(p.repairs, p.active)
	p.openCycle(p.active)
	p.corrections = append(p.corrections, p.turnIndex)
	return []event.Event{b.Emit(event.KindRepairOpened, event.ClassDerived, repairPayload{
		Repair: p.active, License: licenseStructural,
	})}
}

// projectAgent folds one agent turn into the open correction response cycle.
// Every record of the cycle contributes, not only the first.
func (p *Projector) projectAgent(rec stream.Record, res classify.Result) {
	p.lastAgentSeq = rec.Seq
	p.lastAgentTime = rec.Timestamp
	if p.cycle == nil {
		return
	}
	p.cycle.records++
	p.cycle.agentTurns++
	if p.cycle.firstAgent == 0 {
		p.cycle.firstAgent = rec.Seq
	}
	p.cycle.expansions += res.Repair.Expansions()
	if res.Repair.ClaimedRepaired {
		p.cycle.claimed = true
	}
	if res.Repair.TargetRepaired != nil {
		p.cycle.repaired = res.Repair.TargetRepaired
	}
}

// projectRepair opens, deepens or provisionally closes an episode.
func (p *Projector) projectRepair(b *event.Builder, rec stream.Record, res classify.Result) []event.Event {
	var out []event.Event

	if res.Correction.IsCorrection {
		// An episode being open while a correction arrives is coexistence in
		// time. Deepening that episode claims the correction is about the same
		// thing, which needs its own evidence: two established target keys that
		// are equal. Without that the episode is not deepened, and the new
		// correction gets its own identity.
		if p.active != nil {
			if targetEquivalent(p.active.TargetKey, res.Correction.TargetKey) {
				// A deeper correction may name paths the first did not. The
				// episode's target is everything the operator has named about
				// it, so a later write to any of them is still repair.
				p.active.TargetPaths = mergePaths(p.active.TargetPaths, res.Correction.TargetPaths)
				p.active.Depth++
				p.active.activeIndex = p.turnIndex
				p.active.reopen()
				p.openCycle(p.active)
				out = append(out, b.Emit(event.KindRepairDeepened, event.ClassDerived, repairPayload{
					Repair: p.active, License: licenseDeepened,
				}))
				return out
			}
			out = append(out, p.retireActive(b, rec)...)
		}
		// A correction whose target matches an episode awaiting durability is
		// the same episode coming back. It reopens at greater depth under its
		// own identity: two episode records would say the operator corrected
		// two different things.
		if r := p.reopenClosing(res.Correction.TargetKey); r != nil {
			r.TargetPaths = mergePaths(r.TargetPaths, res.Correction.TargetPaths)
			r.Recurred = true
			r.reopen()
			r.Depth++
			r.activeIndex = p.turnIndex
			p.active = r
			p.openCycle(r)
			out = append(out, b.Emit(event.KindRepairRecurred, event.ClassDerived, repairPayload{
				Repair: r, License: licenseRecurred,
			}))
			return out
		}
		p.repairCount++
		start := p.lastAgentTime
		if start.IsZero() {
			start = rec.Timestamp
		}
		p.active = newRepair(p.repairCount, p.epoch, p.lastAgentSeq, rec.Seq,
			res.Correction.TargetType, res.Correction.TargetKey, res.Pointer.Chars, start)
		p.active.TargetPaths = res.Correction.TargetPaths
		p.active.activeIndex = p.turnIndex
		p.repairs = append(p.repairs, p.active)
		p.openCycle(p.active)
		out = append(out, b.Emit(event.KindRepairOpened, event.ClassDerived, repairPayload{
			Repair: p.active, License: licenseOpened,
		}))
		return out
	}

	if p.active == nil || p.cycleUnanswered() {
		// Nothing to close, or the agent has not answered yet. An episode does
		// not close merely because an assistant spoke.
		return out
	}
	// Acceptance or forward work closes the episode. Permission to continue
	// does not: "continue" grants the agent leave to keep going and says
	// nothing about whether the correction was met.
	//
	// An episode also stops being the thing in play when the operator is no
	// longer visibly on it. This turn carries no correction — a correction was
	// handled above — so if it also restates nothing and spends nothing on
	// recovery, the operator has moved to other work. Membership decides
	// attribution: what follows belongs to whatever comes next, not to an
	// episode nobody is working on. Leaving it open charged 61cc320d's closing
	// stretch of new task assignment to a repair opened by a mis-click, and
	// read THRASH over the session the operator rated best.
	//
	// This is a bound on membership, not a finding: the close is provisional,
	// and durability still has to be earned in the recurrence window.
	//
	// Membership asks one question: is there established evidence that the
	// operator is still controlling or reconstructing the episode in play?
	// Continuation and a stop marker both answer yes — "continue" is permission
	// to keep going on this, and "stop" is halting this. So does control text.
	// An operator who follows a correction with "only touch internal/worker" or
	// "do not add another test" is constraining the repair, not starting
	// something else, and closing the episode there would end measurement while
	// the operator is visibly still on it.
	onEpisode := res.Continuation || res.Stop ||
		res.CharsIn(classify.BucketControl) > 0 ||
		res.CharsIn(classify.BucketRestate) > 0 ||
		res.CharsIn(classify.BucketRecovery) > 0
	if !res.Acceptance && res.CharsIn(classify.BucketForward) == 0 && onEpisode {
		// No closing evidence. Accrual is bounded mechanically so that an
		// episode nobody closed cannot grow recovery characters without limit
		// and latch the regime. The bound is a stop to measurement, not a
		// finding: elapsed turns establish only that the episode was still
		// unresolved when the window ran out.
		if p.turnIndex-p.active.activeIndex >= p.cfg.Window.CorrectionDurabilityTurns {
			p.active.close(RepairUnknown, rec.Timestamp)
			out = append(out, b.Emit(event.KindRepairStatus, event.ClassDerived, repairStatusPayload{
				RepairID: p.active.ID, Status: RepairUnknown, Repair: p.active,
				Reason: "no closing evidence inside the durability window; the outcome was never established",
			}))
			p.active = nil
			p.cycle = nil
		}
		return out
	}
	p.active.close(RepairProvisional, rec.Timestamp)
	p.active.closeIndex = p.turnIndex
	out = append(out, b.Emit(event.KindRepairProvisionalClose, event.ClassDerived, repairPayload{
		Repair: p.active, License: licenseProvisional,
	}))
	p.closing = append(p.closing, p.active)
	p.active = nil
	return out
}

// targetEquivalent reports that two corrections are established as being about
// the same thing. Both keys must be present: an unknown target is not equal to
// anything, another unknown target included.
func targetEquivalent(a, b string) bool { return a != "" && b != "" && a == b }

// mergePaths unions two target lists, keeping first-seen order.
func mergePaths(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, p := range list {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

// retireActive ends the open episode when a correction arrives that cannot be
// established as belonging to it.
//
// The episode stops accruing here rather than absorbing recovery characters
// spent on a different semantic target. Its outcome is unknown because none was
// ever established, not because anything concluded it failed.
func (p *Projector) retireActive(b *event.Builder, rec stream.Record) []event.Event {
	r := p.active
	r.close(RepairUnknown, rec.Timestamp)
	p.active = nil
	p.cycle = nil
	return []event.Event{b.Emit(event.KindRepairStatus, event.ClassDerived, repairStatusPayload{
		RepairID: r.ID, Status: RepairUnknown, Repair: r,
		Reason: "a later correction could not be established as belonging to this episode; its outcome was never established",
	})}
}

// reopenClosing removes and returns the episode awaiting durability whose
// target matches key, if any. An empty key matches nothing: without an
// established target, recurrence cannot be claimed.
func (p *Projector) reopenClosing(key string) *Repair {
	if key == "" {
		return nil
	}
	for i, r := range p.closing {
		if r.TargetKey == key {
			p.closing = append(p.closing[:i:i], p.closing[i+1:]...)
			return r
		}
	}
	return nil
}

// settleDurability promotes provisionally closed episodes once the recurrence
// window has passed without the same target coming back.
func (p *Projector) settleDurability(b *event.Builder) []event.Event {
	var out []event.Event
	var keep []*Repair
	for _, r := range p.closing {
		switch {
		case p.turnIndex-r.closeIndex >= p.cfg.Window.CorrectionDurabilityTurns:
			if r.TargetKey == "" {
				r.Status = RepairUnknown
				out = append(out, b.Emit(event.KindRepairStatus, event.ClassDerived, repairStatusPayload{
					RepairID: r.ID, Status: RepairUnknown, Repair: r,
					Reason: "target equivalence could not be established",
				}))
				continue
			}
			r.Status = RepairDurable
			out = append(out, b.Emit(event.KindRepairDurableClose, event.ClassDerived, repairPayload{
				Repair: r, License: licenseDurable,
			}))
		default:
			keep = append(keep, r)
		}
	}
	p.closing = keep
	return out
}

// openCycle starts the correction response cycle for an episode.
func (p *Projector) openCycle(r *Repair) {
	p.cycle = &responseCycle{repair: r}
}

// cycleUnanswered reports that the open cycle has seen no agent turn yet.
func (p *Projector) cycleUnanswered() bool {
	return p.cycle != nil && p.cycle.agentTurns == 0
}

// settlePollution finalizes the repair-pollution status of the whole response
// cycle once the next operator turn arrives.
func (p *Projector) settlePollution(b *event.Builder, immediateCorrection bool) []event.Event {
	if p.cycle == nil || p.cycle.records == 0 {
		return nil
	}
	c := p.cycle
	p.cycle = nil
	status, premise := metrics.PollutionAssessment(metrics.Cycle{
		TargetRepaired:      c.repaired,
		Expansions:          c.expansions,
		ImmediateCorrection: immediateCorrection,
		Writes:              c.writes,
		Named:               len(c.repair.TargetPaths) > 0,
	})
	c.repair.Expansions += c.expansions
	c.repair.Pollution = status
	c.repair.RepairClaimed = c.claimed
	return []event.Event{b.Emit(event.KindRepairStatus, event.ClassDerived, pollutionPayload{
		RepairID:            c.repair.ID,
		AgentTurn:           c.firstAgent,
		CycleRecords:        c.records,
		ClaimedRepaired:     c.claimed,
		TargetRepaired:      c.repaired,
		Expansions:          c.expansions,
		ImmediateCorrection: immediateCorrection,
		CycleWrites:         c.writes,
		CycleInterrupts:     c.interrupts,
		CycleFailedActions:  c.failedActions,
		Status:              status,
		Evidence:            premise,
	})}
}

// resolvePointers settles outstanding compact references against this turn's
// evidence. Absence of a correction is not success: positive evidence is
// required, and a turn that provides neither counts down to unknown.
//
// Every reference has its own outcome opportunity, and this turn is not it for
// all of them at once. A turn only evaluates a reference the agent has already
// had a chance to act on, so a reference the agent has not answered yet is
// passed over rather than settled or counted down. Of the turns that do
// evaluate it, the first is the operator's own response to what the agent did
// with that reference; later turns are responses to later work, and may settle
// it only when they name the same target. A correction that satisfies neither
// test says nothing about the reference and leaves it pending, because the
// alternative is one correction reported as the failure of every reference that
// happened to be outstanding.
func (p *Projector) resolvePointers(b *event.Builder, res classify.Result) []event.Event {
	if len(p.pendingPointers) == 0 {
		return nil
	}
	positive := res.Acceptance || res.CharsIn(classify.BucketForward) > 0
	var out []event.Event
	var keep []*pendingPointer
	for _, pp := range p.pendingPointers {
		if p.lastAgentSeq <= pp.seq {
			// The agent has not acted since the reference was sent, so this turn
			// evaluates nothing about it.
			keep = append(keep, pp)
			continue
		}
		own := !pp.evaluated
		named := res.Correction.IsCorrection && targetEquivalent(pp.key, res.Correction.TargetKey)
		switch {
		case own && res.Correction.IsCorrection:
			p.pointerFailure++
			p.failures = append(p.failures, p.turnIndex)
			out = append(out, b.Emit(event.KindPointerResolved, event.ClassDerived, pointerOutcomePayload{
				PointerTurn: pp.seq, Type: pp.pointerType, Outcome: OutcomeFailure,
				Evidence: "the operator's first response to the agent's work on this reference corrected the agent",
			}))
		case own && positive:
			p.pointerSuccess++
			if pp.baseline {
				p.addBaseline(pp.turnChars)
			}
			out = append(out, b.Emit(event.KindPointerResolved, event.ClassDerived, pointerOutcomePayload{
				PointerTurn: pp.seq, Type: pp.pointerType, Outcome: OutcomeSuccess,
				Evidence: "the operator's first response to the agent's work on this reference accepted it or moved the work forward",
			}))
		case named:
			p.pointerFailure++
			p.failures = append(p.failures, p.turnIndex)
			out = append(out, b.Emit(event.KindPointerResolved, event.ClassDerived, pointerOutcomePayload{
				PointerTurn: pp.seq, Type: pp.pointerType, Outcome: OutcomeFailure,
				Evidence: "a later correction named the same target as this reference",
			}))
		default:
			pp.evaluated = true
			pp.remaining--
			if pp.remaining > 0 {
				keep = append(keep, pp)
				continue
			}
			p.pointerNoAnswer++
			// The window closed without evidence either way. The reference is
			// unknown, but the turn that carried it is just an ordinary
			// steering turn, so it joins the control baseline.
			//
			// Discarding it instead would let absence of evidence act as
			// evidence against the turn. Compact references are ordinary
			// English and most turns carry one, so that rule starves the
			// baseline: a real session reaches the minimum for a baseline
			// almost never, and transmission load then reports unknown for the
			// whole session.
			if pp.baseline {
				p.addBaseline(pp.turnChars)
			}
			out = append(out, b.Emit(event.KindPointerResolved, event.ClassDerived, pointerOutcomePayload{
				PointerTurn: pp.seq, Type: pp.pointerType, Outcome: OutcomeUnknown,
				Evidence: "no evidence about this reference inside its outcome window; the turn still joined the control baseline",
			}))
		}
	}
	p.pendingPointers = keep
	return out
}

// applyReset closes the epoch. Rolling baselines start fresh; session-global
// counters are kept.
func (p *Projector) applyReset(b *event.Builder, rec stream.Record) []event.Event {
	var out []event.Event
	if p.active != nil {
		p.active.close(RepairReset, rec.Timestamp)
		out = append(out, b.Emit(event.KindRepairReset, event.ClassDerived, repairPayload{
			Repair: p.active, License: licenseReset,
		}))
		p.active = nil
		p.cycle = nil
	}
	for _, r := range p.closing {
		r.Status = RepairUnknown
		out = append(out, b.Emit(event.KindRepairStatus, event.ClassDerived, repairStatusPayload{
			RepairID: r.ID, Status: RepairUnknown, Repair: r,
			Reason: "the epoch ended before the durability window closed",
		}))
	}
	p.closing = nil

	for _, pp := range p.pendingPointers {
		p.pointerNoAnswer++
		out = append(out, b.Emit(event.KindPointerResolved, event.ClassDerived, pointerOutcomePayload{
			PointerTurn: pp.seq, Type: pp.pointerType, Outcome: OutcomeUnknown,
			Evidence: "the epoch ended before the outcome was known",
		}))
	}
	p.pendingPointers = nil

	ids := p.obligations.MarkUnknown()
	prior := p.epoch
	p.epoch++
	out = append(out, b.Emit(event.KindEpochAdvanced, event.ClassDerived, epochPayload{
		From:                     prior,
		To:                       p.epoch,
		Trigger:                  rec.Seq,
		ObligationsMarkedUnknown: ids,
		License:                  licenseEpochAdvanced,
	}))
	b.SetEpoch(p.epoch)
	p.resetPending = true
	return out
}

// clearRolling starts fresh rolling baselines for a new epoch. Session-global
// counters, including pointer outcomes and the repair list, are kept.
func (p *Projector) clearRolling() {
	p.resetPending = false
	p.observer.Reset()
	p.baseline = nil
	p.window = nil
	p.corrections = nil
	p.failures = nil
	p.interrupts = nil
	p.priorOperatorText = nil
	p.obligationRepeats = nil
}

// baselineEligible reports whether a turn may join the control baseline: a
// non-correction steering turn outside an active repair episode.
//
// That is the whole of the eligibility rule, and it is not a success test.
// These turns are not known to have worked; they are the turns not visibly
// spent on repair.
func (p *Projector) baselineEligible(res classify.Result) bool {
	return !res.Correction.IsCorrection && p.active == nil
}

// addBaseline appends one eligible prior turn, keeping the baseline bounded by
// window.rolling_turns so it measures recent practice rather than the session.
func (p *Projector) addBaseline(chars int) {
	p.baseline = append(p.baseline, chars)
	if len(p.baseline) > p.cfg.Window.RollingTurns {
		p.baseline = p.baseline[len(p.baseline)-p.cfg.Window.RollingTurns:]
	}
}

// measure computes the derived snapshot for one operator turn. The baseline it
// reads holds prior eligible turns only.
func (p *Projector) measure(rec stream.Record, obs observe.Observation, newObligations int) metrics.Snapshot {
	baseline := metrics.BaselineChars(p.baseline, p.cfg.Thresholds.MinimumControlBaseline)
	buckets := p.windowBuckets()
	unresolved := p.obligations.Unresolved()

	repeats := make([]int, 0, len(unresolved))
	ages := make([]int, 0, len(unresolved))
	for _, ob := range unresolved {
		repeats = append(repeats, ob.RepeatCount)
		ages = append(ages, p.turnIndex-ob.introducedIndex)
	}

	s := metrics.Snapshot{
		Seq:       rec.Seq,
		TurnIndex: p.turnIndex,
		Epoch:     p.epoch,

		UserChars:         obs.Chars,
		BaselineChars:     baseline,
		BaselineTurns:     len(p.baseline),
		SerializationInfl: metrics.SerializationInflation(obs.Chars, baseline),
		QuotedFraction:    metrics.KnownValue(obs.QuotedFraction),
		RepeatFraction:    metrics.KnownValue(obs.RepeatFraction),

		ForwardChars:  buckets.Forward,
		ControlChars:  buckets.Control,
		RecoveryChars: buckets.Recovery,
		RestateChars:  buckets.Restate,
		OtherChars:    buckets.Other,
		ForwardShare:  buckets.ForwardWorkShare(),
		ControlBurden: buckets.ControlPlaneBurden(),
		RestateShare:  buckets.RestateBurden(),

		UnresolvedObligations: len(unresolved),
		NewObligations:        newObligations,
		RepeatedObligations:   p.obligations.RepeatedUnresolved(),
		ViolatedObligations:   p.obligations.CountStatus(ObligationViolated),
		SatisfiedObligation:   p.obligations.CountStatus(ObligationSatisfied),
		ReleasedObligations:   p.obligations.CountStatus(ObligationReleased),
		SupersededObligations: p.obligations.CountStatus(ObligationSuperseded),
		MeanRepeatCount:       metrics.MeanRepeatCount(repeats),
		MedianCandidateAge:    metrics.MedianCandidateAge(ages),
		RepeatRatio:           metrics.ObligationRepeatRatio(p.obligations.RepeatedUnresolved(), len(unresolved)),

		PointerSuccess: p.pointerSuccess,
		PointerFailure: p.pointerFailure,
		PointerUnknown: p.pointerNoAnswer,
		Dereference:    metrics.DereferenceReliability(p.pointerSuccess, p.pointerFailure),

		RecentCorrections:       countWithin(p.corrections, p.turnIndex, driftWindowTurns),
		RecentInterrupts:        countWithin(p.interrupts, p.turnIndex, driftWindowTurns),
		RecentDereferenceMisses: countWithin(p.failures, p.turnIndex, driftWindowTurns),
		DriftWindowTurns:        driftWindowTurns,

		Capabilities: p.caps.Strings(),
	}

	if r := p.currentRepair(); r != nil {
		s.RepairID = r.ID
		s.RepairDepth = r.Depth
		s.RepairChars = r.Chars
		s.RepairRecords = r.Records
		s.RepairSeconds = metrics.RecoveryDuration(r.Start, r.end())
		s.RepairMagnify = metrics.RepairMagnification(r.Chars, r.PointerChars)
		s.RepairStatus = r.Status
		s.Expansions = r.Expansions
		s.PollutionStatus = r.Pollution
	}
	return s
}

// currentRepair is the open episode, or the most recent one when none is open.
func (p *Projector) currentRepair() *Repair {
	if p.active != nil {
		return p.active
	}
	if len(p.repairs) == 0 {
		return nil
	}
	return p.repairs[len(p.repairs)-1]
}

func (p *Projector) regimeInput(reset bool, s metrics.Snapshot) RegimeInput {
	in := RegimeInput{
		Reset:                         reset,
		RepairOpen:                    p.active != nil,
		Baseline:                      s.BaselineChars,
		SerializationInflation:        s.SerializationInfl,
		ForwardShare:                  s.ForwardShare,
		RepeatedUnresolvedObligations: p.obligations.RepeatedUnresolved(),
		RecentObligationRepeats:       countWithin(p.obligationRepeats, p.turnIndex, driftWindowTurns),
		RecentCorrections:             countWithin(p.corrections, p.turnIndex, driftWindowTurns),
		RecentInterrupts:              countWithin(p.interrupts, p.turnIndex, driftWindowTurns),
		RecentDereferenceFailure:      countWithin(p.failures, p.turnIndex, driftWindowTurns),
		Thresholds:                    p.cfg.Thresholds,
	}
	if p.active != nil {
		in.RepairDepth = p.active.Depth
		in.RepairChars = p.active.Chars
	}
	return in
}

// countWithin counts marks inside the last width operator turns.
func countWithin(marks []int, now, width int) int {
	var n int
	for _, m := range marks {
		if now-m < width {
			n++
		}
	}
	return n
}

// windowBuckets sums the rolling window of operator turns.
func (p *Projector) windowBuckets() metrics.Buckets {
	var b metrics.Buckets
	for _, t := range p.window {
		b.Forward += t.buckets.Forward
		b.Control += t.buckets.Control
		b.Recovery += t.buckets.Recovery
		b.Restate += t.buckets.Restate
		b.Other += t.buckets.Other
	}
	return b
}

func bucketsOf(res classify.Result) metrics.Buckets {
	return metrics.Buckets{
		Forward:  res.CharsIn(classify.BucketForward),
		Control:  res.CharsIn(classify.BucketControl),
		Recovery: res.CharsIn(classify.BucketRecovery),
		Restate:  res.CharsIn(classify.BucketRestate),
		Other:    res.CharsIn(classify.BucketOther),
	}
}

// snippet returns the stored excerpt of a record's text under the privacy
// settings. Default settings store a bounded snippet, never the full text.
func (p *Projector) snippet(text string) string {
	if p.cfg.Privacy.StoreText {
		return text
	}
	if !p.cfg.Privacy.StoreSnippets || p.cfg.Privacy.SnippetChars == 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= p.cfg.Privacy.SnippetChars {
		return text
	}
	return string([]rune(text)[:p.cfg.Privacy.SnippetChars])
}

func timestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
