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

func TestSessionEndInvalidLabelSilent(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteSessionLabel("sess-a"); err != nil {
		t.Fatalf("WriteSessionLabel: %v", err)
	}
	st := quietLabelStore()
	var out strings.Builder
	code := RunSessionEnd(context.Background(), st, []string{"--session", "-x"}, strings.NewReader(""), &out)

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
