// report_test.go — T65 `flint report` fast 회귀(fake Store). numeric: 7일 창
// 경계(8일치 심기 → 7일만) · encoding: daily 부재 0·파손 스킵 · protocol: 인자·등록.
package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"flint/lib/store"
)

// newReportFake — report 조회 5종(meta daily·stats·counts·stale·기간 무효화)을
// SQL 텍스트로 구분해 반환하는 fake.
func newReportFake(daily map[string]string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "daily:"):
			rows := [][]any{}
			for k, v := range daily {
				rows = append(rows, []any{k, v})
			}
			return rows, nil
		case strings.Contains(sql, "read_stats"):
			return [][]any{
				{"read_stats", `{"calls":10,"zero":2}`},
				{"hot_stats", `{"calls":4,"sum":1000,"lens":[100,200,300,400]}`},
			}, nil
		case strings.Contains(sql, "invalid_at IS NOT NULL"):
			// pending·active·invalidated(총) — FILTER 3종 단일 행.
			return [][]any{{int64(3), int64(5), int64(2)}}, nil
		case strings.Contains(sql, "read_count"):
			// stale days90·never_read.
			return [][]any{{int64(2), int64(1)}}, nil
		case strings.Contains(sql, "invalid_at >="):
			// 기간(N일) 내 무효화 수.
			return [][]any{{int64(1)}}, nil
		}
		return nil, nil
	}}
}

func runReportArgs(t *testing.T, f Store, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := runReport(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

// reportDates — 구현과 동일 규약(UTC, 오늘 포함 N일)의 창 날짜 생성(테스트용).
func reportDates(n int) []string {
	now := time.Now().UTC()
	out := make([]string, 0, n)
	for i := n - 1; i >= 0; i-- {
		out = append(out, now.AddDate(0, 0, -i).Format("2006-01-02"))
	}
	return out
}

// TestReportSevenDayWindow — Review Focus numeric: 8일치 daily 키를 심어도
// 기본 7일 창의 7행만 나온다(8일째 배제). 파손된 당일-2 키는 0으로 스킵.
func TestReportSevenDayWindow(t *testing.T) {
	now := time.Now().UTC()
	daily := map[string]string{}
	for i := 0; i <= 7; i++ { // 오늘 포함 8일치
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		daily["daily:"+d] = `{"write":1,"hook":1,"manual":0,"mcp":0,"dup":0,"hot":2,"read":3}`
	}
	daily["daily:"+reportDates(7)[1]] = `{broken json` // 파손 → 0 폴백(스킵)
	f := newReportFake(daily)
	out, code := runReportArgs(t, f)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	var m struct {
		Days []struct {
			Date   string `json:"date"`
			Write  int    `json:"write"`
			Hook   int    `json:"hook"`
			Manual int    `json:"manual"`
			Mcp    int    `json:"mcp"`
			Dup    int    `json:"dup"`
			Hot    int    `json:"hot"`
			Read   int    `json:"read"`
		} `json:"days"`
		ZeroPct           int `json:"zero_pct"`
		HotP95            int `json:"hot_p95"`
		Counts            struct {
			Pending     int `json:"pending"`
			Active      int `json:"active"`
			Invalidated int `json:"invalidated"`
		} `json:"counts"`
		Stale struct {
			Days90    int `json:"days90"`
			NeverRead int `json:"never_read"`
		} `json:"stale"`
		InvalidatedPeriod int `json:"invalidated_period"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	want := reportDates(7)
	if len(m.Days) != 7 {
		t.Fatalf("days 길이=%d, want 7 (8일치 심었으나 창은 7): %q", len(m.Days), out)
	}
	for i, d := range m.Days {
		if d.Date != want[i] {
			t.Errorf("days[%d].date=%s, want %s", i, d.Date, want[i])
		}
	}
	last := m.Days[len(m.Days)-1]
	if last.Write != 1 || last.Hook != 1 || last.Hot != 2 || last.Read != 3 {
		t.Errorf("오늘 행 값 오류: %+v", last)
	}
	if m.Days[1].Write != 0 || m.Days[1].Read != 0 {
		t.Errorf("파손 키 스킵 실패(0이어야): %+v", m.Days[1])
	}
	if m.ZeroPct != 20 { // read_stats zero 2/10
		t.Errorf("zero_pct=%d, want 20", m.ZeroPct)
	}
	if m.HotP95 != 400 { // lens [100..400] p95=400
		t.Errorf("hot_p95=%d, want 400", m.HotP95)
	}
	if m.Counts.Pending != 3 || m.Counts.Active != 5 || m.Counts.Invalidated != 2 {
		t.Errorf("counts=%d/%d/%d, want 3/5/2", m.Counts.Pending, m.Counts.Active, m.Counts.Invalidated)
	}
	if m.Stale.Days90 != 2 || m.Stale.NeverRead != 1 {
		t.Errorf("stale=%d/%d, want 2/1", m.Stale.Days90, m.Stale.NeverRead)
	}
	if m.InvalidatedPeriod != 1 {
		t.Errorf("invalidated_period=%d, want 1", m.InvalidatedPeriod)
	}
}

// TestReportDaysCapAndEmpty — --days 상한 14(20은 14로) · daily 키 부재는
// 전일 0행으로 채운다(encoding: 부재 0).
func TestReportDaysCapAndEmpty(t *testing.T) {
	f := newReportFake(nil)
	out, code := runReportArgs(t, f, "--days", "20")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	var m struct {
		Days []map[string]any `json:"days"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	if len(m.Days) != 14 {
		t.Fatalf("상한 미적용: days 길이=%d, want 14", len(m.Days))
	}
	for i, d := range m.Days {
		for _, k := range []string{"write", "hook", "manual", "mcp", "dup", "hot", "read"} {
			if d[k] != float64(0) {
				t.Errorf("days[%d].%s=%v, want 0(부재)", i, k, d[k])
			}
		}
	}
}

// TestReportArgErrors — 무효 --days(비수)·알 수 없는 플래그는 사용법 에러 exit 1.
func TestReportArgErrors(t *testing.T) {
	for _, args := range [][]string{{"--days", "abc"}, {"--bogus"}, {"--days"}} {
		f := newReportFake(nil)
		out, code := runReportArgs(t, f, args...)
		if code != 1 {
			t.Errorf("%v: exit=%d, want 1 (출력 %q)", args, code, out)
		}
		if !strings.Contains(out, `"error"`) {
			t.Errorf("%v: 에러 JSON 아님: %q", args, out)
		}
	}
}

// TestReportServerDown — ping 실패는 errLine + MapExit(서버 다운 2).
func TestReportServerDown(t *testing.T) {
	f := &fakeStore{pingErr: store.ErrServerDown}
	out, code := runReportArgs(t, f)
	if code != 2 {
		t.Errorf("exit=%d, want 2", code)
	}
	if !strings.Contains(out, `"server_down"`) {
		t.Errorf("출력 %q", out)
	}
}

// TestReportRegistered — registry에 report가 있어야 Run이 디스패치한다.
func TestReportRegistered(t *testing.T) {
	if _, ok := Lookup("report"); !ok {
		t.Fatal("report 미등록")
	}
}

// TestCellToIntUnifiedCoercion — 셀→int 단일 유틸(cellToInt) 경계 회귀:
// 문자열 숫자·float·nil·비수. rowIDs(구 구현은 문자열 id 미수용)·
// readCounters(Sscan 별도 구현)가 같은 규약을 공유한다(3중 구현 방지).
func TestCellToIntUnifiedCoercion(t *testing.T) {
	cases := []struct {
		v    any
		want int
		ok   bool
	}{
		{int(7), 7, true},
		{int32(3), 3, true},
		{int64(9), 9, true},
		{float64(12.0), 12, true},
		{"5", 5, true},
		{nil, 0, false},
		{"abc", 0, false},
		{true, 0, false},
	}
	for _, c := range cases {
		got, ok := cellToInt(c.v)
		if got != c.want || ok != c.ok {
			t.Errorf("cellToInt(%#v) = (%d,%v), want (%d,%v)", c.v, got, ok, c.want, c.ok)
		}
	}
	// rowIDs — 문자열 숫자 id도 수용, nil·비수는 조용히 건너뛴다.
	if ids := rowIDs([][]any{{int32(3)}, {"9"}, {nil}, {7.0}, {"x"}, {}}); len(ids) != 3 ||
		ids[0] != 3 || ids[1] != 9 || ids[2] != 7 {
		t.Errorf("rowIDs = %v, want [3 9 7]", ids)
	}
	// readCounters — 문자열·int64·float·nil 혼합 행(불가칸은 0 유지).
	var a, b, c, d int
	readCounters([]any{"5", int64(6), 7.0, nil}, &a, &b, &c, &d)
	if a != 5 || b != 6 || c != 7 || d != 0 {
		t.Errorf("readCounters = %d,%d,%d,%d, want 5,6,7,0", a, b, c, d)
	}
}

// TestReportSQLDoltgresCompat — 회귀: report SQL은 Doltgres에서 실행돼야
// 한다. FILTER 집계 금지(pattern.go 관례)·SQL 내 interval 산술 전면 금지
// (make_interval·리터럴·$1 곱 모두 — T70 e2e: ($1 * interval)은 Doltgres가
// $1을 interval로 추론해 pgx 인코딩 실패). 컷오프는 Go 계산 timestamptz
// 파라미터($1)로만 스캔에 전달한다.
func TestReportSQLDoltgresCompat(t *testing.T) {
	type q struct {
		sql  string
		args []any
	}
	got := []q{}
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		got = append(got, q{sql, args})
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
	if _, code := runReportArgs(t, f); code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if len(got) < 4 {
		t.Fatalf("쿼리 %d건, want ≥4", len(got))
	}
	sawStale, sawPeriod := false, false
	for _, q := range got {
		if strings.Contains(q.sql, "FILTER (") || strings.Contains(q.sql, "FILTER(") {
			t.Errorf("FILTER 집계 금지(Doltgres 비호환): %q", q.sql)
		}
		if strings.Contains(q.sql, "make_interval") || strings.Contains(q.sql, "interval") {
			t.Errorf("SQL 내 interval 산술 금지(T70: Doltgres pgx 추론 결함): %q", q.sql)
		}
		if strings.Contains(q.sql, "read_count") {
			sawStale = true
			assertCutoffArg(t, "stale", q.args, 90*24*time.Hour)
		}
		if strings.Contains(q.sql, "invalid_at >=") {
			sawPeriod = true
			assertCutoffArg(t, "기간", q.args, 7*24*time.Hour) // 기본 --days
		}
	}
	if !sawStale || !sawPeriod {
		t.Errorf("stale(%v)·기간(%v) 쿼리 미관측", sawStale, sawPeriod)
	}
}

// assertCutoffArg — 컷오프 인자는 timestamptz(time.Time) 1개이며 now-backoff
// (±5분 허용, 실행 지연 여유)임을 단언. int 인자였다면 타입 단언이 실패한다.
func assertCutoffArg(t *testing.T, name string, args []any, backoff time.Duration) {
	t.Helper()
	if len(args) != 1 {
		t.Errorf("%s 쿼리 인자 %d건, want 1(timestamptz 컷오프): %v", name, len(args), args)
		return
	}
	cut, ok := args[0].(time.Time)
	if !ok {
		t.Errorf("%s 쿼리 인자 타입 %T, want time.Time(timestamptz 컷오프)", name, args[0])
		return
	}
	want := time.Now().Add(-backoff)
	if d := cut.Sub(want); d < -5*time.Minute || d > 5*time.Minute {
		t.Errorf("%s 컷오프=%v, want %v(±5분)", name, cut, want)
	}
}
