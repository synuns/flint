package cli

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/synuns/flint/lib/ops"
)

// resetMetrics — resets in-process stats (test isolation). Also clears the persistent baseline
// (a precondition for simulating separate processes).
func resetMetrics() {
	ops.ResetMetrics()
}

// TestHotConcurrentAccess — no races during parallel record and read (-race regression).
func TestHotConcurrentAccess(t *testing.T) {
	resetMetrics()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				RecordHotLen(i)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			HotStats()
		}
	}()
	wg.Wait()
	if calls, _, _ := HotStats(); calls != 400 {
		t.Fatalf("calls=%d, want 400(전체 기록 유실 없음)", calls)
	}
}

// TestHotStatsEmpty — the 0-count boundary: all zeros.
func TestHotStatsEmpty(t *testing.T) {
	resetMetrics()
	calls, p95, max := HotStats()
	if calls != 0 || p95 != 0 || max != 0 {
		t.Fatalf("빈 통계=%d/%d/%d, want 0/0/0", calls, p95, max)
	}
}

// TestHotStatsSingle — the 1-count boundary: p95 and max are both that value.
func TestHotStatsSingle(t *testing.T) {
	resetMetrics()
	RecordHotLen(120)
	calls, p95, max := HotStats()
	if calls != 1 || p95 != 120 || max != 120 {
		t.Fatalf("1건 통계=%d/%d/%d, want 1/120/120", calls, p95, max)
	}
}

// TestHotStatsCap500 — the 500-sample cap: old samples are dropped, the latest 500 kept.
func TestHotStatsCap500(t *testing.T) {
	resetMetrics()
	for i := 1; i <= 600; i++ {
		RecordHotLen(i)
	}
	calls, p95, max := HotStats()
	if calls != 500 {
		t.Fatalf("calls=%d, want 500(상한)", calls)
	}
	if max != 600 {
		t.Fatalf("max=%d, want 600(최근 샘플 중 최대)", max)
	}
	// kept samples 101..600; the approximate p95 index = ceil(0.95*500)-1 = 474 -> 575.
	if p95 != 575 {
		t.Fatalf("p95=%d, want 575", p95)
	}
}

// TestHotRecordsRuneLen — after hot output, the rune count is recorded.
func TestHotRecordsRuneLen(t *testing.T) {
	resetMetrics()
	f := &fakeStore{rows: [][]any{{"decision", strings.Repeat("가", 3000), "s", "ts"}}}
	withFake(t, f)
	out, code := mustRun(t, []string{"hot", "--session", "S1"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	calls, _, _ := HotStats()
	if calls != 1 {
		t.Fatalf("hot 실행 후 calls=%d, want 1", calls)
	}
	// when truncated, the rune count of the output as-is (including the "…[truncated]" suffix) must be recorded.
	s := ops.HotLensSnapshot()
	if want := len([]rune(strings.TrimRight(out, "\n"))); len(s) == 0 || s[0] != want {
		t.Fatalf("기록 글자수=%v, want %d(출력 rune 수)", s, want)
	}
}

// TestHotStatsPersistRoundtrip — persist->load cumulative round trip: values preserved across one-shot CLI runs.
func TestHotStatsPersistRoundtrip(t *testing.T) {
	resetMetrics()
	// baseline initialization (no previous run)
	if _, _, ok := LoadHotStats(func(string) (string, error) { return "", nil }); ok {
		t.Fatalf("빈 meta 로드 ok=true, want false")
	}
	RecordHotLen(10)
	RecordHotLen(20)
	stored := ""
	if err := PersistHotStats(func(k, v string) error { stored = v; return nil }); err != nil {
		t.Fatalf("persist 오류: %v", err)
	}
	// simulate a new process: clear memory and load
	resetMetrics()
	calls, p95, ok := LoadHotStats(func(string) (string, error) { return stored, nil })
	if !ok || calls != 2 {
		t.Fatalf("로드 calls=%d ok=%v, want 2/true", calls, ok)
	}
	if p95 != 20 {
		t.Fatalf("로드 p95=%d, want 20", p95)
	}
	// subsequent runs: values increase via cumulative summing
	RecordHotLen(30)
	if err := PersistHotStats(func(k, v string) error { stored = v; return nil }); err != nil {
		t.Fatalf("persist(2회차) 오류: %v", err)
	}
	calls2, _, ok2 := LoadHotStats(func(string) (string, error) { return stored, nil })
	if !ok2 || calls2 != 3 {
		t.Fatalf("누적 calls=%d ok=%v, want 3/true(증가)", calls2, ok2)
	}
}

// TestHotStatsLoadCorruptFallback — corrupt hot_stats falls back to defaults.
func TestHotStatsLoadCorruptFallback(t *testing.T) {
	resetMetrics()
	calls, p95, ok := LoadHotStats(func(string) (string, error) { return "{not-json", nil })
	if ok || calls != 0 || p95 != 0 {
		t.Fatalf("파손 로드=%d/%d/%v, want 0/0/false", calls, p95, ok)
	}
	// after the fallback, persist records only the current run, without the corrupt values
	RecordHotLen(7)
	stored := ""
	if err := PersistHotStats(func(k, v string) error { stored = v; return nil }); err != nil {
		t.Fatalf("persist 오류: %v", err)
	}
	calls2, _, ok2 := LoadHotStats(func(string) (string, error) { return stored, nil })
	if !ok2 || calls2 != 1 {
		t.Fatalf("폴백 후 calls=%d ok=%v, want 1/true", calls2, ok2)
	}
}

// TestStatusIncludesHotStats — the status one-line JSON exposes hot_calls and hot_p95.
func TestStatusIncludesHotStats(t *testing.T) {
	resetMetrics()
	RecordHotLen(42)
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{"1"}}, nil
		}
		return [][]any{{int64(0)}}, nil
	}
	withFake(t, f)
	out, code := mustRun(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["hot_calls"] != float64(1) || m["hot_p95"] != float64(42) {
		t.Fatalf("hot 통계 노출 없음: %v", m)
	}
}

// TestReadZeroPct — ratios: 0 if calls is 0, else the integer percentage.
func TestReadZeroPct(t *testing.T) {
	cases := []struct {
		calls, zero, want int
	}{
		{0, 0, 0},
		{4, 1, 25},
		{4, 0, 0},
		{3, 2, 66},
		{3, 3, 100},
	}
	for _, c := range cases {
		if got := readZeroPct(c.calls, c.zero); got != c.want {
			t.Errorf("readZeroPct(%d,%d)=%d, want %d", c.calls, c.zero, got, c.want)
		}
	}
}

// TestStatusIncludesReadStats — the status one-line JSON exposes read_calls and read_zero_pct.
// read_calls comes from the read_events-derived tally
// (ops.LoadReadStatsFromMeta).
func TestStatusIncludesReadStats(t *testing.T) {
	resetMetrics()
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM read_events"):
			return [][]any{{int64(4), int64(1)}}, nil // derived cumulative calls 4, zero 1
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"1"}}, nil // schema_version
		}
		return [][]any{{int64(0)}}, nil // pending and others
	}
	withFake(t, f)
	out, code := mustRun(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["read_calls"] != float64(4) || m["read_zero_pct"] != float64(25) {
		t.Fatalf("read 통계 노출 오류: read_calls=%v read_zero_pct=%v", m["read_calls"], m["read_zero_pct"])
	}
}

// TestReadWritesNoReadStats — running read (zero hit) does not write the meta
// read_stats (the metering source is read_events).
func TestReadWritesNoReadStats(t *testing.T) {
	meta := map[string]string{}
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "INSERT INTO meta"):
			meta[fmt.Sprint(args[0])] = fmt.Sprint(args[1])
			return nil, nil
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"2"}}, nil
		}
		return nil, nil // active_knowledge query -> 0 rows
	}
	withFake(t, f)
	if out, code := mustRun(t, []string{"read", "flint"}, ""); code != 0 {
		t.Fatalf("read exit=%d, want 0(제로히트 exit 0 — v1.5.3); out=%s", code, out)
	}
	if _, ok := meta["read_stats"]; ok {
		t.Fatalf("read_stats=%q 기록 — blob 쓰기는 폐지돼야 한다", meta["read_stats"])
	}
}

// TestHotStatsWiringAccumulates — the hot-path wiring: load from meta at start, persist just before
// exit — accumulation across separate processes (fresh memory) shows up in status.
func TestHotStatsWiringAccumulates(t *testing.T) {
	meta := map[string]string{}
	f := &fakeStore{}
	f.rows = [][]any{{"decision", "요약", "active", "2026-10-22"}} // hot_view용
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "INSERT INTO meta"):
			meta[args[0].(string)] = fmt.Sprint(args[1])
			return nil, nil
		case strings.Contains(sql, "FROM meta"):
			if len(args) == 1 {
				if v, ok := meta[fmt.Sprint(args[0])]; ok {
					return [][]any{{v}}, nil
				}
				return nil, nil
			}
			return [][]any{{"2"}}, nil // schema_version
		}
		return f.rows, nil
	}
	withFake(t, f)

	// first run -> status hot_calls 1
	resetMetrics()
	if out, code := mustRun(t, []string{"hot", "--session", "S1"}, ""); code != 0 {
		t.Fatalf("hot(1) exit=%d; out=%s", code, out)
	}
	out, code := mustRun(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("status(1) exit=%d; out=%s", code, out)
	}
	if m := decode(t, out); m["hot_calls"] != float64(1) {
		t.Fatalf("1회차 hot_calls=%v, want 1; out=%s", m["hot_calls"], out)
	}

	// simulate a separate process (memory cleared, meta kept) -> cumulative 2 (increased)
	resetMetrics()
	if out, code := mustRun(t, []string{"hot", "--session", "S1"}, ""); code != 0 {
		t.Fatalf("hot(2) exit=%d; out=%s", code, out)
	}
	out, code = mustRun(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("status(2) exit=%d; out=%s", code, out)
	}
	if m := decode(t, out); m["hot_calls"] != float64(2) {
		t.Fatalf("2회차 hot_calls=%v, want 2(누적 증가); out=%s", m["hot_calls"], out)
	}
}

// TestStatusLoadsHotStatsFromMeta — separate-process simulation (memory empty):
// status loads the meta hot_stats baseline and reports the previous run's cumulative.
func TestStatusLoadsHotStatsFromMeta(t *testing.T) {
	resetMetrics()
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case len(args) == 1 && args[0] == "hot_stats":
			return [][]any{{`{"calls":5,"sum":150,"lens":[10,20,30,40,50]}`}}, nil
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"2"}}, nil // schema_version
		}
		return [][]any{{int64(0)}}, nil // pending
	}
	withFake(t, f)
	out, code := mustRun(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["hot_calls"] != float64(5) || m["hot_p95"] != float64(50) {
		t.Fatalf("meta baseline 미로드: hot_calls=%v hot_p95=%v, want 5/50", m["hot_calls"], m["hot_p95"])
	}
}
