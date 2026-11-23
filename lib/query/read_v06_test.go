package query

import (
	"fmt"
	"strings"
	"testing"
)

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

			if !strings.HasSuffix(got, "last_read_at DESC NULLS LAST, read_count DESC") {
				t.Fatalf("RankOrder 접미 불일치: %q", got)
			}

			wantPrefix := fmt.Sprintf("ORDER BY (lower(topic)=lower($%d)) DESC, confidence DESC, ingested_at DESC", tc.placeholder)
			if !strings.HasPrefix(got, wantPrefix) {
				t.Fatalf("RankOrder 접두 불변 위반:\n got  %q\n want %q", got, wantPrefix)
			}
		})
	}
}
