package sqlfs

import (
	"regexp"
	"strings"
	"testing"
)

// tableBlock extracts the CREATE TABLE statement block for the table
// from schema.sql.
func tableBlock(t *testing.T, table string) string {
	t.Helper()
	re := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + table + ` \((.*?)\);`)
	m := re.FindStringSubmatch(SchemaSQL())
	if m == nil {
		t.Fatalf("schema.sql에 %s 테이블 정의 없음", table)
	}
	return m[1]
}

// TestSchemaV2Columns — the v2 new columns sessions.tasks·projects.note
// must be in their table definitions (ADR-0004: accepting the keys v0.1
// only warned about).
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

// TestSchemaVersionThree — declares the schema is v3. The actual meta
// update belongs to admin.go's version gates (EnsureSchemaV2·V3).
func TestSchemaVersionThree(t *testing.T) {
	if !strings.Contains(SchemaSQL(), "meta.schema_version=3") {
		t.Error("schema.sql 헤더에 meta.schema_version=3 명시 누락")
	}
}

// TestSchemaV3Indexes — ADR-0008: the 3 filter-path (entity·domain·
// status) B-tree indexes are included at fresh-DB creation. %q% free
// text accepts a sequential scan.
func TestSchemaV3Indexes(t *testing.T) {
	for _, col := range []string{"entity", "domain", "status"} {
		re := regexp.MustCompile(
			`(?m)^CREATE INDEX IF NOT EXISTS flint_knowledge_` + col + `_idx\s+ON knowledge \(` + col + `\);$`)
		if !re.MatchString(SchemaSQL()) {
			t.Errorf("schema.sql에 knowledge(%s) 인덱스 누락", col)
		}
	}
}

// TestSchemaV12NoV11RecallColumns — v1.9.0 (R42): a fresh DB does not
// create the dropped v11 2 columns (recall_score·last_recalled_at) —
// schema.sql heads straight to the v12 terminus state (2 columns
// absent) from the start. The 2 columns are exclusive to the
// EnsureSchemaV12 gate promotion path of existing v11 DBs (the recall
// source is recall_events).
func TestSchemaV12NoV11RecallColumns(t *testing.T) {
	knowledge := tableBlock(t, "knowledge")
	for _, col := range []string{"recall_score", "last_recalled_at"} {
		re := regexp.MustCompile(`(?m)^\s*` + col + `\s`)
		if re.MatchString(knowledge) {
			t.Errorf("schema.sql knowledge 정의에 폐기된 v11 열 %s 잔존 — 신규 DB는 v12 종착 직행:\n%s", col, knowledge)
		}
	}
}
