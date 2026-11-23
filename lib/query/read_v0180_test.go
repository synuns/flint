package query

import (
	"strings"
	"testing"
)

func TestBuildReadFallbackIncludesTags(t *testing.T) {
	sql, _, ok := BuildReadFallback("doltgres 포트", Filters{})
	if !ok {
		t.Fatal("ok=false, want 폴백 조립(토큰 2)")
	}
	if !strings.Contains(sql, "topic || ' ' || summary || ' ' || tags") {
		t.Fatalf("폴백 접합면에 tags 누락: %s", sql)
	}
}

func TestBuildReadPhraseSurfaceUnchanged(t *testing.T) {
	sql, _ := BuildRead("doltgres", Filters{})
	if !strings.Contains(sql, "lower(topic || ' ' || summary) LIKE") {
		t.Fatalf("구문 접합면 변형: %s", sql)
	}
	if strings.Contains(sql, "|| ' ' || tags") {
		t.Fatalf("구문 쿼리에 tags 유입 금지: %s", sql)
	}
}
