// Package render turns stored events into operator-facing output: a markdown
// report, a timeline, a turn drilldown, and the live one-screen view.
//
// Rendering never classifies and never changes state. Every number it prints
// came from an event file.
package render

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
	"github.com/TGPSKI/flow-indicator/internal/state"
	"github.com/TGPSKI/flow-indicator/internal/store"
)

// Session is the projection of one stream's event files.
type Session struct {
	Dir      string
	StreamID string
	Source   store.Source

	Snapshots   []metrics.Snapshot
	RegimeOrder []string
	Epochs      uint64

	Repairs     map[string]*state.Repair
	RepairOrder []string
	Pollution   map[string]pollutionRecord

	Obligations     map[string]*state.Obligation
	ObligationOrder []string

	Corrections       int
	ObligationRepeats int
	// ObligationReleases and ObligationRevivals count the inventory going down
	// and coming back. They are counted apart from repeats because a
	// re-assertion after a withdrawal is a different event from a restatement
	// of something never withdrawn.
	ObligationReleases int
	ObligationRevivals int
	OperatorTurns      int
	Records            int
	PointerOutcome     map[string]int
	PollutionByRepair  map[string][]string
	// Semantic records the deferred local-model evidence retained in the event
	// log. It is operational evidence about this instrument, never an input to
	// the metrics or regime projection.
	Semantic SemanticStats

	events []event.Event
}

// pollutionRecord is one cycle's pollution assessment. It reads
// `pollution_status`, not `status`: an episode's own status shares this event
// kind, and reading the shorter name would count an episode whose outcome was
// never established as a cycle assessed as unknown.
type pollutionRecord struct {
	Status     string `json:"pollution_status"`
	Expansions int    `json:"repair_expansion_count"`
	AgentTurn  uint64 `json:"agent_turn"`
}

// SemanticStats summarizes persisted worker completions. Its counters are
// deliberately separate from interaction measurements: a model timeout says
// nothing about the measured conversation.
type SemanticStats struct {
	Eligible    int      `json:"eligible"`
	Requested   int      `json:"requested"`
	Validated   int      `json:"validated"`
	Dropped     int      `json:"dropped"`
	Pending     int      `json:"pending"`
	Applied     int      `json:"applied"`
	Changed     int      `json:"changed"`
	Recorded    int      `json:"recorded"`
	Completed   int      `json:"completed"`
	Failed      int      `json:"failed"`
	TimedOut    int      `json:"timed_out"`
	Canceled    int      `json:"canceled"`
	P50MS       *int64   `json:"p50_latency_ms,omitempty"`
	P95MS       *int64   `json:"p95_latency_ms,omitempty"`
	Classifiers []string `json:"classifiers,omitempty"`
	LastStatus  string   `json:"last_status,omitempty"`
	LastError   string   `json:"last_error,omitempty"`
	LastSeq     uint64   `json:"last_source_seq,omitempty"`

	latencies    []int64
	seen         map[string]struct{}
	dispositions map[string]classify.Completion
}

func (s *SemanticStats) add(c classify.Completion) {
	if s.dispositions == nil {
		s.dispositions = make(map[string]classify.Completion)
	}
	prior, exists := s.dispositions[c.JobID]
	if !exists || prior.Status == classify.CompletionPending {
		s.dispositions[c.JobID] = c
	}
	s.LastStatus, s.LastError, s.LastSeq = c.Status, c.Error, c.Seq
	if c.Classifier != "" {
		identity := c.Classifier + "/" + c.ClassifierVersion + "/" + c.ClassifierHash
		if s.seen == nil {
			s.seen = make(map[string]struct{})
		}
		if _, ok := s.seen[identity]; !ok {
			s.seen[identity] = struct{}{}
			s.Classifiers = append(s.Classifiers, identity)
		}
	}
	if c.Status == classify.CompletionPending || c.Status == classify.CompletionDropped {
		return
	}
	s.Recorded++
	if c.LatencyMS >= 0 && (c.Requested || c.Status != classify.CompletionCanceled) {
		s.latencies = append(s.latencies, c.LatencyMS)
	}
	switch c.Status {
	case classify.CompletionCompleted:
		s.Completed++
	case classify.CompletionTimedOut:
		s.TimedOut++
	case classify.CompletionCanceled:
		s.Canceled++
	default:
		s.Failed++
	}
}

func (s *SemanticStats) finish() {
	for _, c := range s.dispositions {
		s.Eligible++
		if c.Requested {
			s.Requested++
		}
		switch c.Status {
		case classify.CompletionCompleted:
			s.Validated++
		case classify.CompletionDropped:
			s.Dropped++
		case classify.CompletionPending:
			s.Pending++
		}
	}
	sort.Strings(s.Classifiers)
	if len(s.latencies) == 0 {
		return
	}
	sort.Slice(s.latencies, func(i, j int) bool { return s.latencies[i] < s.latencies[j] })
	p50, p95 := semanticPercentile(s.latencies, 50), semanticPercentile(s.latencies, 95)
	s.P50MS, s.P95MS = &p50, &p95
}

func semanticPercentile(sorted []int64, p int) int64 {
	i := (len(sorted)*p + 99) / 100
	if i < 1 {
		i = 1
	}
	return sorted[i-1]
}

// Load reads a session directory into a projection.
func Load(dir string) (*Session, error) {
	events, err := store.ReadAll(dir)
	if err != nil {
		return nil, err
	}
	s, err := LoadEvents(events)
	if err != nil {
		return nil, err
	}
	s.Dir = dir
	if raw, err := readSourceFile(dir); err == nil {
		s.Source = raw
		s.StreamID = raw.StreamID
	}
	return s, nil
}

// LoadEvents projects an event sequence without reading a session directory.
// A semantic projection update replaces the prior derived view while its
// completion records remain in the operational semantic statistics.
func LoadEvents(events []event.Event) (*Session, error) {
	base, err := loadEvents(events)
	if err != nil {
		return nil, err
	}
	var latest state.SemanticProjectionUpdate
	view := make(map[uint64][]event.Event)
	for _, e := range events {
		if e.Kind != event.KindSemanticCompleted && e.Kind != event.KindSemanticDisposition && e.Kind != event.KindSemanticProjectionDelta && e.Kind != event.KindSemanticProjectionUpdated {
			view[e.Seq] = append(view[e.Seq], e)
		}
	}
	var parts []state.SemanticProjectionDelta
	deltaApplied := false
	for _, e := range events {
		if e.Kind == event.KindSemanticProjectionDelta {
			var part state.SemanticProjectionDelta
			if err := json.Unmarshal(e.Payload, &part); err != nil {
				return nil, fmt.Errorf("render: decode projection delta %s: %w", e.ID, err)
			}
			if part.Part == 0 {
				parts = nil
			}
			parts = append(parts, part)
			continue
		}
		if e.Kind != event.KindSemanticProjectionUpdated {
			continue
		}
		latest = state.SemanticProjectionUpdate{}
		if err := json.Unmarshal(e.Payload, &latest); err != nil {
			return nil, fmt.Errorf("render: decode semantic projection event %s: %w", e.ID, err)
		}
		if latest.Revision != "" {
			if len(parts) != latest.Parts {
				return nil, fmt.Errorf("render: projection %s has %d parts, expected %d", latest.Revision, len(parts), latest.Parts)
			}
			for i, part := range parts {
				if part.Revision != latest.Revision || part.Part != i {
					return nil, fmt.Errorf("render: projection %s part %d does not match commit", latest.Revision, i)
				}
				if part.First {
					view[part.SourceSeq] = nil
				}
				view[part.SourceSeq] = append(view[part.SourceSeq], part.Events...)
			}
			parts = nil
			deltaApplied = true
		}
	}
	if deltaApplied {
		seqs := make([]uint64, 0, len(view))
		for seq := range view {
			seqs = append(seqs, seq)
		}
		sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
		latest.Events = nil
		for _, seq := range seqs {
			latest.Events = append(latest.Events, view[seq]...)
		}
	}
	if len(latest.Events) == 0 {
		return base, nil
	}
	updated, err := loadEvents(latest.Events)
	if err != nil {
		return nil, err
	}
	updated.Semantic = base.Semantic
	updated.events = events
	return updated, nil
}

func loadEvents(events []event.Event) (*Session, error) {
	s := &Session{
		Repairs:           make(map[string]*state.Repair),
		Pollution:         make(map[string]pollutionRecord),
		Obligations:       make(map[string]*state.Obligation),
		PointerOutcome:    make(map[string]int),
		PollutionByRepair: make(map[string][]string),
		events:            events,
	}

	for _, e := range events {
		if s.StreamID == "" {
			s.StreamID = e.StreamID
		}
		if e.Epoch > s.Epochs {
			s.Epochs = e.Epoch
		}
		switch e.Kind {
		case event.KindRecordObserved:
			s.Records++
			var p struct {
				Observation struct {
					OperatorTurn bool `json:"operator_turn"`
				} `json:"observation"`
			}
			if err := json.Unmarshal(e.Payload, &p); err == nil && p.Observation.OperatorTurn {
				s.OperatorTurns++
			}
		case event.KindMetricsComputed:
			var snap metrics.Snapshot
			if err := json.Unmarshal(e.Payload, &snap); err != nil {
				return nil, fmt.Errorf("render: decode metrics payload of event %s: %w", e.ID, err)
			}
			s.Snapshots = append(s.Snapshots, snap)
		case event.KindRegimeChanged:
			var p struct {
				To string `json:"to"`
			}
			if err := json.Unmarshal(e.Payload, &p); err == nil {
				s.RegimeOrder = append(s.RegimeOrder, p.To)
			}
		case event.KindCorrectionCandidate:
			s.Corrections++
		case event.KindSemanticCompleted, event.KindSemanticDisposition:
			var completion classify.Completion
			if err := json.Unmarshal(e.Payload, &completion); err != nil {
				return nil, fmt.Errorf("render: decode semantic completion payload of event %s: %w", e.ID, err)
			}
			s.Semantic.add(completion)
		case event.KindSemanticProjectionUpdated:
			var update state.SemanticProjectionUpdate
			if err := json.Unmarshal(e.Payload, &update); err != nil {
				return nil, err
			}
			s.Semantic.Applied += update.Applied
			s.Semantic.Changed += update.Changed
		case event.KindObligationRepeated:
			s.ObligationRepeats++
			s.absorbState(e)
		case event.KindObligationReleased:
			s.ObligationReleases++
			s.absorbState(e)
		case event.KindObligationRevived:
			s.ObligationRevivals++
			s.absorbState(e)
		case event.KindPointerResolved:
			var p struct {
				Outcome string `json:"outcome"`
			}
			if err := json.Unmarshal(e.Payload, &p); err == nil {
				s.PointerOutcome[p.Outcome]++
			}
		case event.KindRepairStatus:
			var p pollutionRecord
			var id struct {
				RepairID string `json:"repair_id"`
			}
			_ = json.Unmarshal(e.Payload, &p)
			if err := json.Unmarshal(e.Payload, &id); err == nil && id.RepairID != "" && p.Status != "" {
				s.Pollution[id.RepairID] = p
				s.PollutionByRepair[id.RepairID] = append(s.PollutionByRepair[id.RepairID], p.Status)
			}
			// A status the episode reached without a transition of its own still
			// carries the episode. Skipping it would leave the projection showing
			// an episode as open after its status said otherwise.
			s.absorbState(e)
		default:
			s.absorbState(e)
		}
	}
	sort.SliceStable(s.Snapshots, func(i, j int) bool { return s.Snapshots[i].Seq < s.Snapshots[j].Seq })
	s.Semantic.finish()
	return s, nil
}

// absorbState folds obligation and repair transition events. Each event carries
// the whole projected object, so the last event for an identifier is its
// current state.
func (s *Session) absorbState(e event.Event) {
	if e.Class != event.ClassDerived {
		return
	}
	switch {
	case strings.HasPrefix(e.Kind, "obligation_"):
		var p struct {
			Obligation *state.Obligation `json:"obligation"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil || p.Obligation == nil {
			return
		}
		if _, ok := s.Obligations[p.Obligation.ID]; !ok {
			s.ObligationOrder = append(s.ObligationOrder, p.Obligation.ID)
		}
		s.Obligations[p.Obligation.ID] = p.Obligation
	case strings.HasPrefix(e.Kind, "repair_"):
		var p struct {
			Repair *state.Repair `json:"repair"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil || p.Repair == nil {
			return
		}
		if _, ok := s.Repairs[p.Repair.ID]; !ok {
			s.RepairOrder = append(s.RepairOrder, p.Repair.ID)
		}
		s.Repairs[p.Repair.ID] = p.Repair
	}
}

func readSourceFile(dir string) (store.Source, error) {
	var src store.Source
	raw, err := readFile(filepath.Join(dir, store.FileSource))
	if err != nil {
		return src, err
	}
	if err := json.Unmarshal(raw, &src); err != nil {
		return src, fmt.Errorf("render: decode %s: %w", store.FileSource, err)
	}
	return src, nil
}

// Final returns the last computed snapshot.
func (s *Session) Final() metrics.Snapshot {
	if len(s.Snapshots) == 0 {
		return metrics.Snapshot{}
	}
	return s.Snapshots[len(s.Snapshots)-1]
}

// RegimesSeen lists regimes in first-occurrence order, starting from FLOW.
func (s *Session) RegimesSeen() []string {
	seen := map[string]bool{string(state.RegimeFlow): true}
	out := []string{string(state.RegimeFlow)}
	for _, r := range s.RegimeOrder {
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// FinalRegime is the regime of the last measured turn.
func (s *Session) FinalRegime() string {
	if r := s.Final().Regime; r != "" {
		return r
	}
	return string(state.RegimeFlow)
}

// MaxRepairDepth is the deepest episode in the stream.
func (s *Session) MaxRepairDepth() int {
	var max int
	for _, r := range s.Repairs {
		if r.Depth > max {
			max = r.Depth
		}
	}
	return max
}

// Summary is the disposable projection written to summary.json. It is always
// reproducible from the event files.
type Summary struct {
	StreamID           string              `json:"stream_id"`
	Source             store.Source        `json:"source"`
	Records            int                 `json:"records"`
	OperatorTurns      int                 `json:"operator_turns"`
	Epochs             uint64              `json:"epochs"`
	Corrections        int                 `json:"corrections"`
	ObligationRepeats  int                 `json:"obligation_repeat_events"`
	ObligationReleases int                 `json:"obligation_release_events"`
	ObligationRevivals int                 `json:"obligation_revival_events"`
	RegimesSeen        []string            `json:"regimes_seen"`
	FinalRegime        string              `json:"final_regime"`
	Repairs            []*state.Repair     `json:"repairs"`
	MaxDepth           int                 `json:"max_repair_depth"`
	Obligations        []*state.Obligation `json:"obligations"`
	// PollutionByRepair lists every pollution assessment of an episode, in
	// order. An episode with several correction cycles has one entry per cycle:
	// a clean last cycle does not erase an earlier polluted one.
	PollutionByRepair map[string][]string `json:"pollution_by_repair,omitempty"`
	Semantic          *SemanticStats      `json:"semantic_operations,omitempty"`
	Final             metrics.Snapshot    `json:"final"`
}

// Summarize builds the summary projection.
func (s *Session) Summarize() Summary {
	sum := Summary{
		StreamID:           s.StreamID,
		Source:             s.Source,
		Records:            s.Records,
		OperatorTurns:      s.OperatorTurns,
		Epochs:             s.Epochs + 1,
		Corrections:        s.Corrections,
		ObligationRepeats:  s.ObligationRepeats,
		ObligationReleases: s.ObligationReleases,
		ObligationRevivals: s.ObligationRevivals,
		RegimesSeen:        s.RegimesSeen(),
		FinalRegime:        s.FinalRegime(),
		MaxDepth:           s.MaxRepairDepth(),
		Final:              s.Final(),
	}
	if len(s.PollutionByRepair) > 0 {
		sum.PollutionByRepair = s.PollutionByRepair
	}
	if s.Semantic.Eligible > 0 {
		semantic := s.Semantic
		semantic.latencies = nil
		semantic.seen = nil
		sum.Semantic = &semantic
	}
	for _, id := range s.RepairOrder {
		// Copy before filling in the pollution status: a projection reports on
		// projected state, it does not write into it.
		r := *s.Repairs[id]
		if p, ok := s.Pollution[id]; ok && r.Pollution == "" {
			r.Pollution = p.Status
		}
		sum.Repairs = append(sum.Repairs, &r)
	}
	for _, id := range s.ObligationOrder {
		sum.Obligations = append(sum.Obligations, s.Obligations[id])
	}
	return sum
}

// Report renders the markdown report. Output depends only on stored events, so
// two runs over the same session produce identical bytes.
func (s *Session) Report() string {
	var b strings.Builder
	final := s.Final()

	fmt.Fprintf(&b, "# flow-indicator report: %s\n\n", s.StreamID)
	fmt.Fprintf(&b, "source: %s (%s adapter, %d records)\n\n", s.Source.Path, s.Source.Adapter, s.Records)

	fmt.Fprintf(&b, "regime %s after %d operator turns across %d epochs\n\n",
		s.FinalRegime(), s.OperatorTurns, s.Epochs+1)
	fmt.Fprintf(&b, "regimes seen: %s\n\n", strings.Join(s.RegimesSeen(), " -> "))

	b.WriteString("## metrics\n\n")
	b.WriteString("| family | metric | value | class |\n|---|---|---|---|\n")
	rows := [][4]string{
		{"transmission", "user chars, last turn", fmt.Sprintf("%d", final.UserChars), "observed"},
		{"transmission", "control baseline chars", valueOf(final.BaselineChars), "derived"},
		{"transmission", "baseline turns", fmt.Sprintf("%d", final.BaselineTurns), "derived"},
		{"transmission", "serialization inflation", ratioOf(final.SerializationInfl, "x"), "derived"},
		{"control", "forward-work share", percentOf(final.ForwardShare), "derived"},
		{"control", "control-plane burden", percentOf(final.ControlBurden), "derived"},
		{"control", "restate burden", percentOf(final.RestateShare), "derived"},
		{"obligation", "unresolved candidates", fmt.Sprintf("%d", final.UnresolvedObligations), "derived"},
		{"obligation", "repeated", fmt.Sprintf("%d", final.RepeatedObligations), "derived"},
		{"obligation", "violated", fmt.Sprintf("%d", final.ViolatedObligations), "derived"},
		{"obligation", "released", establishable(final, classify.CapObligationRelease, final.ReleasedObligations), "derived"},
		{"obligation", "superseded", establishable(final, classify.CapObligationSupersession, final.SupersededObligations), "derived"},
		{"obligation", "satisfied", establishable(final, classify.CapObligationSatisfaction, final.SatisfiedObligation), "derived"},
		{"obligation", "repeat ratio", ratioOf(final.RepeatRatio, ""), "derived"},
		{"obligation", "known still in force", "unknown", "derived"},
		{"dereference", "proxy", percentOf(final.Dereference), "derived (proxy)"},
		{"dereference", "resolved / missed / unknown", fmt.Sprintf("%d / %d / %d", final.PointerSuccess, final.PointerFailure, final.PointerUnknown), "derived"},
		{"recovery", "repair depth", fmt.Sprintf("%d", final.RepairDepth), "derived"},
		{"recovery", "repair chars", fmt.Sprintf("%d", final.RepairChars), "derived"},
		{"recovery", "repair records", fmt.Sprintf("%d", final.RepairRecords), "derived"},
		{"pollution", "expansion count", fmt.Sprintf("%d", final.Expansions), "derived"},
		{"pollution", "status", pollutionCell(final), "classified"},
	}
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", r[0], r[1], r[2], r[3])
	}

	if s.Semantic.Eligible > 0 {
		b.WriteString("\n## semantic operations\n\n")
		fmt.Fprintf(&b, "Coverage: %d/%d validated; %d requested, %d failed, %d timed out, %d canceled, %d dropped, %d pending; %d applied, %d changed.\n\n", s.Semantic.Validated, s.Semantic.Eligible, s.Semantic.Requested, s.Semantic.Failed, s.Semantic.TimedOut, s.Semantic.Canceled, s.Semantic.Dropped, s.Semantic.Pending, s.Semantic.Applied, s.Semantic.Changed)
		b.WriteString("These counters describe model work. In hybrid mode, completed answers may update the named semantic projection.\n\n")
		fmt.Fprintf(&b, "| recorded | completed | failed | timed out | canceled | p50 | p95 |\n|---|---|---|---|---|---|---|\n| %d | %d | %d | %d | %d | %s | %s |\n",
			s.Semantic.Recorded, s.Semantic.Completed, s.Semantic.Failed, s.Semantic.TimedOut, s.Semantic.Canceled,
			semanticLatency(s.Semantic.P50MS), semanticLatency(s.Semantic.P95MS))
		if len(s.Semantic.Classifiers) > 0 {
			fmt.Fprintf(&b, "\nclassifier identities: %s\n", strings.Join(s.Semantic.Classifiers, ", "))
		}
		if s.Semantic.LastStatus != "" {
			fmt.Fprintf(&b, "\nlatest outcome: source %d, %s", s.Semantic.LastSeq, s.Semantic.LastStatus)
			if s.Semantic.LastError != "" {
				fmt.Fprintf(&b, ": %s", s.Semantic.LastError)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n## repair episodes\n\n")
	if len(s.RepairOrder) == 0 {
		b.WriteString("none\n")
	} else {
		b.WriteString("| id | correction turn | depth | status | chars | records | pollution |\n|---|---|---|---|---|---|---|\n")
		for _, id := range s.RepairOrder {
			r := s.Repairs[id]
			pollution := r.Pollution
			if history, ok := s.PollutionByRepair[id]; ok {
				pollution = strings.Join(history, ", ")
			}
			fmt.Fprintf(&b, "| %s | %d | %d | %s | %d | %d | %s |\n",
				r.ID, r.FirstCorrectionTurn, r.Depth, r.Status, r.Chars, r.Records, orUnknown(pollution))
		}
	}

	b.WriteString("\n## classifier capabilities\n\n")
	if len(final.Capabilities) == 0 {
		b.WriteString("None recorded. Every fact needing a verification tier reads unknown in this\n")
		b.WriteString("session, and no count of such a fact is a measurement.\n")
	} else {
		b.WriteString("Facts the classifier behind this session could establish. A fact absent from\n")
		b.WriteString("this list was not measured, which is not the same as measured and unknown.\n\n")
		for _, c := range final.Capabilities {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}

	b.WriteString("\n## obligation candidate inventory\n\n")
	b.WriteString("Candidates introduced and not resolved since. Nothing here establishes that a\n")
	b.WriteString("requirement remains in force, only that no evidence removed it, so the\n")
	b.WriteString("known-active surface is unknown. Which resolutions were reachable at all is\n")
	b.WriteString("listed under classifier capabilities above.\n\n")
	if len(s.ObligationOrder) == 0 {
		b.WriteString("none\n")
	} else {
		b.WriteString("| id | turn | kind | status | repeats | text |\n|---|---|---|---|---|---|\n")
		for _, id := range s.ObligationOrder {
			o := s.Obligations[id]
			fmt.Fprintf(&b, "| %s | %d | %s | %s | %d | %s |\n",
				o.ID, o.IntroducedTurn, o.Kind, o.Status, o.RepeatCount, escapePipes(truncate(o.SourceText, 60)))
		}
	}

	b.WriteString("\n## turns\n\n")
	b.WriteString("| turn | epoch | regime | chars | si | forward | cpb | depth |\n|---|---|---|---|---|---|---|---|\n")
	for _, snap := range s.Snapshots {
		fmt.Fprintf(&b, "| %d | %d | %s | %d | %s | %s | %s | %d |\n",
			snap.Seq, snap.Epoch, snap.Regime, snap.UserChars,
			ratioOf(snap.SerializationInfl, "x"), percentOf(snap.ForwardShare),
			percentOf(snap.ControlBurden), snap.RepairDepth)
	}

	b.WriteString("\nEvery value above is reproducible from the event files in this session directory.\n")
	b.WriteString("The dereference figure is a proxy: it reports what the operator did next, not whether a reference resolved.\n")
	b.WriteString("Each derived transition stores the premise that licensed it; `inspect --turn <n>` prints them.\n")
	return b.String()
}

// Timeline renders one CSV row per measured operator turn.
func (s *Session) Timeline() string {
	var b strings.Builder
	b.WriteString(timelineHeader + "\n")
	for _, snap := range s.Snapshots {
		fmt.Fprintf(&b, "%d,%d,%s,%s,%d,%s,%s,%s,%s,%d,%d,%d,%d,%s,%s\n",
			snap.Seq, snap.Epoch, snap.Regime, snap.RegimeRule, snap.UserChars,
			csvValue(snap.BaselineChars), csvValue(snap.SerializationInfl),
			csvValue(snap.ForwardShare), csvValue(snap.ControlBurden),
			snap.UnresolvedObligations, snap.RepeatedObligations,
			snap.RepairDepth, snap.RepairChars,
			csvValue(snap.Dereference), orEmpty(snap.PollutionStatus))
	}
	return b.String()
}

// timelineHeader is a constant so two runs produce identical bytes.
const timelineHeader = "seq,epoch,regime,regime_rule,user_chars,baseline_chars,serialization_inflation," +
	"forward_work_share,control_plane_burden,unresolved_obligation_candidates,repeated_obligations," +
	"repair_depth,repair_chars,dereference_proxy,pollution_status"

func valueOf(v metrics.Value) string {
	if !v.Known {
		return "unknown"
	}
	return fmt.Sprintf("%.0f", v.Num)
}

func semanticLatency(v *int64) string {
	if v == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d ms", *v)
}

func ratioOf(v metrics.Value, suffix string) string {
	if !v.Known {
		return "unknown"
	}
	return fmt.Sprintf("%.1f%s", v.Num, suffix)
}

func percentOf(v metrics.Value) string {
	if !v.Known {
		return "unknown"
	}
	return fmt.Sprintf("%.0f%%", v.Num*100)
}

func csvValue(v metrics.Value) string {
	if !v.Known {
		return "unknown"
	}
	return fmt.Sprintf("%.4f", v.Num)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// establishable renders a count the classifier could reach, and says so
// otherwise.
//
// A zero from a build that cannot establish the fact is not a count. Printing
// it as one puts the session's authority behind a number that was never
// measured, and the reader has no way back to the configuration that produced
// it.
func establishable(s metrics.Snapshot, c classify.Capability, n int) string {
	if !can(s, c) {
		return "not established by this classifier"
	}
	return fmt.Sprintf("%d", n)
}

// pollutionCell renders the repair-pollution status, separating an unknown
// measurement from a build that cannot make the measurement.
func pollutionCell(s metrics.Snapshot) string {
	if !can(s, classify.CapVerifiedRepair) {
		return "not established by this classifier"
	}
	return orUnknown(s.PollutionStatus)
}

func orEmpty(s string) string { return s }

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func escapePipes(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
