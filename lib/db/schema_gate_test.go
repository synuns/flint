package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func schemaPatterns() []string {
	return []string{
		"SELECT key, value FROM meta WHERE key IN ('" + "hot_stats" + "')",
		"SELECT id, length(summary) FROM " + "knowledge",
		"SELECT count(*), count(CASE WHEN zero THEN 1 END) FROM " + "read_events",
	}
}

type schemaViolation struct {
	Path string
	Line int
	Text string
}

func scanSchemaViolations(libRoot string) ([]schemaViolation, error) {
	patterns := schemaPatterns()
	var out []schemaViolation
	err := filepath.WalkDir(libRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(libRoot, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if rel == "db" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, p := range patterns {
				if strings.Contains(line, p) {
					out = append(out, schemaViolation{
						Path: filepath.ToSlash(rel), Line: i + 1, Text: trimmed,
					})
					break
				}
			}
		}
		return nil
	})
	return out, err
}

func TestSchemaSealedOutsideDB(t *testing.T) {
	violations, err := scanSchemaViolations("..")
	if err != nil {
		t.Fatalf("스키마 스캔 실패: %v", err)
	}
	if len(violations) == 0 {
		return
	}
	var b strings.Builder
	for _, v := range violations {
		fmt.Fprintf(&b, "  %s:%d: %s\n", v.Path, v.Line, v.Text)
	}
	t.Fatalf("lib/db 밖 대표 쿼리 원시 문자열 %d건 발견(스키마 쿼리는 lib/db 유일 소유):\n%s",
		len(violations), b.String())
}

func TestScanSchemaViolationsClassifier(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ops"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"db/a.go":       "q := `SELECT id, length(summary) FROM " + "knowledge`\n",
		"ops/a.go":      "// 주석: SELECT id, length(summary) FROM " + "knowledge 언급\nvar x = 1\n",
		"ops/b.go":      "q := `SELECT count(*), count(CASE WHEN zero THEN 1 END) FROM " + "read_events`\n",
		"ops/c.go":      "q := \"SELECT key, value FROM meta WHERE key IN ('" + "hot_stats" + "')\"\n",
		"ops/d.go":      "q := `SELECT bytes FROM " + "read_events WHERE bytes > 0`\n",
		"ops/notes.txt": "raw SELECT id, length(summary) FROM " + "knowledge text\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanSchemaViolations(root)
	if err != nil {
		t.Fatalf("스캔 실패: %v", err)
	}
	want := []schemaViolation{
		{Path: "ops/b.go", Line: 1},
		{Path: "ops/c.go", Line: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("위반 %d건, want %d건: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Path != w.Path || got[i].Line != w.Line {
			t.Errorf("위반[%d]=%s:%d, want %s:%d",
				i, got[i].Path, got[i].Line, w.Path, w.Line)
		}
	}
}
