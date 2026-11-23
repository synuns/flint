package commentlint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoNarrativeComments(t *testing.T) {
	hits, err := NonDirectives("../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Fatalf("non-directive comments: %d\n%s", len(hits), strings.Join(hits, "\n"))
	}
}

func TestNonDirectivesDetectsInjection(t *testing.T) {
	dir := t.TempDir()
	src := "package a\n\nfunc f() {}\n\n// note\nvar _ = 0\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	hits, err := NonDirectives(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0], "// note") {
		t.Fatalf("hits = %v, want 1 containing // note", hits)
	}
}

func TestNonDirectivesAllowsDirectives(t *testing.T) {
	dir := t.TempDir()
	src := "//go:build ignore\n\npackage main\n\n//go:embed seed.jsonl\nvar b []byte\n\n//nolint:staticcheck\nvar _ = 0\n\n// +build linux\nvar _ = 1\n"
	if err := os.WriteFile(filepath.Join(dir, "y.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	hits, err := NonDirectives(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %v, want none", hits)
	}
}

func TestIsDirective(t *testing.T) {
	for _, c := range []string{"//go:embed seed.jsonl", "//go:build ignore", "//nolint:staticcheck", "// +build linux"} {
		if !IsDirective(c) {
			t.Errorf("directive rejected: %q", c)
		}
	}
	for _, c := range []string{"// go:embed oops", "// note", "/* block */", "//go", "//nolintx"} {
		if IsDirective(c) {
			t.Errorf("narrative accepted: %q", c)
		}
	}
}
