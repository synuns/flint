// session_test.go — v1.1.0 T2(§2): 세션 라벨 읽기 계약. 구 단일 공유
// 파일(session-current) 폴백은 제거되었다 — 잔존 파일은 읽지도·지우지도
// 않는다(사용자 디스크 — 미삭제 원칙).
package wire

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestReadSessionLabel — 부재·빈 내용 ok=false, 1줄 라벨 ok=true, 다중
// 세션 파일은 mtime 최신 채택(marker.go 이동분 계약 승계). ConfigDir
// seam 주입으로 격리.
func TestReadSessionLabel(t *testing.T) {
	saved := ConfigDir
	dir := t.TempDir()
	ConfigDir = func() string { return dir }
	t.Cleanup(func() { ConfigDir = saved })

	if _, ok := ReadSessionLabel(); ok {
		t.Fatal("라벨 파일 부재 시 ok=false 여야")
	}
	if err := os.WriteFile(SessionLabelFile("sess-1"), []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadSessionLabel(); ok {
		t.Fatal("공백 라벨 ok=false 여야")
	}
	if err := os.WriteFile(SessionLabelFile("sess-1"), []byte("label-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := ReadSessionLabel(); !ok || got != "label-1" {
		t.Fatalf("라벨 읽기: got=%q ok=%v", got, ok)
	}
	// 다중 세션 — mtime 최신 파일 채택(last-writer-wins 승계).
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(SessionLabelFile("sess-1"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SessionLabelFile("sess-2"), []byte("label-2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := ReadSessionLabel(); !ok || got != "label-2" {
		t.Fatalf("최신 세션 라벨: got=%q ok=%v", got, ok)
	}
}

// TestSessionLabelLegacyIgnored — v1.1.0 T2 Review Focus: 구 session-current
// 잔존 파일은 무시 — 폴백 읽기 없음(라벨 부재), PruneSessionLabels 청산
// 대상도 아니다(사용자 디스크 파일 미삭제).
func TestSessionLabelLegacyIgnored(t *testing.T) {
	saved := ConfigDir
	dir := t.TempDir()
	ConfigDir = func() string { return dir }
	t.Cleanup(func() { ConfigDir = saved })

	legacy := filepath.Join(dir, "session-current")
	if err := os.WriteFile(legacy, []byte("old-label\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadSessionLabel(); ok {
		t.Fatal("잔존 session-current 폴백 읽기 — v1.1.0 제거 위반")
	}
	PruneSessionLabels(24 * time.Hour)
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("잔존 session-current가 Prune 대상(미삭제 원칙 위반): %v", err)
	}
}
