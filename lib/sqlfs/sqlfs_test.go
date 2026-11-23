package sqlfs

import (
	"strings"
	"testing"
)

func TestSchemaHasUniqueHash(t *testing.T) {
	s := SchemaSQL()
	if s == "" {
		t.Fatal("SchemaSQL()이 빈 문자열")
	}
	if !strings.Contains(s, "content_hash") {
		t.Error("SchemaSQL()에 content_hash 컬럼 누락")
	}
	if !strings.Contains(s, "UNIQUE") {
		t.Error("SchemaSQL()에 UNIQUE 제약 누락")
	}
}

func TestViewsFilterInvalid(t *testing.T) {
	v := ViewsSQL()
	if v == "" {
		t.Fatal("ViewsSQL()이 빈 문자열")
	}
	if !strings.Contains(v, "invalid_at IS NULL") {
		t.Error("ViewsSQL()에 invalid_at IS NULL 필터 누락")
	}
	if !strings.Contains(v, "active_knowledge") || !strings.Contains(v, "hot_view") {
		t.Error("ViewsSQL()에 active_knowledge/hot_view 뷰 누락")
	}
}

func TestHotViewSevenDays(t *testing.T) {
	if !strings.Contains(ViewsSQL(), "interval '7 days'") {
		t.Error("ViewsSQL()에 7일 컷(now() - interval '7 days') 누락")
	}
}
