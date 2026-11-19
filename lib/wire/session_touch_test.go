// session_touch_test.go — the session-label adoption mtime-touch contract.
// So that the label of a session active beyond 14 days (default) is not deleted by
// another session's session-start cleanup (PruneSessionLabels), ReadSessionLabel
// refreshes the label file's mtime on adoption (a successful read).
package wire

import (
	"os"
	"testing"
	"time"
)

// TestSessionLabelAdoptionTouchesMtime — adopting a label with an old mtime
// refreshes that file's mtime, and it survives the default 14-day cleanup
// (PruneSessionLabels). An mtime update failure is silently ignored (a read-only
// FS — a code path; this test demonstrates only the success path).
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
	// an active session's label survives the default 14-day cleanup.
	PruneSessionLabels(14 * 24 * time.Hour)
	if _, err := os.Stat(f); err != nil {
		t.Fatalf("활성 라벨이 정리 대상(채택 mtime 터치 계약 위반): %v", err)
	}
}

// TestSessionLabelNoTouchOnMiss — the adoption-failure (empty label) path does
// not touch the mtime (the lookup-failure existing path unchanged — a stale empty
// label remains a cleanup target).
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
