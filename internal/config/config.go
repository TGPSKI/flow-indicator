// Package config loads the operator's configuration.
//
// Loading is strict: an unknown key or an invalid value is an error. A
// malformed configuration never falls back to defaults, because a silent
// fallback changes what the metrics mean without saying so.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Classifier modes.
const (
	ModeNone      = "none"
	ModeHeuristic = "heuristic"
	ModeOpenAI    = "openai-compatible"
	// ModeHybrid keeps heuristic projection on the live path and sends a copy
	// to a bounded local-model worker for deferred semantic evidence.
	ModeHybrid = "hybrid"
)

// Config is the whole configuration file.
type Config struct {
	Window     Window     `json:"window"`
	Thresholds Thresholds `json:"thresholds"`
	Classifier Classifier `json:"classifier"`
	Privacy    Privacy    `json:"privacy"`
}

// Window holds the turn counts that bound rolling calculations. All counts are
// in eligible operator turns.
type Window struct {
	RollingTurns              int `json:"rolling_turns"`
	CorrectionDurabilityTurns int `json:"correction_durability_turns"`
	DereferenceOutcomeTurns   int `json:"dereference_outcome_turns"`
	// TrendDurabilityTurns is how many consecutive operator turns a metric
	// must hold a new quality band before the crossing is reported. One turn
	// in a new band is a turn, not a trend.
	TrendDurabilityTurns int `json:"trend_durability_turns"`
}

// Thresholds holds the values a regime rule compares against.
type Thresholds struct {
	MinimumControlBaseline  int     `json:"minimum_control_baseline"`
	ThrashRepairDepth       int     `json:"thrash_repair_depth"`
	SerializationWarn       float64 `json:"serialization_warn"`
	SerializationHigh       float64 `json:"serialization_high"`
	RepeatedObligationsWarn int     `json:"repeated_obligations_warn"`
}

// Classifier selects how records are interpreted.
type Classifier struct {
	Mode           string `json:"mode"`
	Endpoint       string `json:"endpoint"`
	Model          string `json:"model"`
	LiveDeadlineMS int    `json:"live_deadline_ms"`
	Workers        int    `json:"workers"`
	MaxQueue       int    `json:"max_queue"`
}

// Privacy bounds what source text is written to disk.
type Privacy struct {
	StoreText     bool `json:"store_text"`
	StoreSnippets bool `json:"store_snippets"`
	SnippetChars  int  `json:"snippet_chars"`
}

// Default is the configuration used when no file exists.
func Default() Config {
	return Config{
		Window: Window{
			RollingTurns:              30,
			CorrectionDurabilityTurns: 10,
			DereferenceOutcomeTurns:   3,
			TrendDurabilityTurns:      2,
		},
		Thresholds: Thresholds{
			MinimumControlBaseline:  8,
			ThrashRepairDepth:       3,
			SerializationWarn:       3.0,
			SerializationHigh:       6.0,
			RepeatedObligationsWarn: 2,
		},
		Classifier: Classifier{
			Mode: ModeHeuristic, LiveDeadlineMS: 200, Workers: 1, MaxQueue: 32,
		},
		Privacy: Privacy{
			StoreText:     false,
			StoreSnippets: true,
			SnippetChars:  160,
		},
	}
}

// Path returns the configuration file path: $XDG_CONFIG_HOME/flow-indicator/
// config.json, falling back to ~/.config.
func Path() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: locate home directory: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "flow-indicator", "config.json"), nil
}

// Load reads path. A missing file at the default location yields defaults; a
// missing file the operator named explicitly is an error.
func Load(path string) (Config, error) {
	cfg := Default()
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: decode %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadDefaultPath loads the configured path, returning defaults when no file
// exists there.
func LoadDefaultPath() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	return Load(path)
}

// Validate rejects values that would make a metric meaningless.
func (c Config) Validate() error {
	checks := []struct {
		name string
		got  int
		min  int
	}{
		{"window.rolling_turns", c.Window.RollingTurns, 1},
		{"window.correction_durability_turns", c.Window.CorrectionDurabilityTurns, 1},
		{"window.trend_durability_turns", c.Window.TrendDurabilityTurns, 1},
		{"window.dereference_outcome_turns", c.Window.DereferenceOutcomeTurns, 1},
		{"thresholds.minimum_control_baseline", c.Thresholds.MinimumControlBaseline, 1},
		{"thresholds.thrash_repair_depth", c.Thresholds.ThrashRepairDepth, 1},
		{"thresholds.repeated_obligations_warn", c.Thresholds.RepeatedObligationsWarn, 1},
		{"privacy.snippet_chars", c.Privacy.SnippetChars, 0},
		{"classifier.live_deadline_ms", c.Classifier.LiveDeadlineMS, 1},
		{"classifier.workers", c.Classifier.Workers, 1},
		{"classifier.max_queue", c.Classifier.MaxQueue, 1},
	}
	for _, ch := range checks {
		if ch.got < ch.min {
			return fmt.Errorf("config: %s must be >= %d, got %d", ch.name, ch.min, ch.got)
		}
	}
	if c.Thresholds.SerializationWarn <= 0 {
		return fmt.Errorf("config: thresholds.serialization_warn must be > 0, got %v", c.Thresholds.SerializationWarn)
	}
	if c.Thresholds.SerializationHigh < c.Thresholds.SerializationWarn {
		return fmt.Errorf("config: thresholds.serialization_high must be >= thresholds.serialization_warn, got %v", c.Thresholds.SerializationHigh)
	}
	switch c.Classifier.Mode {
	case ModeNone, ModeHeuristic:
	case ModeOpenAI, ModeHybrid:
		if c.Classifier.Endpoint == "" {
			return errors.New("config: classifier.endpoint is required when classifier.mode is openai-compatible")
		}
		if c.Classifier.Model == "" {
			return errors.New("config: classifier.model is required when classifier.mode is openai-compatible")
		}
	default:
		return fmt.Errorf("config: classifier.mode must be none, heuristic, openai-compatible or hybrid, got %q", c.Classifier.Mode)
	}
	return nil
}

// DataPath returns the storage root: $XDG_DATA_HOME/flow-indicator, falling
// back to ~/.local/share.
func DataPath() (string, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: locate home directory: %w", err)
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "flow-indicator"), nil
}
