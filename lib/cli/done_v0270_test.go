// done_v0270_test.go — v0.27.0 R1: 세션 지식화 적립 계약.
package cli

import (
	"context"
	"strings"
	"testing"
)

// sessionKnFake — fakeStore에 nothing 추가(기본 fake로 충분: queryFn nil
// 반환 → meta 부재 → 빈도 통과, InsertBatch는 inserted에 녹음).
func TestMaybeSessionKnowledge(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	res := &DoneResult{Agent: "test-agent", Summary: "검색 품질 개선 작업을 완료했다"}
	maybeSessionKnowledge(context.Background(), st, res, "test-agent", "s1")
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
	// 증류 마커 적립(R2 회로 연결).
	if _, err := ReadTypedMarker(markerKindDistill); err != nil {
		t.Fatalf("증류 마커 미적립: %v", err)
	}
}

// TestMaybeSessionKnowledgeShortSummary — 최소 길이 미달은 미적립.
func TestMaybeSessionKnowledgeShortSummary(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	res := &DoneResult{Agent: "a", Summary: "짧다"}
	maybeSessionKnowledge(context.Background(), st, res, "a", "s1")
	if len(st.inserted) != 0 {
		t.Fatalf("짧은 요약 적립 = %d건, want 0", len(st.inserted))
	}
}
