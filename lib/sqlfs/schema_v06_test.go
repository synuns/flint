// schema_v06_test.go — T59 schema v4(항목 참조 계측 컬럼) fast 테스트.
package sqlfs

import (
	"regexp"
	"testing"
)

// TestSchemaV4Columns — v4 신규 컬럼 knowledge.read_count·last_read_at가
// 신규 DB 생성 시점(스키마 SQL)에 포함된다. 기존 v3 DB는 admin.go의
// EnsureSchemaV4 게이트가 ALTER로 적용한다.
func TestSchemaV4Columns(t *testing.T) {
	knowledge := tableBlock(t, "knowledge")
	if !regexp.MustCompile(`(?m)^\s*read_count\s+integer\s+NOT NULL DEFAULT 0\s*(,|--|$)`).
		MatchString(knowledge) {
		t.Errorf("knowledge에 read_count integer NOT NULL DEFAULT 0 컬럼 누락:\n%s", knowledge)
	}
	if !regexp.MustCompile(`(?m)^\s*last_read_at\s+timestamptz\s*(,|--|$)`).
		MatchString(knowledge) {
		t.Errorf("knowledge에 last_read_at timestamptz 컬럼 누락:\n%s", knowledge)
	}
}
