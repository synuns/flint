// read_v0120_test.go — v0.12.0 T1: BuildReadStatus base 테이블 status 필터.
package query

import (
	"strings"
	"testing"
)

func TestBuildReadStatusSQL(t *testing.T) {
	sql, args := BuildReadStatus("", Filters{Status: "pending", Limit: 20})
	if !strings.Contains(sql, "FROM knowledge") || strings.Contains(sql, "active_knowledge") {
		t.Errorf("base 테이블 미사용: %s", sql)
	}
	if !strings.Contains(sql, "status = $") {
		t.Errorf("status 조건 누락: %s", sql)
	}
	if len(args) == 0 || args[0] != "pending" {
		t.Errorf("파라미터 = %v", args)
	}
}
