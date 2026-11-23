package wire

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dialectPatterns() []string {
	return []string{
		"dolt_" + "log",
		"dolt_" + "diff",
		"dolt_" + "commit" + "_diff_" + "knowledge",
		"AS " + "OF '",
	}
}

type dialectViolation struct {
	Path string
	Line int
	Text string
}

func scanDialectViolations(libRoot string) ([]dialectViolation, error) {
	patterns := dialectPatterns()
	var out []dialectViolation
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
					out = append(out, dialectViolation{
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

func TestDialectSealedOutsideDB(t *testing.T) {
	violations, err := scanDialectViolations("..")
	if err != nil {
		t.Fatalf("방언 스캔 실패: %v", err)
	}
	if len(violations) == 0 {
		return
	}
	var b strings.Builder
	for _, v := range violations {
		fmt.Fprintf(&b, "  %s:%d: %s\n", v.Path, v.Line, v.Text)
	}
	t.Fatalf("lib/db 밖 방언 원시 문자열 %d건 발견(방언은 lib/db 유일 소유):\n%s",
		len(violations), b.String())
}

func TestScanDialectViolationsClassifier(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ops"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{

		"db/history.go":    "q := `SELECT * FROM " + "dolt_" + "log`\n",
		"db/diff_store.go": "q := \"... " + "dolt_" + "diff ...\"\n",

		"ops/a.go": "// 주석: " + "dolt_" + "log 언급\nvar x = 1\n",

		"ops/b.go": "q := `SELECT 1 FROM " + "dolt_" + "diff`\n",
		"ops/c.go": "s := \"active_knowledge " + "AS " + "OF 'h'\"\n",
		"ops/d.go": "q := `SELECT * FROM " + "dolt_" + "commit" + "_diff_" + "knowledge`\n",

		"ops/notes.txt": "raw " + "dolt_" + "log text\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanDialectViolations(root)
	if err != nil {
		t.Fatalf("스캔 실패: %v", err)
	}
	want := []dialectViolation{
		{Path: "ops/b.go", Line: 1},
		{Path: "ops/c.go", Line: 1},
		{Path: "ops/d.go", Line: 1},
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
