package backup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// T54 backup 로테이션 — .sql mtime 최신순 keep-N 유지, 나머지+짝이루는
// .sha256 삭제. 계획 지정 테스트: 3쌍→keep 2 → 1쌍 삭제·2쌍 잔존.

// writeDump — dir에 name(.sql)과 선택적 .sha256 사이드카를 mtime으로 생성.
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

// 계획 지정: 3쌍 → keep 2 → 1쌍 삭제(최신 2쌍 유지).
// R17-B1 이후 파일명은 flintDumpName 패턴(flint- 생성 파일)을 사용.
func TestRotateKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	a := "flint-20261022-120000-101.sql" // 최신
	b := "flint-20261022-110000-102.sql" // 중간
	c := "flint-20261022-100000-103.sql" // 가장 오래됨
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

// Review Focus(file): 사이드카 누락 파일은 .sql 단독 삭제(removed에도 단독).
func TestRotateMissingSidecarDeletesAlone(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	newName := "flint-20261022-120000-201.sql"
	oldName := "flint-20261022-110000-202.sql"
	writeDump(t, dir, newName, base, true)
	writeDump(t, dir, oldName, base.Add(-time.Hour), false) // 사이드카 없음

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

// Review Focus(numeric): keep≤0은 전체 삭제 아님 — 최소 1 보장.
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

	// 음수도 동일 — 최소 1.
	writeDump(t, dir, oldName, base.Add(-time.Hour), true)
	if removed, err = Rotate(dir, -3); err != nil || len(removed) != 2 {
		t.Fatalf("keep=-3: removed=%v err=%v, want 2항목 잔존 1쌍", removed, err)
	}
	mustExist(t, filepath.Join(dir, newName))
}

// keep 이하 — no-op, removed 빈 목록.
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

// .sql 외 파일(.sha256 단독 orphan 등)은 건드리지 않는다.
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

// 디렉터리 부재 — 에러.
func TestRotateMissingDir(t *testing.T) {
	if _, err := Rotate(filepath.Join(t.TempDir(), "absent"), 3); err == nil {
		t.Fatal("존재하지 않는 dir은 에러")
	}
}

// BackupKeep — meta backup_keep 우선, 무효·에러·미설정 시 기본 14.
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
