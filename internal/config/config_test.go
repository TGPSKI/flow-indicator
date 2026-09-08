package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultsAreValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default configuration is invalid: %v", err)
	}
}

func TestUnknownKeyIsAnError(t *testing.T) {
	_, err := Load(write(t, `{"window": {"rolling_turns": 30}, "extra": 1}`))
	if err == nil {
		t.Fatal("unknown key accepted")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Errorf("error does not name the unknown key: %v", err)
	}
}

func TestInvalidValueIsAnErrorNotAFallback(t *testing.T) {
	_, err := Load(write(t, `{"thresholds": {"thrash_repair_depth": 0}}`))
	if err == nil {
		t.Fatal("invalid threshold accepted")
	}
	if !strings.Contains(err.Error(), "thresholds.thrash_repair_depth") {
		t.Errorf("error does not name the field: %v", err)
	}
}

func TestMissingNamedFileIsAnError(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("a configuration file the operator named must exist")
	}
}

func TestPartialFileKeepsDefaults(t *testing.T) {
	cfg, err := Load(write(t, `{"privacy": {"snippet_chars": 40}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Privacy.SnippetChars != 40 {
		t.Errorf("snippet_chars = %d, want 40", cfg.Privacy.SnippetChars)
	}
	if cfg.Window.RollingTurns != Default().Window.RollingTurns {
		t.Errorf("rolling_turns = %d, want the default %d", cfg.Window.RollingTurns, Default().Window.RollingTurns)
	}
}

func TestOpenAIModeRequiresEndpointAndModel(t *testing.T) {
	_, err := Load(write(t, `{"classifier": {"mode": "openai-compatible"}}`))
	if err == nil {
		t.Fatal("openai-compatible mode accepted without an endpoint")
	}
	cfg, err := Load(write(t, `{"classifier": {"mode": "openai-compatible", "endpoint": "http://localhost:8000/v1/chat/completions", "model": "local"}}`))
	if err != nil {
		t.Fatalf("valid openai-compatible configuration rejected: %v", err)
	}
	if cfg.Classifier.Model != "local" {
		t.Errorf("model = %q, want local", cfg.Classifier.Model)
	}
}

func TestHybridModeRequiresEndpointAndModel(t *testing.T) {
	_, err := Load(write(t, `{"classifier": {"mode": "hybrid"}}`))
	if err == nil {
		t.Fatal("hybrid mode accepted without an endpoint")
	}
	cfg, err := Load(write(t, `{"classifier": {"mode": "hybrid", "endpoint": "http://127.0.0.1:8000/v1/chat/completions", "model": "local", "live_deadline_ms": 150, "workers": 2, "max_queue": 8}}`))
	if err != nil {
		t.Fatalf("valid hybrid configuration rejected: %v", err)
	}
	if cfg.Classifier.LiveDeadlineMS != 150 || cfg.Classifier.Workers != 2 || cfg.Classifier.MaxQueue != 8 {
		t.Fatalf("hybrid settings = %+v", cfg.Classifier)
	}
}

func TestDeferredModeRequiresEndpointAndModel(t *testing.T) {
	_, err := Load(write(t, `{"classifier": {"mode": "deferred"}}`))
	if err == nil {
		t.Fatal("deferred mode accepted without an endpoint")
	}
	if _, err := Load(write(t, `{"classifier": {"mode": "deferred", "endpoint": "http://127.0.0.1:8000/v1/chat/completions", "model": "local"}}`)); err != nil {
		t.Fatalf("valid deferred configuration rejected: %v", err)
	}
}

func TestUnknownClassifierModeIsAnError(t *testing.T) {
	_, err := Load(write(t, `{"classifier": {"mode": "magic"}}`))
	if err == nil || !strings.Contains(err.Error(), "classifier.mode") {
		t.Fatalf("error = %v, want one naming classifier.mode", err)
	}
}

func TestPathsFollowXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/xdg-config/flow-indicator/config.json" {
		t.Errorf("config path = %s", got)
	}
	data, err := DataPath()
	if err != nil {
		t.Fatal(err)
	}
	if data != "/tmp/xdg-data/flow-indicator" {
		t.Errorf("data path = %s", data)
	}
}
