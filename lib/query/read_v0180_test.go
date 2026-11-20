// read_v0180_test.go — the fallback search surface includes tags. Knowledge
// classified only by tags (no vocabulary in topic or summary) is recovered by the
// word fallback — removing one of the main causes of fallback intake.
package query

import (
	"strings"
	"testing"
)

// TestBuildReadFallbackIncludesTags — the fallback LIKE surface expands to
// topic || ' ' || summary || ' ' || tags (the phrase query unchanged).
func TestBuildReadFallbackIncludesTags(t *testing.T) {
	sql, _, ok := BuildReadFallback("doltgres 포트", Filters{})
	if !ok {
		t.Fatal("ok=false, want 폴백 조립(토큰 2)")
	}
	if !strings.Contains(sql, "topic || ' ' || summary || ' ' || tags") {
		t.Fatalf("폴백 접합면에 tags 누락: %s", sql)
	}
}

// TestBuildReadPhraseSurfaceUnchanged — the phrase query's surface stays
// topic || ' ' || summary as-is (tags belong to the --tag filter's domain — the contract unchanged).
func TestBuildReadPhraseSurfaceUnchanged(t *testing.T) {
	sql, _ := BuildRead("doltgres", Filters{})
	if !strings.Contains(sql, "lower(topic || ' ' || summary) LIKE") {
		t.Fatalf("구문 접합면 변형: %s", sql)
	}
	if strings.Contains(sql, "|| ' ' || tags") {
		t.Fatalf("구문 쿼리에 tags 유입 금지: %s", sql)
	}
}
