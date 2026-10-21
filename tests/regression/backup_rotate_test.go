// backup_rotate — T54 backup 로테이션 회귀(코디네이터 룰링: lib/cli 소유
// 블록 동결로 tests/regression/에 배치). lib/backup 공개 계약 수준에서
// Review Focus 2건(file: 사이드카 누락 단독 삭제, numeric: keep≤0 최소 1)을
// 포함해 재검증한다. 패키지 내부 상세 케이스는 lib/backup/rotate_test.go.
package regression

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"flint/lib/backup"
)

// regDump — dir에 name(.sql)+선택 사이드카를 mtime으로 생성.
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

// 계획 지정 케이스 — 3쌍 → keep 2 → 최신 2쌍 유지·1쌍 삭제.
func TestRotateKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	regDump(t, dir, "a.sql", base, true)
	regDump(t, dir, "b.sql", base.Add(-time.Hour), true)
	regDump(t, dir, "c.sql", base.Add(-2*time.Hour), true)

	removed, err := backup.Rotate(dir, 2)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 2 || removed[0] != "c.sql" || removed[1] != "c.sql.sha256" {
		t.Fatalf("removed=%v, want [c.sql c.sql.sha256]", removed)
	}
	for _, name := range []string{"a.sql", "a.sql.sha256", "b.sql", "b.sql.sha256"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("잔존해야 함: %s: %v", name, err)
		}
	}
}

// Review Focus(file) — 사이드카 누락 파일은 .sql 단독 삭제.
func TestRotateMissingSidecarDeletesAlone(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	regDump(t, dir, "new.sql", base, true)
	regDump(t, dir, "old.sql", base.Add(-time.Hour), false)

	removed, err := backup.Rotate(dir, 1)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 1 || removed[0] != "old.sql" {
		t.Fatalf("removed=%v, want [old.sql]", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.sql")); !os.IsNotExist(err) {
		t.Fatalf("old.sql 미삭제: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.sql")); err != nil {
		t.Fatalf("new.sql 유지 실패: %v", err)
	}
}

// Review Focus(numeric) — keep≤0은 전체 삭제 아님, 최소 1 보장.
func TestRotateKeepZeroKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	regDump(t, dir, "new.sql", base, true)
	regDump(t, dir, "old.sql", base.Add(-time.Hour), true)

	removed, err := backup.Rotate(dir, 0)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed=%v, want [old.sql old.sql.sha256]", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.sql")); err != nil {
		t.Fatalf("keep 0에도 최신 1쌍은 유지: %v", err)
	}
}

// BackupKeep — meta 우선·무효 시 14 폴백.
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
