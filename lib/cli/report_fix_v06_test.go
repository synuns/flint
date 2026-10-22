// report_fix_v06_test.go — T70 회귀: report stale·기간 조회의
// `$1 * interval '1 day'`가 실 Doltgres에서 실패하던 결함(인시던트:
// lib/cli/report.go collectCounts). $1 int는 interval(OID 1186)로 추론돼 pgx
// 인코딩에 실패하고, ::int 캐스트로 넘어가도 평가가 깨진다(timestamptz <
// interval, fresh-DB e2e 실측). 컷오프는 Go 계산 timestamptz 파라미터여야 한다.
package cli

import (
	"strings"
	"testing"
	"time"
)

// TestReportCutoffTimestamptzParam — stale(read_count)·기간(invalid_at >=)
// 조회 모두 SQL에 interval 연산 없이 컷오프 시각을 첫 인자로 받는다.
func TestReportCutoffTimestamptzParam(t *testing.T) {
	var queries []string
	var argsBySQL []struct {
		sql  string
		args []any
	}
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		queries = append(queries, sql)
		argsBySQL = append(argsBySQL, struct {
			sql  string
			args []any
		}{sql, args})
		switch {
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_stats"):
			return [][]any{{"read_stats", `{"calls":0,"zero":0}`}, {"hot_stats", `{"lens":[]}`}}, nil
		case strings.Contains(sql, "invalid_at IS NOT NULL"):
			return [][]any{{int64(0), int64(0), int64(0)}}, nil
		case strings.Contains(sql, "read_count"):
			return [][]any{{int64(0), int64(0)}}, nil
		case strings.Contains(sql, "invalid_at >="):
			return [][]any{{int64(0)}}, nil
		}
		return nil, nil
	}}
	if _, code := runReportArgs(t, f, "--days", "7"); code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	sawStale, sawPeriod := false, false
	for _, q := range argsBySQL {
		isStale := strings.Contains(q.sql, "read_count")
		isPeriod := strings.Contains(q.sql, "invalid_at >=")
		if isStale {
			sawStale = true
		}
		if isPeriod {
			sawPeriod = true
		}
		if !isStale && !isPeriod {
			continue
		}
		if strings.Contains(q.sql, "interval") {
			t.Errorf("SQL에 interval 연산 잔존(Doltgres 결함): %q", q.sql)
		}
		if len(q.args) != 1 {
			t.Fatalf("인자 수=%d, want 1(cutoff): %v", len(q.args), q.args)
		}
		cut, ok := q.args[0].(time.Time)
		if !ok {
			t.Fatalf("첫 인자가 timestamptz 컷오프가 아님: %T(%v)", q.args[0], q.args[0])
		}
		wantDays := 7
		if isStale {
			wantDays = 90 // stale 집계 기준일(reportStaleDays)
		}
		if el := time.Since(cut); el < time.Duration(wantDays-1)*24*time.Hour || el > time.Duration(wantDays+1)*24*time.Hour {
			t.Errorf("컷오프 경과=%v, want 약 %d일", el, wantDays)
		}
	}
	if !sawStale || !sawPeriod {
		t.Fatalf("stale(%v)·기간(%v) 조회 미관측(총 %d건)", sawStale, sawPeriod, len(queries))
	}
}
