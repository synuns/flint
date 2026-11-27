package knowledge

import (
	"context"
	"strings"
	"testing"
)

type staleFake struct {
	*fakeStore
	runs int
}

func (s *staleFake) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	return s.runs, nil
}

func TestDoneStalePathNoKnowledgeDeposit(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	stubAutoBackup(t)
	st := &staleFake{fakeStore: &fakeStore{}, runs: 99}
	var out strings.Builder
	code := RunDone(context.Background(), st, []string{"--session", "claude"},
		strings.NewReader(`{"compact_summary":"stale 상태에서도 세션 행은 기록돼야 하는 합성 요약"}`), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "stale") {
		t.Fatalf("출력 = %s, want stale 경고", out.String())
	}
	knowledge := 0
	for _, rec := range st.inserted {
		if rec["kind"] == "knowledge" {
			knowledge++
		}
	}
	if knowledge != 0 {
		t.Fatalf("stale 경로 지식화 = %d건, want 0", knowledge)
	}
}

func TestDoneStdinBranches(t *testing.T) {
	if got := parseDoneStdin(strings.NewReader(`{bad json`)); got != (doneStdinPayload{}) {
		t.Errorf("invalid JSON = %#v, want zero", got)
	}
	if got := parseDoneStdin(strings.NewReader(`{"hook_event_name":"Stop","stop_hook_active":false}`)); got != (doneStdinPayload{}) {
		t.Errorf("필드 부재 = %#v, want zero", got)
	}
	if got := parseDoneStdin(strings.NewReader(`{"compact_summary":"  유효 요약  ","tasks":" [{\"task\":\"a\"}] "}`)); got.CompactSummary != "유효 요약" || got.Tasks != `[{"task":"a"}]` {
		t.Errorf("필드 존재 = %#v, want 트림된 값", got)
	}
	if got := parseDoneStdin(nil); got != (doneStdinPayload{}) {
		t.Errorf("nil stdin = %#v, want zero", got)
	}
}

func TestDoneCommitsAuditTrail(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	stubAutoBackup(t)
	st := &fakeStore{}
	var out strings.Builder
	if code := RunDone(context.Background(), st, []string{"--session", "claude"},
		strings.NewReader(`{"compact_summary":"감사 귀속 검증용 세션 요약 본문"}`), &out); code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	found := false
	for _, c := range st.commits {
		if strings.Contains(c, "session-end:") {
			found = true
		}
	}
	if !found {
		t.Errorf("세션 종료 감사 커밋 부재: %v", st.commits)
	}
}
