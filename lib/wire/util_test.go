// The single-implementation (wire) behavior of CellToTime and QuoteJSON + a
// whole-repo single-source guard (knowledge/ops consumers may only alias-delegate, with no duplicated func definitions).
package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCellToTimeQuoteJSONSingle(t *testing.T) {
	t.Run("CellToTime time.Time 그대로", func(t *testing.T) {
		now := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
		got, ok := CellToTime(now)
		if !ok || !got.Equal(now) {
			t.Fatalf("time.Time 입력 = (%v, %v), want (%v, true)", got, ok, now)
		}
	})
	t.Run("CellToTime RFC3339 문자열", func(t *testing.T) {
		got, ok := CellToTime("2026-11-13T09:30:00Z")
		if !ok {
			t.Fatal("RFC3339 문자열 매핑 실패")
		}
		if want := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC); !got.Equal(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
	t.Run("CellToTime 비문자열·불량 문자열 false", func(t *testing.T) {
		for _, v := range []any{42, nil, true, "not-a-time", ""} {
			if _, ok := CellToTime(v); ok {
				t.Fatalf("CellToTime(%v) = true, want false", v)
			}
		}
	})
	t.Run("QuoteJSON 유효 JSON 리터럴", func(t *testing.T) {
		for _, s := range []string{`plain`, `with "quote"`, "new\nline", `한글 경로`, ""} {
			got := QuoteJSON(s)
			var back string
			if err := json.Unmarshal([]byte(got), &back); err != nil {
				t.Fatalf("QuoteJSON(%q) = %q: 언마샬 실패: %v", s, got, err)
			}
			if back != s {
				t.Fatalf("QuoteJSON(%q) 왕복 = %q", s, back)
			}
		}
	})
}

// TestBackupEncryptOnSingle — predicate convergence.
// wire.BackupEncryptOn's behavior (true only for "true" and "1") + a whole-repo
// single-source guard (only the single func definition in wire/util.go is allowed —
// no knowledge-local copies or ops func delegations; consumers may only delegate via var aliases).
func TestBackupEncryptOnSingle(t *testing.T) {
	t.Run("행위 — true·1만 참", func(t *testing.T) {
		for _, v := range []string{"true", "1"} {
			if !BackupEncryptOn(v) {
				t.Errorf("BackupEncryptOn(%q) = false, want true", v)
			}
		}
		for _, v := range []string{"false", "0", "", "TRUE", "yes", " true"} {
			if BackupEncryptOn(v) {
				t.Errorf("BackupEncryptOn(%q) = true, want false", v)
			}
		}
	})
	t.Run("로컬 정의 잔여 0", func(t *testing.T) {
		names := []string{"BackupEncryptOn", "backupEncryptOn"}
		root := filepath.Join("..", "..", "lib")
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if filepath.Base(path) == "util.go" && filepath.Base(filepath.Dir(path)) == "wire" {
				return nil // the single-implementation owner
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, n := range names {
				if strings.Contains(string(b), "func "+n+"(") {
					t.Errorf("%s: func %s 로컬 정의 잔여 — wire 별칭 위임이어야 함", path, n)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("소스 스캔 실패: %v", err)
		}
	})
}

// TestCellToTimeQuoteJSONNoLocalCopies — across all lib (non-test) sources, the
// func definitions of CellToTime and QuoteJSON are allowed only in the single wire/util.go
// spot. Consumers (knowledge, ops) must delegate via var aliases (a guard against duplicated-debt recurrence).
func TestCellToTimeQuoteJSONNoLocalCopies(t *testing.T) {
	names := []string{"CellToTime", "cellToTime", "QuoteJSON", "quoteJSON"}
	root := filepath.Join("..", "..", "lib")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Base(path) == "util.go" && filepath.Base(filepath.Dir(path)) == "wire" {
			return nil // the single-implementation owner
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, n := range names {
			if strings.Contains(string(b), "func "+n+"(") {
				t.Errorf("%s: func %s 로컬 정의 잔여 — wire 별칭 위임이어야 함", path, n)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("소스 스캔 실패: %v", err)
	}
}
