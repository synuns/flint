// sessionlabel_v102_test.go — the hook/session-label cluster
// regressions: per-session label file separation (parallel races, expiry cleanup,
// ignoring legacy), moving invalid-label validation ahead of recording, silent invalid
// arguments at hook entry points, and hooks.json/doctor jq fallback consistency (empty — the same direction as treating invalid as absent).
package ops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// injectSessionLabelFn — cleans up hot's injection seams for standalone ops runs (the
// real wiring is the cli root init — the same convention as the cli sessionhook tests).
// SessionLabelFn points to the ops implementation; the other metering seams get minimal no-ops.
func injectSessionLabelFn(t *testing.T) {
	t.Helper()
	oldLabel := knowledge.SessionLabelFn
	oldProfile := knowledge.ExposureProfileFn
	oldRecLen := knowledge.RecordHotLenFn
	oldLoad := knowledge.LoadHotStatsFromMetaFn
	oldPersist := knowledge.PersistHotStatsToMetaFn
	knowledge.SessionLabelFn = SessionLabel
	knowledge.ExposureProfileFn = func(func(string) (string, error)) string { return "normal" }
	knowledge.RecordHotLenFn = func(int) {}
	knowledge.LoadHotStatsFromMetaFn = func(context.Context, wire.Store) {}
	knowledge.PersistHotStatsToMetaFn = func(context.Context, wire.Store) {}
	t.Cleanup(func() {
		knowledge.SessionLabelFn = oldLabel
		knowledge.ExposureProfileFn = oldProfile
		knowledge.RecordHotLenFn = oldRecLen
		knowledge.LoadHotStatsFromMetaFn = oldLoad
		knowledge.PersistHotStatsToMetaFn = oldPersist
	})
}

// labelRecorder — a fake recording the SessionStartRecorder seam (the ops counterpart
// of the cli sessionhook_v0200_test convention).
type labelRecorder struct {
	*fakeStore
	starts []string
}

func (s *labelRecorder) RecordSessionStart(ctx context.Context, session string) error {
	s.starts = append(s.starts, session)
	return nil
}

func quietLabelStore() *labelRecorder {
	return &labelRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}}
}

// TestSessionStartInvalidLabelSilent — a hook entry with an invalid label (a
// `--session --json` flag absorption, or a trailing --session) means (1) no exit 3 and no
// stdout error (non-interference §3 — SessionStart stdout is the injection path), (2) the
// invalid value is not recorded in the session boundary or the label file (treated as
// absent), and (3) no read auto-adoption — closing the pollution-revival path.
func TestSessionStartInvalidLabelSilent(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"flag 흡수", []string{"--session", "--json"}},
		{"trailing 플래그", []string{"--session"}},
		{"'-' 접두", []string{"--session", "-x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
			injectSessionLabelFn(t)
			st := quietLabelStore()
			var out strings.Builder
			code := RunSessionStart(context.Background(), st, tc.args, strings.NewReader(""), &out)
			if code != 0 {
				t.Fatalf("exit=%d, want 0(무효 라벨은 부재 취급); out=%s", code, out.String())
			}
			if strings.Contains(out.String(), `"error"`) {
				t.Errorf("훅 stdout에 오류 출력(비간섭 위반): %s", out.String())
			}
			if len(st.starts) != 1 || st.starts[0] != "" {
				t.Errorf("기록된 경계 = %v, want [\"\"](무효 값 미기록 — 부재와 동일)", st.starts)
			}
			if _, ok := wire.ReadSessionLabel(); ok {
				t.Error("무효 라벨이 파일에 잔존 — read 자동 채택 오염 부활")
			}
		})
	}
}

// TestSessionEndInvalidLabelSilent — session-end's invalid label is also treated as
// absent (no exit 3 or validation error; done's existing absent path) + parallel
// sessions' label files are preserved (closing the removal race).
func TestSessionEndInvalidLabelSilent(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteSessionLabel("sess-a"); err != nil {
		t.Fatalf("WriteSessionLabel: %v", err)
	}
	st := quietLabelStore()
	var out strings.Builder
	code := RunSessionEnd(context.Background(), st, []string{"--session", "-x"}, strings.NewReader(""), &out)
	// the done usage-error (absent path) exit updated 0->3 — the assertion of this test is
	// that an invalid label still takes the absent path, not a 'validation error'.
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "usage:") {
		t.Errorf("무효 라벨이 검증 에러가 아닌 부재 경로를 타야 함: %s", out.String())
	}
	if got, ok := wire.ReadSessionLabel(); !ok || got != "sess-a" {
		t.Fatalf("병렬 세션 라벨 = (%q %v), want sess-a(타 세션 종료가 지우지 않음)", got, ok)
	}
}

// TestSessionLabelParallelSeparation — the labels of 2 parallel sessions are separated
// into independent files (closing the overwrite race) + ending one session preserves the
// other's label (closing the removal race). Under the old single shared file, sess-b's
// write destroyed sess-a's, and session-end wiped them all.
func TestSessionLabelParallelSeparation(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteSessionLabel("sess-a"); err != nil {
		t.Fatalf("WriteSessionLabel(a): %v", err)
	}
	if err := WriteSessionLabel("sess-b"); err != nil {
		t.Fatalf("WriteSessionLabel(b): %v", err)
	}
	for _, sess := range []string{"sess-a", "sess-b"} {
		b, err := os.ReadFile(wire.SessionLabelFile(sess))
		if err != nil || string(b) != sess {
			t.Errorf("세션 %s 라벨 파일 = (%q, %v) — 병렬 덮어쓰기 파괴", sess, b, err)
		}
	}
	ClearSessionLabel("sess-b")
	if got, ok := wire.ReadSessionLabel(); !ok || got != "sess-a" {
		t.Fatalf("sess-b 종료 후 라벨 = (%q %v), want sess-a(분리 보존)", got, ok)
	}
}

// TestSessionLabelLegacyIgnored — the legacy session-current residual file is neither
// read nor settled (the user's disk — the no-deletion principle). Without a per-session
// file it is treated as absent; ClearSessionLabel removes only its own session's file.
func TestSessionLabelLegacyIgnored(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	legacy := filepath.Join(dir, "session-current")
	if err := os.WriteFile(legacy, []byte("old-label\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire.ReadSessionLabel(); ok {
		t.Fatal("잔존 session-current 폴백 읽기 — v1.1.0 제거 위반")
	}
	if err := WriteSessionLabel("new-sess"); err != nil {
		t.Fatalf("WriteSessionLabel: %v", err)
	}
	ClearSessionLabel("new-sess")
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("session-end가 잔존 session-current 청산(미삭제 원칙 위반): %v", err)
	}
	if _, ok := wire.ReadSessionLabel(); ok {
		t.Error("자기 세션 파일 제거 후 라벨 잔존")
	}
}

// TestSessionStartPrunesExpiredLabels — expiry cleanup: residual label files that never
// met a session-end are cleaned at the next session start (window =
// events_retention_days, default 14 days — aligned with the retention window of the read_events rows the label points at).
func TestSessionStartPrunesExpiredLabels(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteSessionLabel("stale-sess"); err != nil {
		t.Fatalf("WriteSessionLabel(stale): %v", err)
	}
	stale := wire.SessionLabelFile("stale-sess")
	old := time.Now().Add(-20 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	injectSessionLabelFn(t)
	st := quietLabelStore()
	var out strings.Builder
	if code := RunSessionStart(context.Background(), st,
		[]string{"--session", "fresh-sess"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("session-start exit=%d: %s", code, out.String())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("만료(20일) 라벨 파일 미정리")
	}
	if _, err := os.Stat(wire.SessionLabelFile("fresh-sess")); err != nil {
		t.Errorf("현행 라벨 파일이 정리 대상에 포함: %v", err)
	}
}

// TestHooksJsonSessionFallbackEmpty — the plugin-fallback hooks.json and the hooks
// doctor installs (flintHookDefs) use empty for the session-start/session-end jq
// fallback — when session_id is absent, no empty label is passed along (the same
// direction as the entry point treating invalid as absent; both sides use the same pattern).
func TestHooksJsonSessionFallbackEmpty(t *testing.T) {
	b, err := os.ReadFile("../../flint-plugin/hooks/hooks.json")
	if err != nil {
		t.Fatalf("hooks.json 읽기: %v", err)
	}
	for _, sub := range []string{"session-start", "session-end"} {
		if !strings.Contains(string(b), sub+" --session $(jq -r '.session_id // empty | @sh'") {
			t.Errorf("hooks.json %s jq 폴백이 empty 아님", sub)
		}
	}
	defs := flintHookDefs("/x/flint")
	for _, ev := range []string{"SessionStart", "Stop"} {
		for _, g := range defs[ev] {
			for _, h := range g["hooks"].([]any) {
				cmd := h.(map[string]any)["command"].(string)
				if !strings.Contains(cmd, ".session_id // empty | @sh") {
					t.Errorf("doctor %s 훅 jq 폴백이 empty 아님: %q", ev, cmd)
				}
			}
		}
	}
}
