// thresholds_once_f6_test.go — F6(성능) 회귀: read 제로히트 경로의
// thresholds IN 쿼리는 1회. rrf_k 주입(폴백 재랭킹)과 제안 임계값 주입이
// 각자 LoadThresholdsFn을 호출하던 것(2회)을 첫 eligible 블록의 1회
// 로드 결과 재사용으로.
package knowledge

import (
	"context"
	"strings"
	"testing"
)

// TestReadZeroHitThresholdsLoadedOnce — Review Focus: eligible 제로히트
// read에서 thresholds IN 쿼리 1회(수정 전 2회).
func TestReadZeroHitThresholdsLoadedOnce(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // 전 쿼리 0행 — 제로히트(폴백·제안 모두 시도)
	}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"없는토픽"}, strings.NewReader(""), &out)
	if code != 1 {
		t.Fatalf("exit = %d, want 1(제로히트 침묵)", code)
	}
	n := 0
	for _, q := range st.queries {
		// hot_stats 등 다른 meta IN 조회와 구분 — thresholds 레지스트리
		// 키('stale_days') 동봉으로 판정.
		if strings.Contains(q, "FROM meta WHERE key IN") && strings.Contains(q, "'stale_days'") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("thresholds IN 쿼리 %d회, want 1회(rrf_k·제안 주입 공유)", n)
	}
}
