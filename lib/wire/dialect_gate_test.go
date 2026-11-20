// dialect_gate_test.go — the dialect sealing gate (v1.8.0).
// Fails if a raw Doltgres dialect string appears in lib/**/*.go outside lib/db
// (test and non-test alike). lib/db is the sole owner of dialect SQL.
// Comment lines (// after trimming) and lib/db and docs are excluded.
package wire

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dialectPatterns — the forbidden patterns. Assembled from split literals so this
// file itself does not trip the gate (complete only at runtime).
// The check scope is limited to the history-query dialects (dolt_log, dolt_diff,
// dolt_commit_diff_knowledge, AS OF) — diagnostic and write dialects like
// dolt_status are out of scope (writes are already owned by lib/db).
func dialectPatterns() []string {
	return []string{
		"dolt_" + "log",
		"dolt_" + "diff",
		"dolt_" + "commit" + "_diff_" + "knowledge",
		"AS " + "OF '",
	}
}

// dialectViolation — one violation (relative path, 1-based line, the line itself).
type dialectViolation struct {
	Path string
	Line int
	Text string
}

// scanDialectViolations — line-scans the .go files under libRoot (relative to
// this .go package directory, "../" = lib) and collects violations. Everything under lib/db is skipped.
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

// TestDialectSealedOutsideDB — the gate body: even 1 violation fails with
// file:line and the offending line. Green means the dialect cut is complete.
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

// TestScanDialectViolationsClassifier — scanner classification accuracy (a synthetic
// tree): catches dialect strings on code lines (backtick SQL, quoted strings) while
// allowing comment lines and the lib/db subtree.
func TestScanDialectViolationsClassifier(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ops"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		// under lib/db — the dialect's sole-owner zone, all allowed.
		"db/history.go":    "q := `SELECT * FROM " + "dolt_" + "log`\n",
		"db/diff_store.go": "q := \"... " + "dolt_" + "diff ...\"\n",
		// comment lines (// prefix) are allowed.
		"ops/a.go": "// 주석: " + "dolt_" + "log 언급\nvar x = 1\n",
		// 3 code-line violation kinds (backtick SQL, quoted string, AS OF splicing).
		"ops/b.go": "q := `SELECT 1 FROM " + "dolt_" + "diff`\n",
		"ops/c.go": "s := \"active_knowledge " + "AS " + "OF 'h'\"\n",
		"ops/d.go": "q := `SELECT * FROM " + "dolt_" + "commit" + "_diff_" + "knowledge`\n",
		// non-.go files are ignored.
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
