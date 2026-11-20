package backup

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// backup rotation — keep the newest N .sql by mtime, delete the rest plus
// their paired .sha256. Plan-specified test: 3 pairs -> keep 2 -> 1 pair
// deleted, 2 pairs remain.

// writeDump — creates name (.sql) in dir with an optional .sha256 sidecar at the given mtime.
func writeDump(t *testing.T, dir, name string, mtime time.Time, withSide bool) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("-- "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if withSide {
		side := path + ".sha256"
		if err := os.WriteFile(side, []byte("0000  "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(side, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("존재해야 함: %s: %v", path, err)
	}
}

func mustGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("삭제되어야 함: %s (err=%v)", path, err)
	}
}

// Plan-specified: 3 pairs -> keep 2 -> 1 pair deleted (the newest 2 kept).
// Filenames use the flintDumpName pattern (flint-generated files).
func TestRotateKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	a := "flint-20261022-120000-101.sql" // newest
	b := "flint-20261022-110000-102.sql" // middle
	c := "flint-20261022-100000-103.sql" // oldest
	writeDump(t, dir, a, base, true)
	writeDump(t, dir, b, base.Add(-time.Hour), true)
	writeDump(t, dir, c, base.Add(-2*time.Hour), true)

	removed, err := Rotate(dir, 2)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 2 || removed[0] != c || removed[1] != c+".sha256" {
		t.Fatalf("removed=%v, want [%s %s.sha256]", removed, c, c)
	}
	mustExist(t, filepath.Join(dir, a))
	mustExist(t, filepath.Join(dir, a+".sha256"))
	mustExist(t, filepath.Join(dir, b))
	mustExist(t, filepath.Join(dir, b+".sha256"))
	mustGone(t, filepath.Join(dir, c))
	mustGone(t, filepath.Join(dir, c+".sha256"))
}

// A file missing its sidecar is deleted as the .sql alone (alone in removed too).
func TestRotateMissingSidecarDeletesAlone(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	newName := "flint-20261022-120000-201.sql"
	oldName := "flint-20261022-110000-202.sql"
	writeDump(t, dir, newName, base, true)
	writeDump(t, dir, oldName, base.Add(-time.Hour), false) // no sidecar

	removed, err := Rotate(dir, 1)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 1 || removed[0] != oldName {
		t.Fatalf("removed=%v, want [%s] (사이드카 없으므로 단독)", removed, oldName)
	}
	mustGone(t, filepath.Join(dir, oldName))
	mustExist(t, filepath.Join(dir, newName))
	mustExist(t, filepath.Join(dir, newName+".sha256"))
}

// keep <= 0 does not delete everything — a minimum of 1 is guaranteed.
func TestRotateKeepZeroKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	newName := "flint-20261022-120000-301.sql"
	oldName := "flint-20261022-110000-302.sql"
	writeDump(t, dir, newName, base, true)
	writeDump(t, dir, oldName, base.Add(-time.Hour), true)

	removed, err := Rotate(dir, 0)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 2 || removed[0] != oldName || removed[1] != oldName+".sha256" {
		t.Fatalf("removed=%v, want [%s %s.sha256] (최신 1쌍은 잔존)", removed, oldName, oldName)
	}
	mustExist(t, filepath.Join(dir, newName))
	mustExist(t, filepath.Join(dir, newName+".sha256"))

	// Negative likewise — minimum 1.
	writeDump(t, dir, oldName, base.Add(-time.Hour), true)
	if removed, err = Rotate(dir, -3); err != nil || len(removed) != 2 {
		t.Fatalf("keep=-3: removed=%v err=%v, want 2항목 잔존 1쌍", removed, err)
	}
	mustExist(t, filepath.Join(dir, newName))
}

// At or below keep — no-op, removed empty.
func TestRotateNoopWhenUnderKeep(t *testing.T) {
	dir := t.TempDir()
	writeDump(t, dir, "flint-20261022-120000-401.sql", time.Now(), true)

	removed, err := Rotate(dir, 14)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed=%v, want 빈 목록", removed)
	}
	mustExist(t, filepath.Join(dir, "flint-20261022-120000-401.sql"))
}

// Non-.sql files (e.g. an orphan .sha256 alone) are left untouched.
func TestRotateIgnoresNonSql(t *testing.T) {
	dir := t.TempDir()
	base := time.Now()
	writeDump(t, dir, "flint-20261022-120000-501.sql", base, true)
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := Rotate(dir, 0)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed=%v, want 빈 목록(.sql만 1개라 최소 1 유지)", removed)
	}
	mustExist(t, other)
}

// Missing directory — error.
func TestRotateMissingDir(t *testing.T) {
	if _, err := Rotate(filepath.Join(t.TempDir(), "absent"), 3); err == nil {
		t.Fatal("존재하지 않는 dir은 에러")
	}
}

// BackupKeep — meta backup_keep first; invalid, error, or unset falls back to 14.
func TestBackupKeep(t *testing.T) {
	cases := []struct {
		name  string
		getKV func(string) (string, error)
		want  int
	}{
		{"meta 우선", func(string) (string, error) { return "3", nil }, 3},
		{"에러 시 기본값", func(string) (string, error) { return "", errors.New("db down") }, 14},
		{"빈 값 기본값", func(string) (string, error) { return "", nil }, 14},
		{"비정수 기본값", func(string) (string, error) { return "many", nil }, 14},
		{"음수 기본값", func(string) (string, error) { return "-1", nil }, 14},
		{"0 기본값", func(string) (string, error) { return "0", nil }, 14},
		{"nil getKV 기본값", nil, 14},
	}
	for _, tc := range cases {
		if got := BackupKeep(tc.getKV); got != tc.want {
			t.Fatalf("%s: BackupKeep=%d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestBackupKeepQuietUnset — an unset backup_keep (blank value or lookup
// error) falls back to the normal default quietly — the every-run warning
// on fresh DBs was removed. The warning for a set-but-invalid value stays.
func TestBackupKeepQuietUnset(t *testing.T) {
	capture := func(t *testing.T, getKV func(string) (string, error)) (int, string) {
		t.Helper()
		var buf bytes.Buffer
		saved := log.Writer()
		log.SetOutput(&buf)
		t.Cleanup(func() { log.SetOutput(saved) })
		keep := BackupKeep(getKV)
		return keep, buf.String()
	}
	t.Run("미설정(빈 값) — 조용히 기본값", func(t *testing.T) {
		keep, logged := capture(t, func(string) (string, error) { return "", nil })
		if keep != DefaultBackupKeep {
			t.Fatalf("BackupKeep=%d, want %d", keep, DefaultBackupKeep)
		}
		if strings.TrimSpace(logged) != "" {
			t.Errorf("미설정 경고 출력(fresh DB 노이즈): %q", logged)
		}
	})
	t.Run("조회 에러 — 조용히 기본값", func(t *testing.T) {
		keep, logged := capture(t, func(string) (string, error) { return "", errors.New("meta backup_keep not set") })
		if keep != DefaultBackupKeep {
			t.Fatalf("BackupKeep=%d, want %d", keep, DefaultBackupKeep)
		}
		if strings.TrimSpace(logged) != "" {
			t.Errorf("미설정 경고 출력: %q", logged)
		}
	})
	t.Run("무효값 — 경고 유지", func(t *testing.T) {
		keep, logged := capture(t, func(string) (string, error) { return "many", nil })
		if keep != DefaultBackupKeep {
			t.Fatalf("BackupKeep=%d, want %d", keep, DefaultBackupKeep)
		}
		if !strings.Contains(logged, "invalid") {
			t.Errorf("무효값 경고 미출력: %q", logged)
		}
	})
}
