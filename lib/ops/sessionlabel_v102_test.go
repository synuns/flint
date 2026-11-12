// sessionlabel_v102_test.go — v1.0.2 R2 수정 라운드(훅·세션 라벨 클러스터)
// 회귀: R2-A2 세션별 라벨 파일 분리(병렬 경합·만료 정리·레거시 폴백),
// R2-B1 무효 라벨 검증의 기록 앞 이동, R2-B2 훅 진입점 무효 인자 조용함,
// R2-C3 hooks.json·doctor jq 폴백 정합(empty — 무효=부재 취급과 동일 방향).
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

// injectSessionLabelFn — ops 단독 실행 시 hot의 주입 seam 정리(실배선은
// cli root init — cli sessionhook 테스트와 동일 관례). SessionLabelFn은
// ops 구현을, 나머지 계측 seam은 최소 no-op으로 연결한다.
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

// labelRecorder — SessionStartRecorder seam을 녹음하는 fake(cli
// sessionhook_v0200_test 관례의 ops 대응분).
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

// TestSessionStartInvalidLabelSilent — R2-B1·B2: 무효 라벨(`--session
// --json` 플래그 흡수·trailing --session) 훅 진입은 (1) exit 3·stdout
// 오류 없음(비간섭 §3 — SessionStart stdout은 주입 경로), (2) 무효 값이
// 세션 경계·라벨 파일에 기록되지 않음(부재 취급), (3) read 자동 채택
// 부재 — 오염 부활 경로 폐쇄.
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

// TestSessionEndInvalidLabelSilent — R2-B2·A2: session-end의 무효 라벨도
// 부재 취급(exit 3·검증 오류 없이 done의 기존 부재 경로) + 이 세션이
// 아닌 병렬 세션의 라벨 파일은 보존(제거 경합 폐쇄).
func TestSessionEndInvalidLabelSilent(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteSessionLabel("sess-a"); err != nil {
		t.Fatalf("WriteSessionLabel: %v", err)
	}
	st := quietLabelStore()
	var out strings.Builder
	code := RunSessionEnd(context.Background(), st, []string{"--session", "-x"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "usage:") {
		t.Errorf("무효 라벨이 검증 에러가 아닌 부재 경로를 타야 함: %s", out.String())
	}
	if got, ok := wire.ReadSessionLabel(); !ok || got != "sess-a" {
		t.Fatalf("병렬 세션 라벨 = (%q %v), want sess-a(타 세션 종료가 지우지 않음)", got, ok)
	}
}

// TestSessionLabelParallelSeparation — R2-A2: 병렬 2세션의 라벨이 독립
// 파일로 분리(덮어쓰기 경합 폐쇄) + 한 세션 종료가 다른 세션 라벨을
// 보존(제거 경합 폐쇄). 구 단일 공유 파일이라면 sess-b 기록이 sess-a를
// 파괴하고 session-end가 전부를 지웠다.
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

// TestSessionLabelLegacyFallback — R2-A2 업그레이드 경계: 구 session-current
// 단독 시 폴백 채택, 세션별 파일 등장 후에는 세션별이 우선, session-end가
// 구 파일까지 청산.
func TestSessionLabelLegacyFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "session-current"), []byte("old-label\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := wire.ReadSessionLabel(); !ok || got != "old-label" {
		t.Fatalf("레거시 폴백 = (%q %v), want old-label", got, ok)
	}
	if err := WriteSessionLabel("new-sess"); err != nil {
		t.Fatalf("WriteSessionLabel: %v", err)
	}
	if got, ok := wire.ReadSessionLabel(); !ok || got != "new-sess" {
		t.Fatalf("세션별 우선 = (%q %v), want new-sess", got, ok)
	}
	ClearSessionLabel("new-sess")
	if _, ok := wire.ReadSessionLabel(); ok {
		t.Error("session-end 후 라벨 잔존(레거시 포함 청산)")
	}
}

// TestSessionStartPrunesExpiredLabels — R2-A2 만료 정리: session-end를 못
// 만난 잔존 라벨 파일이 새 세션 시작 시 정리된다(창 = events_retention_days
// 기본 14일 — 라벨이 가리키는 read_events 행의 보존 창과 정렬).
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

// TestHooksJsonSessionFallbackEmpty — R2-C3: 플러그인 폴백 hooks.json과
// doctor가 설치하는 훅(flintHookDefs)의 session-start·session-end jq 폴백은
// empty — session_id 부재 시 빈 라벨을 흘려보내지 않는다(진입점의
// 무효=부재 취급과 동일 방향, 양측 동일 패턴).
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
