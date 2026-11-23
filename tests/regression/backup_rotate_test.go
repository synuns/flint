package regression

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/synuns/flint/lib/backup"
)

func regDump(t *testing.T, dir, name string, mtime time.Time, withSide bool) {
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

func TestRotateKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	a := "flint-20261022-120000-101.sql"
	b := "flint-20261022-110000-102.sql"
	c := "flint-20261022-100000-103.sql"
	regDump(t, dir, a, base, true)
	regDump(t, dir, b, base.Add(-time.Hour), true)
	regDump(t, dir, c, base.Add(-2*time.Hour), true)

	removed, err := backup.Rotate(dir, 2)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 2 || removed[0] != c || removed[1] != c+".sha256" {
		t.Fatalf("removed=%v, want [%s %s.sha256]", removed, c, c)
	}
	for _, name := range []string{a, a + ".sha256", b, b + ".sha256"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("잔존해야 함: %s: %v", name, err)
		}
	}
}

func TestRotateMissingSidecarDeletesAlone(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	newName := "flint-20261022-120000-201.sql"
	oldName := "flint-20261022-110000-202.sql"
	regDump(t, dir, newName, base, true)
	regDump(t, dir, oldName, base.Add(-time.Hour), false)

	removed, err := backup.Rotate(dir, 1)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 1 || removed[0] != oldName {
		t.Fatalf("removed=%v, want [%s]", removed, oldName)
	}
	if _, err := os.Stat(filepath.Join(dir, oldName)); !os.IsNotExist(err) {
		t.Fatalf("%s 미삭제: %v", oldName, err)
	}
	if _, err := os.Stat(filepath.Join(dir, newName)); err != nil {
		t.Fatalf("%s 유지 실패: %v", newName, err)
	}
}

func TestRotateKeepZeroKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	newName := "flint-20261022-120000-301.sql"
	oldName := "flint-20261022-110000-302.sql"
	regDump(t, dir, newName, base, true)
	regDump(t, dir, oldName, base.Add(-time.Hour), true)

	removed, err := backup.Rotate(dir, 0)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed=%v, want [%s %s.sha256]", removed, oldName, oldName)
	}
	if _, err := os.Stat(filepath.Join(dir, newName)); err != nil {
		t.Fatalf("keep 0에도 최신 1쌍은 유지: %v", err)
	}
}

func TestRotatePreservesForeignFiles(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	mine := "mine.sql"
	manual := "manual-export.age"
	regDump(t, dir, mine, base.Add(-48*time.Hour), true)
	regDump(t, dir, manual, base.Add(-47*time.Hour), false)
	regDump(t, dir, "flint-20261022-120000-401.sql", base, true)
	regDump(t, dir, "flint-20261022-110000-402.sql", base.Add(-time.Hour), false)

	removed, err := backup.Rotate(dir, 1)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 1 || removed[0] != "flint-20261022-110000-402.sql" {
		t.Fatalf("removed=%v, want [flint-20261022-110000-402.sql] — 사용자 파일은 제외", removed)
	}
	for _, name := range []string{mine, mine + ".sha256", manual} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("사용자 파일 보존 실패: %s: %v", name, err)
		}
	}
}

func TestBackupKeepFallbacks(t *testing.T) {
	if got := backup.BackupKeep(func(string) (string, error) { return "3", nil }); got != 3 {
		t.Fatalf("meta 우선: %d, want 3", got)
	}
	for name, v := range map[string]string{"에러": "", "빈 값": "", "비정수": "many", "0": "0", "음수": "-1"} {
		get := func(string) (string, error) { return v, nil }
		if name == "에러" {
			get = func(string) (string, error) { return "", errors.New("db down") }
		}
		if got := backup.BackupKeep(get); got != backup.DefaultBackupKeep {
			t.Fatalf("%s: %d, want %d", name, got, backup.DefaultBackupKeep)
		}
	}
	if got := backup.BackupKeep(nil); got != backup.DefaultBackupKeep {
		t.Fatalf("nil getKV: %d, want %d", got, backup.DefaultBackupKeep)
	}
}
