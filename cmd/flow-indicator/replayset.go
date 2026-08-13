package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TGPSKI/flow-indicator/internal/classify"
	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/event"
	"github.com/TGPSKI/flow-indicator/internal/harness"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
	"github.com/TGPSKI/flow-indicator/internal/state"
	"github.com/TGPSKI/flow-indicator/internal/store"
	"github.com/TGPSKI/flow-indicator/internal/stream"
)

// replay-set runs the ordinary replay path over a manifest of sources and
// writes one machine-readable row per session.
//
// It is a validation surface, not a corpus product. Every field is a count the
// instrument already produces or a fact read from the source bytes; nothing
// here scores a session or labels its health. A reviewer uses the rows to pick
// which sources to read, and the anchors to find where to start reading.

// setRow is one session's replay result. Unknown is null, never zero.
type setRow struct {
	SourcePath   string `json:"source_path"`
	SourceSHA256 string `json:"source_sha256"`
	StreamID     string `json:"stream_id"`

	// Error is the reason this source produced no measurement. Every other
	// field is then unset: a source that failed to decode has no counts.
	Error string `json:"error,omitempty"`

	RecordCount     int      `json:"record_count"`
	OperatorTurns   int      `json:"operator_turn_count"`
	StartTime       *string  `json:"start_time"`
	EndTime         *string  `json:"end_time"`
	DurationSeconds *float64 `json:"duration_seconds"`

	Classifier        string `json:"classifier_name"`
	ClassifierVersion string `json:"classifier_version"`
	ClassifierHash    string `json:"classifier_hash"`

	Interrupts       int `json:"interrupt_count"`
	Corrections      int `json:"correction_count"`
	CompactSummaries int `json:"compact_summary_count"`

	RegimesSeen   []string `json:"regimes_seen"`
	FinalRegime   string   `json:"final_regime"`
	FirstDrift    *mark    `json:"first_drift"`
	FirstRecovery *mark    `json:"first_recovery"`
	FirstThrash   *mark    `json:"first_thrash"`

	MaxKnownSI *float64 `json:"max_known_si"`
	MaxCPB     *float64 `json:"max_cpb"`

	RepairEpisodes int `json:"repair_episode_count"`
	MaxRepairChars int `json:"max_repair_chars"`
	MaxRepairDepth int `json:"max_repair_depth"`

	PointerSuccess int `json:"pointer_success"`
	PointerFailure int `json:"pointer_failure"`
	PointerUnknown int `json:"pointer_unknown"`
}

// mark is a point in a session: where it is in the stream and when it happened.
type mark struct {
	Seq  uint64  `json:"seq"`
	Time *string `json:"time"`
	Rule string  `json:"rule,omitempty"`
}

// anchor is a source coordinate worth reading. Every anchor drills back to the
// source path and the byte offset of the record it names.
type anchor struct {
	SourcePath string  `json:"source_path"`
	StreamID   string  `json:"stream_id"`
	Kind       string  `json:"kind"`
	Seq        uint64  `json:"seq"`
	Offset     int64   `json:"offset"`
	Time       *string `json:"time"`
	Detail     string  `json:"detail,omitempty"`
}

func replaySet(args []string) error {
	fs := flag.NewFlagSet("replay-set", flag.ExitOnError)
	var common commonFlags
	common.bind(fs)
	adapterName := fs.String("adapter", "claude-code", "adapter or harness name: "+strings.Join(harness.Names(), ", "))
	manifest := fs.String("manifest", "", "file listing one source path per line")
	output := fs.String("output", "", "JSONL file to write one row per session to")
	anchors := fs.String("anchors", "", "JSONL file for source coordinates (default: <output> with -anchors)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifest == "" || *output == "" {
		return errors.New("replay-set: --manifest and --output are required")
	}
	paths, err := readManifest(*manifest)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("replay-set: %s lists no sources", *manifest)
	}
	anchorPath := *anchors
	if anchorPath == "" {
		anchorPath = strings.TrimSuffix(*output, ".jsonl") + "-anchors.jsonl"
	}

	cfg, root, err := common.load()
	if err != nil {
		return err
	}
	if _, err := harness.New(*adapterName); err != nil {
		return err
	}
	rules, err := common.rules()
	if err != nil {
		return err
	}
	cls, err := classifierFor(cfg, rules, false)
	if err != nil {
		return err
	}

	rows, err := os.Create(*output)
	if err != nil {
		return fmt.Errorf("replay-set: create %s: %w", *output, err)
	}
	defer rows.Close()
	marks, err := os.Create(anchorPath)
	if err != nil {
		return fmt.Errorf("replay-set: create %s: %w", anchorPath, err)
	}
	defer marks.Close()

	rowEnc := json.NewEncoder(rows)
	markEnc := json.NewEncoder(marks)
	ctx := context.Background()
	for _, path := range paths {
		row, found := replayOne(ctx, cfg, root, *adapterName, cls, path)
		if err := rowEnc.Encode(row); err != nil {
			return err
		}
		for _, a := range found {
			if err := markEnc.Encode(a); err != nil {
				return err
			}
		}
		if row.Error != "" {
			fmt.Printf("%-40s  %s\n", row.StreamID, row.Error)
			continue
		}
		fmt.Printf("%-40s  %-8s  %d records  %d operator turns  %s\n",
			row.StreamID, row.FinalRegime, row.RecordCount, row.OperatorTurns,
			strings.Join(row.RegimesSeen, "→"))
	}
	fmt.Printf("\n%d sources: %s, %s\n", len(paths), *output, anchorPath)
	return nil
}

// readManifest reads one source path per line. Blank lines and lines beginning
// with # are ignored.
func readManifest(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("replay-set: read manifest %s: %w", path, err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// replayOne replays one source through the ordinary projector path and returns
// its row and its anchors. A source that cannot be read or decoded returns a
// row carrying the reason and no counts.
func replayOne(ctx context.Context, cfg config.Config, root, harnessName string, cls classify.Classifier, path string) (setRow, []anchor) {
	row := setRow{
		SourcePath:        path,
		StreamID:          strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Classifier:        cls.Name(),
		ClassifierVersion: cls.Version(),
		ClassifierHash:    cls.Hash(),
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		row.Error = err.Error()
		return row, nil
	}
	row.SourcePath = abs
	raw, err := os.ReadFile(abs)
	if err != nil {
		row.Error = err.Error()
		return row, nil
	}
	sum := sha256.Sum256(raw)
	row.SourceSHA256 = hex.EncodeToString(sum[:])

	records, err := harness.DecodeBytes(harnessName, abs, "", "", raw)
	if err != nil {
		row.Error = err.Error()
		return row, nil
	}
	row.RecordCount = len(records)
	if id := streamIDOf(records, abs); id != "" {
		row.StreamID = id
	}

	session, err := store.OpenSession(root, row.StreamID, true)
	if err != nil {
		row.Error = err.Error()
		return row, nil
	}
	projector := state.New(cfg, cls, row.StreamID)

	var found []anchor
	var snapshots []metrics.Snapshot
	regime := "FLOW"
	row.RegimesSeen = []string{regime}

	for _, rec := range records {
		if rec.IsOperatorTurn() {
			row.OperatorTurns++
		}
		if rec.Metadata[stream.MetaInputMode] == stream.InputCompactSummary {
			row.CompactSummaries++
		}
		if !rec.Timestamp.IsZero() {
			if row.StartTime == nil {
				row.StartTime = timePtr(rec.Timestamp)
			}
			row.EndTime = timePtr(rec.Timestamp)
		}
		events := projector.Push(ctx, rec)
		if err := session.AppendAll(events); err != nil {
			session.Close()
			row.Error = err.Error()
			return row, found
		}
		at := timePtr(rec.Timestamp)
		for _, e := range events {
			switch e.Kind {
			case event.KindCorrectionCandidate:
				row.Corrections++
				found = append(found, anchorOf(row, e, "correction", at, ""))
			case event.KindOperatorInterrupt:
				row.Interrupts++
				found = append(found, anchorOf(row, e, "interrupt", at, ""))
			case event.KindRepairOpened:
				found = append(found, anchorOf(row, e, "repair_open", at, ""))
			case event.KindMetricsComputed:
				var snap metrics.Snapshot
				if json.Unmarshal(e.Payload, &snap) == nil {
					snapshots = append(snapshots, snap)
				}
			case event.KindRegimeChanged:
				var p struct {
					To   string `json:"to"`
					Rule string `json:"rule"`
				}
				if json.Unmarshal(e.Payload, &p) != nil {
					continue
				}
				regime = p.To
				row.RegimesSeen = append(row.RegimesSeen, p.To)
				m := &mark{Seq: e.Seq, Time: at, Rule: p.Rule}
				switch p.To {
				case "DRIFT":
					if row.FirstDrift == nil {
						row.FirstDrift = m
					}
				case "RECOVERY":
					if row.FirstRecovery == nil {
						row.FirstRecovery = m
					}
				case "THRASH":
					if row.FirstThrash == nil {
						row.FirstThrash = m
					}
					found = append(found, anchorOf(row, e, "thrash_transition", at, p.Rule))
				}
				if p.To != "FLOW" && !hasAnchor(found, "first_non_flow") {
					found = append(found, anchorOf(row, e, "first_non_flow", at, p.Rule))
				}
			}
		}
	}
	if err := session.AppendAll(projector.Finish()); err != nil {
		session.Close()
		row.Error = err.Error()
		return row, found
	}
	if err := session.Close(); err != nil {
		row.Error = err.Error()
		return row, found
	}
	if err := session.WriteSource(store.Source{
		StreamID: row.StreamID, Adapter: harnessName, Path: abs,
		Bytes: int64(len(raw)), Records: len(records), Offset: int64(len(raw)),
	}); err != nil {
		row.Error = err.Error()
		return row, found
	}
	if err := writeProjections(session.Dir); err != nil {
		row.Error = err.Error()
		return row, found
	}

	row.FinalRegime = regime
	row.RegimesSeen = distinct(row.RegimesSeen)
	if row.StartTime != nil && row.EndTime != nil {
		start, _ := time.Parse(time.RFC3339, *row.StartTime)
		end, _ := time.Parse(time.RFC3339, *row.EndTime)
		d := end.Sub(start).Seconds()
		row.DurationSeconds = &d
	}
	for _, r := range projector.Repairs() {
		row.RepairEpisodes++
		if r.Chars > row.MaxRepairChars {
			row.MaxRepairChars = r.Chars
		}
		if r.Depth > row.MaxRepairDepth {
			row.MaxRepairDepth = r.Depth
		}
	}
	final := projector.Snapshot()
	row.PointerSuccess, row.PointerFailure, row.PointerUnknown =
		final.PointerSuccess, final.PointerFailure, final.PointerUnknown
	for _, s := range snapshots {
		if s.SerializationInfl.Known && (row.MaxKnownSI == nil || s.SerializationInfl.Num > *row.MaxKnownSI) {
			v := s.SerializationInfl.Num
			row.MaxKnownSI = &v
		}
		if s.ControlBurden.Known && (row.MaxCPB == nil || s.ControlBurden.Num > *row.MaxCPB) {
			v := s.ControlBurden.Num
			row.MaxCPB = &v
		}
	}
	found = append(found, topInflationAnchors(row, records, snapshots)...)
	return row, found
}

// topInflationAnchors names the three highest known-inflation turns, so a
// reviewer can read the turns that cost the most to send without scanning the
// timeline.
func topInflationAnchors(row setRow, records []stream.Record, snapshots []metrics.Snapshot) []anchor {
	known := make([]metrics.Snapshot, 0, len(snapshots))
	for _, s := range snapshots {
		if s.SerializationInfl.Known {
			known = append(known, s)
		}
	}
	sort.SliceStable(known, func(i, j int) bool {
		return known[i].SerializationInfl.Num > known[j].SerializationInfl.Num
	})
	if len(known) > 3 {
		known = known[:3]
	}
	bySeq := make(map[uint64]stream.Record, len(records))
	for i, r := range records {
		bySeq[uint64(i+1)] = r
	}
	out := make([]anchor, 0, len(known))
	for _, s := range known {
		rec := bySeq[s.Seq]
		out = append(out, anchor{
			SourcePath: row.SourcePath,
			StreamID:   row.StreamID,
			Kind:       "top_si",
			Seq:        s.Seq,
			Offset:     rec.Source.Offset,
			Time:       timePtr(rec.Timestamp),
			Detail:     fmt.Sprintf("%.1fx over %d chars", s.SerializationInfl.Num, s.UserChars),
		})
	}
	return out
}

func anchorOf(row setRow, e event.Event, kind string, at *string, detail string) anchor {
	return anchor{
		SourcePath: row.SourcePath,
		StreamID:   row.StreamID,
		Kind:       kind,
		Seq:        e.Seq,
		Offset:     e.Source.Offset,
		Time:       at,
		Detail:     detail,
	}
}

func hasAnchor(as []anchor, kind string) bool {
	for _, a := range as {
		if a.Kind == kind {
			return true
		}
	}
	return false
}

func distinct(in []string) []string {
	var out []string
	for _, s := range in {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}

func timePtr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
