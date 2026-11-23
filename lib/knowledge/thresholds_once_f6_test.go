package knowledge

import (
	"context"
	"strings"
	"testing"
)

func TestReadZeroHitThresholdsLoadedOnce(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"없는토픽"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
	}
	n := 0
	for _, q := range st.queries {

		if strings.Contains(q, "FROM meta WHERE key IN") && strings.Contains(q, "'stale_days'") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("thresholds IN 쿼리 %d회, want 1회(rrf_k·제안 주입 공유)", n)
	}
}
