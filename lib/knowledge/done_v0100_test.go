// done_v0100_test.go — lazyDumpFile(자동 백업 지연 writer) 계약.
// v0.39.0 T4: lib/cli에서 이동(cli/done_v0100_test.go 원본 + cli/
// backup_integrity_test.go의 TestLazyDumpPartialWriteDiscards).
package knowledge

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/backup"
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

// TestLazyDumpPartialWriteDiscards — v0.25.4(U2): 본문 기록 실패 시
// 부분 파일이 정상 사이드카를 받지 않고 폐기된다(체크섬 통과 오탐
// 백업 슬롯 방어).
func TestLazyDumpPartialWriteDiscards(t *testing.T) {
	dir := t.TempDir()
	lf := &lazyDumpFile{dir: dir}
	if _, err := lf.Write([]byte("BEGIN;\n")); err != nil {
		t.Fatal(err)
	}
	// 파일을 닫아버려 후속 Write 실패 유도 → failed 마킹.
	lf.f.Close()
	if _, err := lf.Write([]byte("more")); err == nil {
		t.Fatal("닫힌 파일 Write 성공 — 픽스처 결함")
	}
	if err := lf.close(); err == nil {
		t.Fatal("failed 상태 close가 에러 아님")
	}
	// 부분 파일 폐기 확인 — dir에 .sql/.sha256 잔존 없음.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".sql") || strings.HasSuffix(e.Name(), ".sha256") {
			t.Errorf("부분 파일 잔존: %s", e.Name())
		}
	}
}
