package query

import (
	"strings"
	"testing"
)

// TestRankOrderTieBreak — v0.6 tie-breaker(T68): 기존 정렬 절(topic 정합 >
// confidence > 최신) 접두 불변, 말미에 last_read_at DESC NULLS LAST 추가.
// NULLS LAST로 미참조 항목(last_read_at NULL)은 동률에서 꼴찌가 된다.
func TestRankOrderTieBreak(t *testing.T) {
	cases := []struct {
		name string
		q    string
	}{
		{"검색어", "rust"},
		{"빈 쿼리", ""},
		{"작은따옴표 이스케이프", "it's"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RankOrder(tc.q)
			// 접미: last_read_at DESC NULLS LAST 추가 + v0.13.0 R1 최종
			// 타이브레이크 read_count DESC(룰링 갱신).
			if !strings.HasSuffix(got, "last_read_at DESC NULLS LAST, read_count DESC") {
				t.Fatalf("RankOrder(%q) 접미 불일치: %q", tc.q, got)
			}
			// 접두: 기존 절(topic 정합 > confidence > 최신) 불변.
			wantPrefix := "ORDER BY (lower(topic)=lower('" + strings.ReplaceAll(tc.q, "'", "''") +
				"')) DESC, confidence DESC, ingested_at DESC"
			if !strings.HasPrefix(got, wantPrefix) {
				t.Fatalf("RankOrder(%q) 접두 불변 위반:\n got  %q\n want %q", tc.q, got, wantPrefix)
			}
		})
	}
}
