// done_v0270_test.go — the session-knowledgization accrual contract.
// The distill-marker accrual check verifies equivalently by capturing
// the WriteTypedMarkerFn seam instead of the cli marker-storage
// implementation (ReadTypedMarker) (marker file IO belongs to the
// cli-residual compact_test on the real path).
package knowledge

import (
	"context"
	"strings"
	"testing"
	"time"
)

// captureTypedMarker — swaps the WriteTypedMarkerFn seam (call
// recording).
func captureTypedMarker(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	prev := WriteTypedMarkerFn
	WriteTypedMarkerFn = func(kind, sessionID, reason string) error {
		calls = append(calls, kind+"|"+sessionID+"|"+reason)
		return nil
	}
	t.Cleanup(func() { WriteTypedMarkerFn = prev })
	return &calls
}

func TestMaybeSessionKnowledge(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	markers := captureTypedMarker(t)
	st := knFake("")
	maybeSessionKnowledge(context.Background(), st, "검색 품질 개선 작업을 완료했다", "test-agent", "s1", 0)
	if len(st.inserted) != 1 {
		t.Fatalf("적립 = %d건, want 1", len(st.inserted))
	}
	got := st.inserted[0]
	if got["status"] != "pending" || got["confidence"] != 0.5 {
		t.Errorf("적립 상태 = %v/%v, want pending/0.5", got["status"], got["confidence"])
	}
	if !strings.Contains(got["topic"].(string), "session[") {
		t.Errorf("topic = %v", got["topic"])
	}
	// tags must be passed as []string{"process"} so it passes
	// normalizeTags and is stored in the normalized form ":process:"
	// (repairing the contract violation where passing the string
	// ":process:" was dropped to an empty string in the default branch).
	if tags, ok := got["tags"].(string); !ok || tags != ":process:" {
		t.Errorf("tags = %#v, want \":process:\"", got["tags"])
	}
	// Distill-marker accrual — the distill kind·session must match.
	found := false
	for _, c := range *markers {
		if strings.HasPrefix(c, "distill|s1|") {
			found = true
		}
	}
	if !found {
		t.Fatalf("증류 마커 미적립: %v", *markers)
	}
}

// TestMaybeSessionKnowledgeShortSummary — under the minimum length, no
// accrual.
func TestMaybeSessionKnowledgeShortSummary(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := knFake("")
	maybeSessionKnowledge(context.Background(), st, "짧다", "a", "s1", 0)
	if len(st.inserted) != 0 {
		t.Fatalf("짧은 요약 적립 = %d건, want 0", len(st.inserted))
	}
}

// TestMaybeSessionKnowledgeControlOnlyGate — the length gate is applied
// after sanitization — a control-characters-only summary sanitizes to an
// empty string and is not accrued.
func TestMaybeSessionKnowledgeControlOnlyGate(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := knFake("")
	maybeSessionKnowledge(context.Background(), st, "\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e", "a", "s1", 0)
	if len(st.inserted) != 0 {
		t.Fatalf("제어문자 요약 적립 = %d건, want 0(살균 후 게이트)", len(st.inserted))
	}
}

// TestMaybeSessionKnowledgeTruncates — the summary is truncated to 500
// runes (import-path symmetry) — the excess is not accrued.
func TestMaybeSessionKnowledgeTruncates(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := knFake("")
	long := strings.Repeat("가", 800)
	maybeSessionKnowledge(context.Background(), st, long, "a", "s1", 0)
	if len(st.inserted) != 1 {
		t.Fatalf("적립 = %d건, want 1", len(st.inserted))
	}
	if got := len([]rune(st.inserted[0]["summary"].(string))); got != 500 {
		t.Errorf("요약 길이 = %d룬, want 500", got)
	}
}

// TestMaybeSessionKnowledgeFrequencyCap — the frequency-cap contract —
// skip if the last accrual is within N days; accrue if N days have
// elapsed·the value is corrupt.
func TestMaybeSessionKnowledgeFrequencyCap(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	today := time.Now().UTC().Format(time.RFC3339)
	st := knFake(today)
	maybeSessionKnowledge(context.Background(), st, "오늘 이미 적립한 상태의 요약", "a", "s1", 0)
	if len(st.inserted) != 0 {
		t.Fatalf("당일 재적립 = %d건, want 0(빈도 상한)", len(st.inserted))
	}
	twoDaysAgo := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	st2 := knFake(twoDaysAgo)
	maybeSessionKnowledge(context.Background(), st2, "이틀 전 적립 이후의 새 요약", "a", "s1", 0)
	if len(st2.inserted) != 1 {
		t.Fatalf("경과 후 적립 = %d건, want 1", len(st2.inserted))
	}
	st3 := knFake("not-a-timestamp")
	maybeSessionKnowledge(context.Background(), st3, "파손된 메타 값 이후의 요약", "a", "s1", 0)
	if len(st3.inserted) != 1 {
		t.Fatalf("파손 값 적립 = %d건, want 1(fail-open 재시도)", len(st3.inserted))
	}
}

// TestMaybeSessionKnowledgeOffSentinel — session_knowledge_days -1
// (off) accrues nothing (0 is coerced to the default 1 by
// loadThresholds — i.e. unset).
func TestMaybeSessionKnowledgeOffSentinel(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key IN") {
			return [][]any{{"session_knowledge_days", "-1"}}, nil
		}
		return nil, nil
	}}
	maybeSessionKnowledge(context.Background(), st, "끔 상태에서는 적립되지 않는 요약", "a", "s1", 0)
	if len(st.inserted) != 0 {
		t.Fatalf("-1 끔 적립 = %d건, want 0", len(st.inserted))
	}
}

// TestMaybeSessionKnowledgeDedupSkip — an already-present
// content_hash is not re-accrued (blocking same-session re-exits).
func TestMaybeSessionKnowledgeDedupSkip(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	st.exists = true
	maybeSessionKnowledge(context.Background(), st, "이미 적립된 동일 내용의 요약", "a", "s1", 0)
	if len(st.inserted) != 0 {
		t.Fatalf("dedup 적립 = %d건, want 0", len(st.inserted))
	}
}

// TestDoneStdinSummaryActivatesKnowledge — the Stop hook stdin's
// compact_summary activates session knowledgization (another harness's
// fallback path).
func TestDoneStdinSummaryActivatesKnowledge(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	stubAutoBackup(t)
	st := knFake("")
	var out strings.Builder
	code := RunDone(context.Background(), st, []string{"--session", "claude"},
		strings.NewReader(`{"hook_event_name":"Stop","compact_summary":"검색 품질 개선 작업 — RRF 융합 구현 완료"}`), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	// Accrual — 1 pending knowledge row (only when Summary is filled).
	knowledge := 0
	for _, rec := range st.inserted {
		if rec["kind"] == "knowledge" {
			knowledge++
		}
	}
	if knowledge != 1 {
		t.Fatalf("세션 지식화 적립 = %d건, want 1(stdin 요약 활성)", knowledge)
	}
}

// staleFake — fakeStore + a fixed StaleSessionRuns return (inducing the
// stale branch).
type staleFake struct {
	*fakeStore
	runs int
}

func (s *staleFake) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	return s.runs, nil
}

// TestDoneStalePathAccruesKnowledge — on the stale path (the
// consecutive-unrecorded warning), a filled summary still
// knowledgizes — not trapped in an early return.
func TestDoneStalePathAccruesKnowledge(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	stubAutoBackup(t)
	st := &staleFake{fakeStore: knFake(""), runs: 99}
	var out strings.Builder
	code := RunDone(context.Background(), st, []string{"--session", "claude"},
		strings.NewReader(`{"compact_summary":"stale 상태에서도 보존돼야 하는 세션 요약"}`), &out)
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
	if knowledge != 1 {
		t.Fatalf("stale 경로 지식화 = %d건, want 1", knowledge)
	}
}

// TestDoneStdinSummaryBranches — invalid JSON·a missing field yield the
// empty value (the skip-fallback-boilerplate contract — a regression
// preventing the asymptomatic discard of valid summaries).
func TestDoneStdinSummaryBranches(t *testing.T) {
	if got := doneStdinSummary(strings.NewReader(`{bad json`)); got != "" {
		t.Errorf("invalid JSON = %q, want \"\"", got)
	}
	if got := doneStdinSummary(strings.NewReader(`{"hook_event_name":"Stop","stop_hook_active":false}`)); got != "" {
		t.Errorf("필드 부재 = %q, want \"\"", got)
	}
	if got := doneStdinSummary(strings.NewReader(`{"compact_summary":"  유효 요약  "}`)); got != "유효 요약" {
		t.Errorf("필드 존재 = %q, want 트림된 요약", got)
	}
	if got := doneStdinSummary(nil); got != "" {
		t.Errorf("nil stdin = %q, want \"\"", got)
	}
}

// TestDoneCommitsAuditTrail — the session exit attributes the session
// row·knowledgization·counter writes to an audit commit (preventing
// absorption into an unrelated next write's global staging).
func TestDoneCommitsAuditTrail(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	stubAutoBackup(t)
	st := knFake("")
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
