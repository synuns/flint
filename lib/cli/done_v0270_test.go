// done_v0270_test.go — v0.27.0 R1: 세션 지식화 적립 계약.
// v0.27.2: 소스 문자열 시그니처 + 빈도 상한·-1 센티널·dedup·tags·
// stale 경로·PostCompact 경로 계약 추가.
package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

// knFake — 빈도 상한·meta 경로 stub(queryFn이 session_knowledge_last
// 조회에 값을 반환 — 키는 $1 파라미터로 전달되므로 args도 검사).
// 나머지는 기본 fake(nil = 키 부재 → 빈도 통과).
func knFake(last string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		for _, a := range args {
			if s, ok := a.(string); ok && s == "session_knowledge_last" && last != "" {
				return [][]any{{last}}, nil
			}
		}
		return nil, nil
	}}
}

func TestMaybeSessionKnowledge(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := knFake("")
	maybeSessionKnowledge(context.Background(), st, "검색 품질 개선 작업을 완료했다", "test-agent", "s1", 0)
	if len(st.inserted) != 1 {
		t.Fatalf("적립 = %d건, want 1", len(st.inserted))
	}
	got := st.inserted[0]
	if got["status"] != "pending" || got["confidence"] != 0.5 {
		t.Errorf("적립 상태 = %v/%v, want pending/0.5", got["status"], got["confidence"])
	}
	if !strings.Contains(got["topic"].(string), "세션[") {
		t.Errorf("topic = %v", got["topic"])
	}
	// v0.27.2: tags는 []string{"process"}로 전달돼야 normalizeTags를
	// 통과해 정규형 ":process:"로 저장된다(문자열 ":process:" 전달 시
	// default 분기에서 빈 문자열로 소거되던 계약 위반 수선).
	if tags, ok := got["tags"].(string); !ok || tags != ":process:" {
		t.Errorf("tags = %#v, want \":process:\"", got["tags"])
	}
	// 증류 마커 적립(R2 회로 연결).
	if _, err := ReadTypedMarker(markerKindDistill); err != nil {
		t.Fatalf("증류 마커 미적립: %v", err)
	}
}

// TestMaybeSessionKnowledgeShortSummary — 최소 길이 미달은 미적립.
func TestMaybeSessionKnowledgeShortSummary(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := knFake("")
	maybeSessionKnowledge(context.Background(), st, "짧다", "a", "s1", 0)
	if len(st.inserted) != 0 {
		t.Fatalf("짧은 요약 적립 = %d건, want 0", len(st.inserted))
	}
}

// TestMaybeSessionKnowledgeControlOnlyGate — v0.27.2: 길이 게이트는 살균
// 후 기준 — 제어문자뿐인 요약은 sanitize 결과 빈 문자열로 미적립.
func TestMaybeSessionKnowledgeControlOnlyGate(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := knFake("")
	maybeSessionKnowledge(context.Background(), st, "\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e", "a", "s1", 0)
	if len(st.inserted) != 0 {
		t.Fatalf("제어문자 요약 적립 = %d건, want 0(살균 후 게이트)", len(st.inserted))
	}
}

// TestMaybeSessionKnowledgeTruncates — v0.27.2: 요약 500룬 절단(import
// 경로 대칭) — 초과분은 적립되지 않는다.
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

// TestMaybeSessionKnowledgeFrequencyCap — v0.27.2: 빈도 상한 계약 —
// 마지막 적립이 N일 이내면 skip, N일 경과·파손 값이면 적립.
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

// TestMaybeSessionKnowledgeOffSentinel — v0.27.2: session_knowledge_days
// -1(끔)은 적립하지 않는다(0은 loadThresholds가 기본 1로 코어스 — 미설정).
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

// TestMaybeSessionKnowledgeDedupSkip — v0.27.2: 같은 content_hash가 이미
// 있으면 재적립하지 않는다(같은 세션 재종료 차단).
func TestMaybeSessionKnowledgeDedupSkip(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	st.exists = true
	maybeSessionKnowledge(context.Background(), st, "이미 적립된 동일 내용의 요약", "a", "s1", 0)
	if len(st.inserted) != 0 {
		t.Fatalf("dedup 적립 = %d건, want 0", len(st.inserted))
	}
}

// TestDoneStdinSummaryActivatesKnowledge — v0.27.1: Stop 훅 stdin의
// compact_summary가 세션 지식화를 활성화한다(타 하네스 폴백 경로).
func TestDoneStdinSummaryActivatesKnowledge(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := knFake("")
	var out strings.Builder
	code := runDone(context.Background(), st, []string{"--session", "claude"},
		strings.NewReader(`{"hook_event_name":"Stop","compact_summary":"검색 품질 개선 작업 — RRF 융합 구현 완료"}`), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	// 적립 — knowledge pending 1건(Summary가 채워진 경우만).
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

// staleFake — fakeStore + StaleSessionRuns 고정 반환(stale 분기 유도).
type staleFake struct {
	*fakeStore
	runs int
}

func (s *staleFake) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	return s.runs, nil
}

// TestDoneStalePathAccruesKnowledge — v0.27.2: stale 경로(미기록 연속
// 경고)에서도 요약이 있으면 지식화 — 조기 return에 갇히지 않는다.
func TestDoneStalePathAccruesKnowledge(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &staleFake{fakeStore: knFake(""), runs: 99}
	var out strings.Builder
	code := runDone(context.Background(), st, []string{"--session", "claude"},
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

// TestDoneStdinSummaryBranches — v0.27.2: invalid JSON·필드 부재는
// 빈 값(폴백 상용구 스킵 계약 — 유효 요약의 무증상 폐기 방지 회귀).
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

// TestDoneCommitsAuditTrail — v0.29.1(동시성 감사 발견 8): 세션 종료는
// 세션 행·지식화·카운터 쓰기를 감사 커밋으로 귀속시킨다(무관한 다음
// write의 전역 스테이징 흡수 방지).
func TestDoneCommitsAuditTrail(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := knFake("")
	var out strings.Builder
	if code := runDone(context.Background(), st, []string{"--session", "claude"},
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
