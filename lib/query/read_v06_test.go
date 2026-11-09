package query

import (
	"fmt"
	"strings"
	"testing"
)

// TestRankOrderTieBreak — v0.6 tie-breaker(T68): 기존 정렬 절(topic 정합 >
// confidence > 최신) 접두 불변, 말미에 last_read_at DESC NULLS LAST 추가.
// NULLS LAST로 미참조 항목(last_read_at NULL)은 동률에서 꼴찌가 된다.
// v0.28.0(H③): q는 파라미터($N) — 이스케이프 케이스는 인젝션 표면
// 자체가 사라졌으므로 placeholder 형태 정합으로 재작성.
func TestRankOrderTieBreak(t *testing.T) {
	cases := []struct {
		name        string
		placeholder int
	}{
		{"통상 파라미터", 3},
		{"첫 파라미터", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RankOrder(tc.placeholder)
			// 접미: last_read_at DESC NULLS LAST 추가 + v0.13.0 R1 최종
			// 타이브레이크 read_count DESC(룰링 갱신).
			if !strings.HasSuffix(got, "last_read_at DESC NULLS LAST, read_count DESC") {
				t.Fatalf("RankOrder 접미 불일치: %q", got)
			}
			// 접두: 기존 절(topic 정합 > confidence > 최신) 불변 — 파라미터형.
			wantPrefix := fmt.Sprintf("ORDER BY (lower(topic)=lower($%d)) DESC, confidence DESC, ingested_at DESC", tc.placeholder)
			if !strings.HasPrefix(got, wantPrefix) {
				t.Fatalf("RankOrder 접두 불변 위반:\n got  %q\n want %q", got, wantPrefix)
			}
		})
	}
}
