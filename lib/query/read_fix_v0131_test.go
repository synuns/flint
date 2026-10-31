// read_fix_v0131_test.go — 심층 리뷰 F3·F8 회귀. F3: --status active의
// active 정의를 active_knowledge 뷰(invalid_at IS NULL)와 동치화 — 뷰가
// 차단하는 행(invalid_at 설정 active 행)이 --status active에만 보이는 정의
// 분열 방지. pending·invalidated는 invalid_at 규약이 달라 조건 미부착.
// F8: 두 조립기의 q·tag·entity·domain 필터가 언제나 동일(공유 조립)함을
// 고정. 기존 테스트 수정 금지 룰에 따라 신규 파일.
package query

import (
	"regexp"
	"strings"
	"testing"
)

// TestBuildReadStatusActiveInvalidAt — status='active' SQL은 invalid_at IS
// NULL 조건 포함(invalid_at이 설정된 active 행은 이 조건으로 제외됨).
// pending·invalidated에는 부착되지 않아야 한다(invalidated는 invalid_at
// 설정이 본질 — 부착 시 영(零)히트, pending은 승격 전 규약 상이).
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

// TestBuildReadReadStatusFilterParity — BuildRead·BuildReadStatus의 필터
// 조립(q·tag·entity·domain)은 절 문자열·순서까지 동일해야 한다(placeholder
// 번호는 base의 선행 status 조건만큼 shift하므로 정규화해 비교).
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

// fixWhereClause — SQL에서 WHERE 절 본문(ORDER BY 직전까지) 추출.
func fixWhereClause(sql string) string {
	i := strings.Index(sql, " WHERE ")
	j := strings.Index(sql, " ORDER BY ")
	if i < 0 || j < 0 || j < i {
		return ""
	}
	return sql[i+len(" WHERE "):j]
}
