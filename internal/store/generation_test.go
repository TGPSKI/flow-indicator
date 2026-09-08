package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForcedGenerationRemovesPriorProjections(t *testing.T) {
	root := t.TempDir()
	s, err := OpenSession(root, "generation", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{FileSource, FileSummary, FileMetricsJSON, FileReport, FileTimeline} {
		if err := os.WriteFile(s.Path(name), []byte("old generation"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSession(root, "generation", true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{FileSource, FileSummary, FileMetricsJSON, FileReport, FileTimeline} {
		if _, err := os.Stat(filepath.Join(s.Dir, name)); !os.IsNotExist(err) {
			t.Fatalf("stale %s survives: %v", name, err)
		}
	}
}
