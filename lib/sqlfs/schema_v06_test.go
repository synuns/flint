package sqlfs

import (
	"regexp"
	"testing"
)

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
