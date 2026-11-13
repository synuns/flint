// session_touch_test.go — v1.2.3 T9(A4): 세션 라벨 채택 mtime 터치 계약.
// 14일(기본)을 초과해 활성인 세션의 라벨이 타 세션 session-start 정리
// (PruneSessionLabels)에서 삭제되지 않게, ReadSessionLabel 채택(읽기
// 성공) 시 해당 라벨 파일의 mtime을 갱신한다.
package wire

import (
	"os"
	"testing"
	"time"
)

// TestSessionLabelAdoptionTouchesMtime — 과거 mtime 라벨 채택 시 그
// 파일 mtime이 갱신되고, 기본 14일 정리(PruneSessionLabels)에서
// 살아남는다. mtime 갱신 실패는 조용히 무시(읽기전용 FS — 코드 경로,
// 본 테스트는 성공 경로만 실증).
func TestSessionLabelAdoptionTouchesMtime(t *testing.T) {
	saved := ConfigDir
	dir := t.TempDir()
	ConfigDir = func() string { return dir }
	t.Cleanup(func() { ConfigDir = saved })

	f := SessionLabelFile("t9-deep")
	if err := os.WriteFile(f, []byte("t9-demo-label\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-20 * 24 * time.Hour)
	if err := os.Chtimes(f, old, old); err != nil {
		t.Fatal(err)
	}
	if got, ok := ReadSessionLabel(); !ok || got != "t9-demo-label" {
		t.Fatalf("라벨 채택: got=%q ok=%v", got, ok)
	}
	fi, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().After(old.Add(19 * 24 * time.Hour)) {
		t.Fatalf("채택 후 mtime 미갱신: %v(과거 채택 시각 그대로)", fi.ModTime())
	}
	// 활성 세션 라벨은 기본 14일 정리에서 생존.
	PruneSessionLabels(14 * 24 * time.Hour)
	if _, err := os.Stat(f); err != nil {
		t.Fatalf("활성 라벨이 정리 대상(채택 mtime 터치 계약 위반): %v", err)
	}
}

// TestSessionLabelNoTouchOnMiss — 채택 실패(빈 라벨) 경로는 mtime을
// 건드리지 않는다(조회 실패 기존 경로 불변 — 스테일 빈 라벨은 정리 대상
// 그대로).
func TestSessionLabelNoTouchOnMiss(t *testing.T) {
	saved := ConfigDir
	dir := t.TempDir()
	ConfigDir = func() string { return dir }
	t.Cleanup(func() { ConfigDir = saved })

	f := SessionLabelFile("t9-deep-empty")
	if err := os.WriteFile(f, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-20 * 24 * time.Hour)
	if err := os.Chtimes(f, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadSessionLabel(); ok {
		t.Fatal("빈 라벨 ok=false 여야")
	}
	fi, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Fatalf("채택 실패 경로에서 mtime 갱신됨: %v, want %v", fi.ModTime(), old)
	}
}
