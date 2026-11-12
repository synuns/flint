// util_test.go — v0.41.0 T4 Review Focus: TestCellToTimeQuoteJSONSingle.
// CellToTime·QuoteJSON의 단일 구현(wire) 행위 + 전 repo 단일 소스 가드
// (지식·ops 소비자는 복제 func 정의 없이 별칭 위임만 허용).
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

// TestBackupEncryptOnSingle — v0.43.0 T3 Review Focus: 술어 수렴.
// wire.BackupEncryptOn 행위("true"·"1"만 참) + 전 repo 단일 소스 가드
// (func 정의는 wire/util.go 단일 분만 허용 — knowledge 로컬 복제·ops
// func 위임 금지, 소비자는 var 별칭 위임만).
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
				return nil // 단일 구현 소유분
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

// TestCellToTimeQuoteJSONNoLocalCopies — lib 전체(비테스트) 소스에서
// CellToTime·QuoteJSON의 func 정의는 wire/util.go 단일 분만 허용한다.
// 소비자(knowledge·ops)는 var 별칭 위임이어야 한다(복제 부채 재발 가드).
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
			return nil // 단일 구현 소유분
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
