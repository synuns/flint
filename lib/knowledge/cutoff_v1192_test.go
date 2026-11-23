package knowledge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

func TestVerdictForPendingAgeOverflow(t *testing.T) {
	now := time.Now()
	th := store.DefaultThresholds()
	th.PendingAgeDays = 200000
	it := pendingItem{ID: 7, Topic: "demo-topic", Summary: "demo summary", ValidAt: now.Add(-200 * 24 * time.Hour)}
	v := verdictFor(it, th, now, nil)
	if v.Kind == "expire" {
		t.Fatalf("판정 = expire(%s), want expire 아님(래프 시 전 건 만료 판정)", v.Why)
	}
	if v.Kind != "hold" {
		t.Fatalf("판정 = %s, want hold", v.Kind)
	}
}

func TestVerdictForPromoteOverflow(t *testing.T) {
	now := time.Now()
	th := store.DefaultThresholds()
	th.PendingPromoteDays = 200000
	it := pendingItem{ID: 8, Topic: "demo-topic", Summary: strings.Repeat("요약", 30), ValidAt: now.Add(-1 * time.Hour)}
	v := verdictFor(it, th, now, nil)
	if v.Kind != "promote" {
		t.Fatalf("판정 = %s(%s), want promote(래프 시 승급 불가)", v.Kind, v.Why)
	}
}

func gateOverflowFake(last string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		for _, a := range args {
			if s, ok := a.(string); ok && s == "session_knowledge_last" && last != "" {
				return [][]any{{last}}, nil
			}
		}
		if strings.Contains(sql, "FROM meta WHERE key IN") {
			return [][]any{{"session_knowledge_days", "200000"}}, nil
		}
		return nil, nil
	}}
}

func TestSessionKnowledgeGateOverflow(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())

	t.Run("100일 경과 — 게이트 유지", func(t *testing.T) {
		st := gateOverflowFake(time.Now().UTC().Add(-100 * 24 * time.Hour).Format(time.RFC3339))
		maybeSessionKnowledge(context.Background(), st, "검색 품질 개선 작업을 완료했다", "a", "s1", 0)
		if len(st.inserted) != 0 {
			t.Fatalf("적립 = %d건, want 0(빈도 상한 게이트 — 랩 시 매 세션 적립)", len(st.inserted))
		}
	})
	t.Run("200000일 경과 — 게이트 해제", func(t *testing.T) {
		st := gateOverflowFake(time.Now().UTC().AddDate(0, 0, -200000).Format(time.RFC3339))
		maybeSessionKnowledge(context.Background(), st, "검색 품질 개선 작업을 완료했다", "a", "s1", 0)
		if len(st.inserted) != 1 {
			t.Fatalf("적립 = %d건, want 1(클램프가 무조건 차단 아님 증명)", len(st.inserted))
		}
	})
}
