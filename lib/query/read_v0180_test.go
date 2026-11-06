// read_v0180_test.go — v0.18.0 R5: 폴백 검색면에 tags 포함. 태그로만
// 분류된 지식(topic·summary에 어휘 없음)이 단어 폴백에서 회수된다 —
// 폴백 유입의 주요 원인 중 하나를 제거한다.
package query

import (
	"strings"
	"testing"
)

// TestBuildReadFallbackIncludesTags — 폴백 LIKE 접합면이
// topic || ' ' || summary || ' ' || tags로 확장된다(구문 쿼리는 불변).
func TestBuildReadFallbackIncludesTags(t *testing.T) {
	sql, _, ok := BuildReadFallback("doltgres 포트", Filters{})
	if !ok {
		t.Fatal("ok=false, want 폴백 조립(토큰 2)")
	}
	if !strings.Contains(sql, "topic || ' ' || summary || ' ' || tags") {
		t.Fatalf("폴백 접합면에 tags 누락: %s", sql)
	}
}

// TestBuildReadPhraseSurfaceUnchanged — 구문(phrase) 쿼리 접합면은
// topic || ' ' || summary 그대로(tags는 --tag 필터의 영역 — 계약 불변).
func TestBuildReadPhraseSurfaceUnchanged(t *testing.T) {
	sql, _ := BuildRead("doltgres", Filters{})
	if !strings.Contains(sql, "lower(topic || ' ' || summary) LIKE") {
		t.Fatalf("구문 접합면 변형: %s", sql)
	}
	if strings.Contains(sql, "|| ' ' || tags") {
		t.Fatalf("구문 쿼리에 tags 유입 금지: %s", sql)
	}
}
