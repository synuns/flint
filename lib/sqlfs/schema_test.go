package sqlfs

import (
	"regexp"
	"strings"
	"testing"
)

// tableBlock은 schema.sql에서 해당 CREATE TABLE 문 블록을 추출한다.
func tableBlock(t *testing.T, table string) string {
	t.Helper()
	re := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + table + ` \((.*?)\);`)
	m := re.FindStringSubmatch(SchemaSQL())
	if m == nil {
		t.Fatalf("schema.sql에 %s 테이블 정의 없음", table)
	}
	return m[1]
}

// TestSchemaV2Columns — v2 신규 컬럼 sessions.tasks·projects.note가
// 각 테이블 정의에 포함되어야 한다(ADR-0004: v0.1이 경고만 하던 키 수용).
func TestSchemaV2Columns(t *testing.T) {
	sessions := tableBlock(t, "sessions")
	if !regexp.MustCompile(`(?m)^\s*tasks\s+text\s*(,|--|$)`).MatchString(sessions) {
		t.Errorf("sessions에 tasks text 컬럼 누락:\n%s", sessions)
	}

	projects := tableBlock(t, "projects")
	if !regexp.MustCompile(`(?m)^\s*note\s+text\s*(,|--|$)`).MatchString(projects) {
		t.Errorf("projects에 note text 컬럼 누락:\n%s", projects)
	}
}

// TestSchemaVersionTwo — 스키마가 v2임을 명시한다.
// 실제 meta 갱신은 T17 admin.go의 버전 게이트가 담당한다.
func TestSchemaVersionTwo(t *testing.T) {
	if !strings.Contains(SchemaSQL(), "meta.schema_version=2") {
		t.Error("schema.sql 헤더에 meta.schema_version=2 명시 누락")
	}
}
