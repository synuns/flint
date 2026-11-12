// pending_v0290_test.go — v0.29.0 증류 2막 계약: pending 심사 뷰·approve
// 병합·세션 지식 topic 품질·session_id 연결.
// v0.39.0 T4: lib/cli에서 이동(cli/pending_v0290_test.go 원본).
package knowledge

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

// TestSummaryKeyword — R3: topic 키워드 추출(첫 구분자·20룬 상한).
func TestSummaryKeyword(t *testing.T) {
	cases := []struct{ in, want string }{
		{"v0.27.2 패치: 세션 지식화를 PostCompact 표준 경로로 활성화했다", "v0.27.2 패치: 세션 지식화를"}, // 20룬 상한
		{"짧", "짧"}, // 상한 없음·짧은 그대로
		{"아주 긴 문장인데 구분자가 없어서 20룬 상한에 걸린다 여기까지가 딱 맞음", "아주 긴 문장인데 구분자가 없어서 2"},
	}
	for _, c := range cases {
		if got := summaryKeyword(c.in); got != c.want {
			t.Errorf("summaryKeyword(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMaybeSessionKnowledgeTopicQuality — R3: topic에 요약 키워드 반영.
func TestMaybeSessionKnowledgeTopicQuality(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := knFake("")
	maybeSessionKnowledge(context.Background(), st, "검색 품질 개선 작업을 RRF 융합으로 완료했다", "claude", "s1", 0)
	if len(st.inserted) != 1 {
		t.Fatalf("적립 = %d, want 1", len(st.inserted))
	}
	topic := st.inserted[0]["topic"].(string)
	if !strings.Contains(topic, "세션[claude]") || !strings.Contains(topic, "검색 품질") {
		t.Errorf("topic 품질 미반영: %q", topic)
	}
}

// TestMaybeSessionKnowledgeSessionID — R4: agent의 최신 sessions.id 연결.
func TestMaybeSessionKnowledgeSessionID(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		for _, a := range args {
			if s, ok := a.(string); ok && s == "session_knowledge_last" {
				return nil, nil
			}
		}
		if strings.Contains(sql, "FROM sessions") {
			return [][]any{{int32(7)}}, nil
		}
		return nil, nil
	}}
	maybeSessionKnowledge(context.Background(), st, "세션 지식의 추적성을 위한 session_id 연결 검증", "claude", "s1", 0)
	if len(st.inserted) != 1 {
		t.Fatalf("적립 = %d, want 1", len(st.inserted))
	}
	sid, ok := st.inserted[0]["session_id"].(int)
	if !ok || sid != 7 {
		t.Errorf("session_id = %#v, want 7", st.inserted[0]["session_id"])
	}
}

// TestRunPendingVerdicts — R1: 삼단 추천(승격·병합·만료·보류) 분류.
func TestRunPendingVerdicts(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	old, recent := time.Now().UTC().Add(-20*24*time.Hour), time.Now().UTC().Add(-24*time.Hour)
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil // 기본 임계(PendingAgeDays 7)
		}
		if strings.Contains(sql, "status = 'pending'") {
			return [][]any{
				{int32(1), "오래된 항목", "본문이 충분히 길다 — 만료 판정 대상", "agent", old},
				{int32(2), "최근 항목", "본문이 충분히 길어서 승격 판정 대상이 되는 최소 길이를 넘기는 예시 문장이다", "hook", recent},
				{int32(3), "짧은 최근", "짧다", "hook", recent},
			}, nil
		}
		if strings.Contains(sql, "status = 'active'") {
			return nil, nil // 유사 active 없음
		}
		return nil, nil
	}}
	var out strings.Builder
	code := RunPending(context.Background(), st, nil, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	s := out.String()
	for _, want := range []string{
		"1|만료|", "2|승격|", "3|보류|",
		`"expire":1`, `"promote":1`, `"hold":1`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("출력에 %q 부재:\n%s", want, s)
		}
	}
}

// TestRunPendingMergeHint — R1: 유사 active 존재 시 병합 추천.
func TestRunPendingMergeHint(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	recent := time.Now().UTC().Add(-24 * time.Hour)
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "status = 'pending'") {
			return [][]any{{int32(9), "표 파이프 이스케이프 규칙(합성 데모)", "유사 active가 있는 최신 항목 — 병합 추천 대상", "agent", recent}}, nil
		}
		if strings.Contains(sql, "status = 'active'") {
			// v0.29.0 리뷰: 유사 판정은 (id, topic) 2열 — 접두 일치.
			return [][]any{{int32(4), "표 파이프 이스케이프 규칙(합성 데모) 기존 활성"}}, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := RunPending(context.Background(), st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "9|병합|") || !strings.Contains(out.String(), `"merge":1`) {
		t.Errorf("병합 추천 미출력:\n%s", out.String())
	}
}

// TestRunPendingJSONCountsConsistency — v0.32.1(감사 1): valid_at 파싱
// 불가 행의 counts·items 모순 수선 — 양쪽 다 hold로 일치.
func TestRunPendingJSONCountsConsistency(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	// valid_at 셀에 time.Time 아닌 값 → cellToTime 실패 → zero ValidAt.
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "status = 'pending'") {
			return [][]any{{int32(11), "파손 시각 행", "본문이 충분히 긴 합성 요약 행입니다", "agent", "not-a-time"}}, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := RunPending(context.Background(), st, []string{"--json"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	s := out.String()
	if strings.Contains(s, `"expire":1`) {
		t.Errorf("counts 모순 — 파손 행이 expire로 집계: %s", s)
	}
	if !strings.Contains(s, `"hold":1`) || !strings.Contains(s, `"verdict":"hold"`) {
		t.Errorf("양쪽 hold 일치 없음: %s", s)
	}
	// v0.33.0(패치 감사 1): age_days도 0 — 292년치 표기 회귀 봉쇄.
	if !strings.Contains(s, `"age_days":0`) {
		t.Errorf("zero valid_at의 age_days 비정상: %s", s)
	}
	if strings.Contains(s, `"items":null`) {
		t.Errorf("items null: %s", s)
	}
}
