package cli

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flint/lib/backup"
)

func TestLazyDumpWritesSidecar(t *testing.T) {
	dir := t.TempDir()
	lf := &lazyDumpFile{dir: dir}
	if _, err := lf.Write([]byte("dump body")); err != nil {
		t.Fatal(err)
	}
	if err := lf.close(); err != nil && !strings.Contains(err.Error(), "sidecar") {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.sql"))
	if len(matches) != 1 {
		t.Fatalf("덤프 파일 수 = %d", len(matches))
	}
	if _, err := backup.VerifyFile(matches[0]); err != nil {
		t.Errorf("사이드카 검증 실패: %v", err)
	}
}

func TestLazyDumpNoopNoSidecar(t *testing.T) {
	dir := t.TempDir()
	lf := &lazyDumpFile{dir: dir}
	if err := lf.close(); err != nil {
		t.Fatalf("no-op close 오류: %v", err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*")); len(m) != 0 {
		t.Errorf("no-op 잔존 파일: %v", m)
	}
}

// 덤프 close 실패 시 사이드카를 남기지 않는다(불완전 파일을 검증
// 통과로 위장하지 않는다) — 이중 close로 실패 경로를 유도한다.
func TestLazyDumpCloseFailNoSidecar(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "flint-closefail.sql")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("partial"); err != nil {
		t.Fatal(err)
	}
	lf := &lazyDumpFile{f: f, h: sha256.New()}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lf.close(); err == nil {
		t.Fatal("이미 닫힌 파일 close = nil, want 오류")
	}
	if _, err := os.Stat(name + ".sha256"); !os.IsNotExist(err) {
		t.Errorf("close 실패 시 사이드카 미생성 기대: %v", err)
	}
}
