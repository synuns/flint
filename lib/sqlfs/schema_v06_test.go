// schema_v06_test.go — schema v4 (item reference-metering columns)
// fast tests.
package sqlfs

import (
	"regexp"
	"testing"
)

// TestSchemaV4Columns — the v4 new columns knowledge.read_count·
// last_read_at are included at fresh-DB creation (the schema SQL).
// Existing v3 DBs get them via admin.go's EnsureSchemaV4 gate ALTER.
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
