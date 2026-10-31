// suggest_v0132_test.go — v0.13.2 심층 리뷰 회귀: N13 제안 스캔 상한.
// 스펙 §3 R1 '스캔 상한 없음' 정정 — 무상한 전체 스캔 방어.
package query

import (
	"regexp"
	"strconv"
	"testing"
)

// TestBuildSuggestScanCapped — N13: 제안 스캔 SQL은 LIMIT 상한을 가진다
// (개인 규모 수천 행은 수용하는 5000 이상, 이상 대량 유입은 방어).
// 수정 전에는 상한 없는 전체 스캔이었다.
func TestBuildSuggestScanCapped(t *testing.T) {
	sql, args := BuildSuggestScan()
	if len(args) != 0 {
		t.Errorf("스캔 인자 = %v, want nil(상한은 상수 인라인)", args)
	}
	m := regexp.MustCompile(`LIMIT (\d+)$`).FindStringSubmatch(sql)
	if m == nil {
		t.Fatalf("스캔 SQL에 LIMIT 상한 없음(무상한 전체 스캔): %s", sql)
	}
	if n, err := strconv.Atoi(m[1]); err != nil || n < 5000 {
		t.Errorf("스캔 상한 = %s, want ≥ 5000(개인 규모 수천 행 수용): %s", m[1], sql)
	}
}
