package panel

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The package is reusable only for as long as it depends on nothing. An import
// of the standard library has no dot in its first segment; every other import
// path does, because it starts with a host.
//
// Direct imports are enough to check: a set of stdlib-only direct imports is
// stdlib-only transitively. Every file is read, build tags included, since a
// platform-specific file is where a stray import would sit unnoticed.
func TestPackageImportsOnlyTheStandardLibrary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	seen := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" {
			continue
		}
		seen++
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
				t.Errorf("%s imports %s; panel must depend on nothing but the standard library",
					name, path)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no Go files read; the check proved nothing")
	}
}
