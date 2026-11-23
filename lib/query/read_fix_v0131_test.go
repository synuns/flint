package query

import (
	"regexp"
	"strings"
	"testing"
)

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

func fixWhereClause(sql string) string {
	i := strings.Index(sql, " WHERE ")
	j := strings.Index(sql, " ORDER BY ")
	if i < 0 || j < 0 || j < i {
		return ""
	}
	return sql[i+len(" WHERE ") : j]
}
