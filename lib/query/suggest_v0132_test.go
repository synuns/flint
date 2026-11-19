// suggest_v0132_test.go — the suggestion scan cap.
// Correcting spec §3 R1's 'no scan cap' — defending against unbounded full scans.
package query

import (
	"regexp"
	"strconv"
	"testing"
)

// TestBuildSuggestScanCapped — N13: the suggestion scan SQL has a LIMIT cap
// (5000+, accepting personal-scale thousands of rows; defending against anomalous
// mass intake). Before the fix it was an uncapped full scan.
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
