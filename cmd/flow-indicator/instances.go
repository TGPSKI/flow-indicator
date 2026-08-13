package main

import (
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

	"github.com/TGPSKI/flow-indicator/internal/config"
	"github.com/TGPSKI/flow-indicator/internal/metrics"
)

// Instance records are operational projections, not events. They sit beside
// sessions so clock and process data cannot enter replayed event artifacts.
const (
	instanceVersion = 1
	instanceTTL     = 30 * time.Second
)

type instanceMetrics struct {
	SerializationInflation      metrics.Value `json:"serialization_inflation"`
	ForwardWorkShare            metrics.Value `json:"forward_work_share"`
	ControlPlaneBurden          metrics.Value `json:"control_plane_burden"`
	UnresolvedObligations       int           `json:"unresolved_obligation_candidates"`
	RepeatedObligations         int           `json:"repeated_obligations"`
	RepairDepth                 int           `json:"repair_depth"`
	DereferenceReliabilityProxy metrics.Value `json:"dereference_reliability_proxy"`
}

type instanceReport struct {
	Version                int             `json:"version"`
	InstanceID             string          `json:"instance_id"`
	PID                    int             `json:"pid"`
	Hostname               string          `json:"hostname"`
	StartedAt              time.Time       `json:"started_at"`
	ReportedAt             time.Time       `json:"reported_at"`
	ExpiresAt              time.Time       `json:"expires_at"`
	StreamID               string          `json:"stream_id"`
	Harness                string          `json:"harness"`
	SourcePath             string          `json:"source_path"`
	Mark                   string          `json:"mark"`
	Root                   string          `json:"root"`
	Project                string          `json:"project"`
	PaneID                 string          `json:"pane_id"`
	WorkspaceID            string          `json:"workspace_id"`
	Regime                 string          `json:"regime"`
	TurnIndex              int             `json:"turn_index"`
	Seq                    uint64          `json:"seq"`
	Epoch                  uint64          `json:"epoch"`
	Elapsed                string          `json:"elapsed"`
	Trend                  string          `json:"trend"`
	Metrics                instanceMetrics `json:"metrics"`
	ClassifierCapabilities []string        `json:"classifier_capabilities"`
}

type instanceState struct {
	Regime   string
	Snapshot metrics.Snapshot
	Elapsed  string
	Trend    string
	Mark     string
}

type instanceEmitter struct {
	dir    string
	path   string
	report instanceReport
	lastAt time.Time
}

func newInstanceEmitter(root, streamID, harnessName, sourcePath, sessionRoot, pane string) (*instanceEmitter, error) {
	host, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("instances: hostname: %w", err)
	}
	started := time.Now().UTC()
	pid := os.Getpid()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", host, pid, started.UnixNano())))
	id := hex.EncodeToString(sum[:])[:16]
	dir := filepath.Join(root, "instances")
	return &instanceEmitter{
		dir: dir, path: filepath.Join(dir, id+".json"),
		report: instanceReport{Version: instanceVersion, InstanceID: id, PID: pid,
			Hostname: host, StartedAt: started, StreamID: streamID, Harness: harnessName,
			SourcePath: sourcePath, Root: sessionRoot, Project: filepath.Base(sessionRoot),
			PaneID: pane, WorkspaceID: os.Getenv("HERDR_WORKSPACE_ID")},
	}, nil
}

func (e *instanceEmitter) Report(s instanceState) error {
	now := time.Now().UTC()
	if now.Sub(e.lastAt) < herdrRefresh {
		return nil
	}
	e.report.ReportedAt, e.report.ExpiresAt = now, now.Add(instanceTTL)
	e.report.Regime, e.report.TurnIndex, e.report.Seq, e.report.Epoch = s.Regime, s.Snapshot.TurnIndex, s.Snapshot.Seq, s.Snapshot.Epoch
	e.report.Elapsed, e.report.Trend, e.report.Mark = s.Elapsed, s.Trend, s.Mark
	e.report.Metrics = instanceMetrics{SerializationInflation: s.Snapshot.SerializationInfl, ForwardWorkShare: s.Snapshot.ForwardShare,
		ControlPlaneBurden: s.Snapshot.ControlBurden, UnresolvedObligations: s.Snapshot.UnresolvedObligations,
		RepeatedObligations: s.Snapshot.RepeatedObligations, RepairDepth: s.Snapshot.RepairDepth,
		DereferenceReliabilityProxy: s.Snapshot.Dereference}
	e.report.ClassifierCapabilities = append([]string(nil), s.Snapshot.Capabilities...)
	if err := os.MkdirAll(e.dir, 0o700); err != nil {
		return fmt.Errorf("instances: create %s: %w", e.dir, err)
	}
	raw, err := json.Marshal(e.report)
	if err != nil {
		return fmt.Errorf("instances: marshal %s: %w", e.path, err)
	}
	tmp, err := os.CreateTemp(e.dir, ".instance-*.json")
	if err != nil {
		return fmt.Errorf("instances: create temporary record: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("instances: write temporary record: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("instances: chmod temporary record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("instances: close temporary record: %w", err)
	}
	if err := os.Rename(name, e.path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("instances: replace %s: %w", e.path, err)
	}
	e.lastAt = now
	return nil
}

func (e *instanceEmitter) Close() { _ = os.Remove(e.path) }

func instances(args []string) error {
	fs := flag.NewFlagSet("instances", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "storage root (XDG data path)")
	asJSON := fs.Bool("json", false, "write the public instance document")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*asJSON {
		return errors.New("instances: --json is required")
	}
	root := *dataDir
	if root == "" {
		var err error
		root, err = configDataPath()
		if err != nil {
			return err
		}
	}
	items, err := liveInstances(filepath.Join(root, "instances"), time.Now().UTC())
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		CollectedAt time.Time        `json:"collected_at"`
		Items       []instanceReport `json:"items"`
	}{CollectedAt: time.Now().UTC(), Items: items})
}

func configDataPath() (string, error) { return config.DataPath() }

func liveInstances(dir string, now time.Time) ([]instanceReport, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []instanceReport{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("instances: read %s: %w", dir, err)
	}
	host, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("instances: hostname: %w", err)
	}
	// Empty is a successful observation, and its public JSON form must be []
	// rather than null so collectors can distinguish it from a schema failure.
	out := make([]instanceReport, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("instances: read %s: %w", path, err)
		}
		var report instanceReport
		if err := json.Unmarshal(raw, &report); err != nil {
			return nil, fmt.Errorf("instances: decode %s: %w", path, err)
		}
		if report.Version != instanceVersion || report.InstanceID == "" || report.StreamID == "" {
			return nil, fmt.Errorf("instances: invalid record %s", path)
		}
		if !instanceLive(report, host, now) {
			if !report.ExpiresAt.IsZero() && now.After(report.ExpiresAt.Add(instanceTTL)) {
				_ = os.Remove(path)
			}
			continue
		}
		out = append(out, report)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstanceID < out[j].InstanceID })
	return out, nil
}

func instanceLive(r instanceReport, host string, now time.Time) bool {
	if r.Hostname != host || r.PID < 1 || r.ReportedAt.IsZero() || now.After(r.ExpiresAt) {
		return false
	}
	_, err := os.Stat(filepath.Join("/proc", fmt.Sprintf("%d", r.PID)))
	return err == nil
}
