package sqlfs

import (
	"regexp"
	"strings"
	"testing"
)

func tableBlock(t *testing.T, table string) string {
	t.Helper()
	re := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + table + ` \((.*?)\);`)
	m := re.FindStringSubmatch(SchemaSQL())
	if m == nil {
		t.Fatalf("schema.sql에 %s 테이블 정의 없음", table)
	}
	return m[1]
}

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

func TestSchemaVersionThree(t *testing.T) {
	if !strings.Contains(SchemaSQL(), "meta.schema_version=3") {
		t.Error("schema.sql 헤더에 meta.schema_version=3 명시 누락")
	}
}

func TestSchemaV3Indexes(t *testing.T) {
	for _, col := range []string{"entity", "domain", "status"} {
		re := regexp.MustCompile(
			`(?m)^CREATE INDEX IF NOT EXISTS flint_knowledge_` + col + `_idx\s+ON knowledge \(` + col + `\);$`)
		if !re.MatchString(SchemaSQL()) {
			t.Errorf("schema.sql에 knowledge(%s) 인덱스 누락", col)
		}
	}
}

func TestSchemaV12NoV11RecallColumns(t *testing.T) {
	knowledge := tableBlock(t, "knowledge")
	for _, col := range []string{"recall_score", "last_recalled_at"} {
		re := regexp.MustCompile(`(?m)^\s*` + col + `\s`)
		if re.MatchString(knowledge) {
			t.Errorf("schema.sql knowledge 정의에 폐기된 v11 열 %s 잔존 — 신규 DB는 v12 종착 직행:\n%s", col, knowledge)
		}
	}
}
