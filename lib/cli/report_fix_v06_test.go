// report_fix_v06_test.go — regression: the defect where the stale and period queries'
// `$1 * interval '1 day'` failed on real Doltgres (incident:
// lib/cli/report.go collectCounts). $1 as int is inferred as interval (OID 1186), failing pgx
// encoding; even passing with an ::int cast, the comparison breaks (timestamptz <
// interval, fresh-DB e2e observation). The cutoff must be a Go-computed timestamptz parameter.
package cli

import (
	"strings"
	"testing"
	"time"
)

// TestReportCutoffTimestamptzParam — both the stale (read_count) and period (invalid_at >=)
// queries take the cutoff time as the first argument with no interval arithmetic in the SQL.
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
	sawStale, sawPeriod, sawDead := false, false, false
	for _, q := range argsBySQL {
		// query identification uses the product SQL's self-identifying markers (dissolving
		// the distortion where substring guesswork co-evolved with product SQL rewrites).
		isStale := strings.Contains(q.sql, "/* stale */")
		isPeriod := strings.Contains(q.sql, "invalid_at >=")
		isDead := strings.Contains(q.sql, "/* dead */")
		if isStale {
			sawStale = true
		}
		if isPeriod {
			sawPeriod = true
		}
		if isDead {
			sawDead = true
		}
		if !isStale && !isPeriod && !isDead {
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
			wantDays = 90 // the stale tally's reference days (reportStaleDays)
		}
		if isDead {
			wantDays = 30 // the dead_pct reference days (reportDeadDays)
		}
		if el := time.Since(cut); el < time.Duration(wantDays-1)*24*time.Hour || el > time.Duration(wantDays+1)*24*time.Hour {
			t.Errorf("컷오프 경과=%v, want 약 %d일", el, wantDays)
		}
	}
	if !sawStale || !sawPeriod || !sawDead {
		t.Fatalf("stale(%v)·기간(%v)·dead(%v) 조회 미관측(총 %d건)", sawStale, sawPeriod, sawDead, len(queries))
	}
}
