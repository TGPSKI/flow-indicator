package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TGPSKI/flow-indicator/internal/config"
)

func TestConfigInitWritesCompleteDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	if err := configInit([]string{"--config", path}); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != config.Default() {
		t.Errorf("config = %+v, want %+v", got, config.Default())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestConfigInitUsesXDGPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	if err := configInit(nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "flow-indicator", "config.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("init did not create the XDG configuration file: %v", err)
	}
}

func TestConfigInitRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := configInit([]string{"--config", path})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v, want existing-file error", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "preserve" {
		t.Errorf("existing file became %q", got)
	}
}

func TestConfigShowPrintsEffectiveConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"privacy":{"snippet_chars":40}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := configShowTo(&out, []string{"--config", path}); err != nil {
		t.Fatal(err)
	}
	var got config.Config
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("show emitted invalid JSON: %v", err)
	}
	want := config.Default()
	want.Privacy.SnippetChars = 40
	if got != want {
		t.Errorf("config = %+v, want %+v", got, want)
	}
}

func TestConfigShowUsesDefaultsWhenXDGFileIsAbsent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer
	if err := configShowTo(&out, nil); err != nil {
		t.Fatal(err)
	}
	var got config.Config
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("show emitted invalid JSON: %v", err)
	}
	if got != config.Default() {
		t.Errorf("config = %+v, want %+v", got, config.Default())
	}
}

func TestConfigCommandRequiresKnownAction(t *testing.T) {
	err := configCmd([]string{"apply"})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("error = %v, want unknown-action error", err)
	}
}
