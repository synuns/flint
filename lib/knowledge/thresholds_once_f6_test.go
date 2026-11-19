// thresholds_once_f6_test.go — F6 (performance) regression: on the read
// zero-hit path, the thresholds IN query runs once. The rrf_k injection
// (fallback rerank) and the suggestion-threshold injection each used to
// call LoadThresholdsFn (twice); now they reuse the single load from the
// first eligible block.
package knowledge

import (
	"context"
	"strings"
	"testing"
)

// TestReadZeroHitThresholdsLoadedOnce — on an eligible zero-hit read, one
// thresholds IN query (two before the fix).
func TestReadZeroHitThresholdsLoadedOnce(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // 0 rows for every query — zero-hit (fallback·suggestion both attempted)
	}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"없는토픽"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
	}
	n := 0
	for _, q := range st.queries {
		// Distinguished from other meta IN lookups such as hot_stats —
		// judged by the inclusion of a thresholds registry key
		// ('stale_days').
		if strings.Contains(q, "FROM meta WHERE key IN") && strings.Contains(q, "'stale_days'") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("thresholds IN 쿼리 %d회, want 1회(rrf_k·제안 주입 공유)", n)
	}
}
