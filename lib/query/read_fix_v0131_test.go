// read_fix_v0131_test.go — --status active's active definition made
// equivalent to the active_knowledge view (invalid_at IS NULL) — preventing a
// definitional split where rows the view blocks (active rows with invalid_at set)
// appear only under --status active. pending and invalidated, with different
// invalid_at conventions, get no such condition. Also pins that the two assemblers'
// q/tag/entity/domain filters are always identical (the shared assembly).
package query

import (
	"regexp"
	"strings"
	"testing"
)

// TestBuildReadStatusActiveInvalidAt — the status='active' SQL includes the
// invalid_at IS NULL condition (active rows with invalid_at set are excluded by
// it). It must not be attached to pending or invalidated (for invalidated, having
// invalid_at set is the essence — attaching yields zero hits; pending has a different pre-promotion convention).
func TestBuildReadStatusActiveInvalidAt(t *testing.T) {
	sql, _ := BuildReadStatus("", Filters{Status: "active", Limit: 10})
	if !strings.Contains(sql, "invalid_at IS NULL") {
		t.Errorf("active 조회에 invalid_at IS NULL 누락(뷰와 다른 active 정의): %s", sql)
	}
	for _, s := range []string{"pending", "invalidated"} {
		sql, _ := BuildReadStatus("", Filters{Status: s, Limit: 10})
		if strings.Contains(sql, "invalid_at IS NULL") {
			t.Errorf("%s 조회에 invalid_at 조건 부착(규약 상이 — 미부착이 맞음): %s", s, sql)
		}
	}
}

// TestBuildReadReadStatusFilterParity — the filter assemblies (q, tag, entity,
// domain) of BuildRead and BuildReadStatus must be identical down to the clause strings
// and order (the placeholder numbers shift by the base's leading status condition, so the comparison normalizes them).
func TestBuildReadReadStatusFilterParity(t *testing.T) {
	f := Filters{Tag: "a", Entity: "e", Domain: "d", Limit: 7}
	viewSQL, _ := BuildRead("키워드", f)
	baseSQL, _ := BuildReadStatus("키워드", f)

	viewWhere := fixWhereClause(viewSQL)
	baseWhere := strings.TrimPrefix(fixWhereClause(baseSQL), "status = $1 AND ")
	if baseWhere == fixWhereClause(baseSQL) {
		t.Fatalf("base where 절 파싱 실패(선행 status 조건 없음): %s", baseSQL)
	}
	norm := regexp.MustCompile(`\$\d+`)
	if norm.ReplaceAllString(viewWhere, `$?`) != norm.ReplaceAllString(baseWhere, `$?`) {
		t.Errorf("필터 조립 분열:\n view %s\n base %s", viewWhere, baseWhere)
	}
}

// fixWhereClause — extracts the WHERE clause body from the SQL (up to just before ORDER BY).
func fixWhereClause(sql string) string {
	i := strings.Index(sql, " WHERE ")
	j := strings.Index(sql, " ORDER BY ")
	if i < 0 || j < 0 || j < i {
		return ""
	}
	return sql[i+len(" WHERE ") : j]
}
