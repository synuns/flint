package wire

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
