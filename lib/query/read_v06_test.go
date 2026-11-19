package query

import (
	"fmt"
	"strings"
	"testing"
)

// TestRankOrderTieBreak — the tiebreaker: the existing sort clause (topic match
// > confidence > newest) keeps its prefix; a trailing last_read_at DESC NULLS LAST is added.
// With NULLS LAST, unreferenced items (last_read_at NULL) rank last among ties.
// q is a parameter ($N) — the escaping cases were rewritten as placeholder-form
// matching, since the injection surface itself is gone.
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
			// suffix: last_read_at DESC NULLS LAST added + the final tiebreak
			// read_count DESC (a ruling update).
			if !strings.HasSuffix(got, "last_read_at DESC NULLS LAST, read_count DESC") {
				t.Fatalf("RankOrder 접미 불일치: %q", got)
			}
			// prefix: the existing clause (topic match > confidence > newest) unchanged — parameterized.
			wantPrefix := fmt.Sprintf("ORDER BY (lower(topic)=lower($%d)) DESC, confidence DESC, ingested_at DESC", tc.placeholder)
			if !strings.HasPrefix(got, wantPrefix) {
				t.Fatalf("RankOrder 접두 불변 위반:\n got  %q\n want %q", got, wantPrefix)
			}
		})
	}
}
