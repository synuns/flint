package cli

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/synuns/flint/lib/ops"
)

// resetMetrics — 프로세스 내 통계 초기화(테스트 격리). 영속 baseline도
// 함께 지운다(프로세스 분리 시뮬레이션의 전제). v0.40.0 T2: 계측 상태는
// lib/ops로 이동 — ops.ResetMetrics 위임.
func resetMetrics() {
	ops.ResetMetrics()
}

// TestHotConcurrentAccess — 병렬 기록·조회 중 레이스 없음(-race 회귀).
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

// TestHotStatsEmpty — 0건 경계: 모두 0.
func TestHotStatsEmpty(t *testing.T) {
	resetMetrics()
	calls, p95, max := HotStats()
	if calls != 0 || p95 != 0 || max != 0 {
		t.Fatalf("빈 통계=%d/%d/%d, want 0/0/0", calls, p95, max)
	}
}

// TestHotStatsSingle — 1건 경계: p95·max 모두 그 값.
func TestHotStatsSingle(t *testing.T) {
	resetMetrics()
	RecordHotLen(120)
	calls, p95, max := HotStats()
	if calls != 1 || p95 != 120 || max != 120 {
		t.Fatalf("1건 통계=%d/%d/%d, want 1/120/120", calls, p95, max)
	}
}

// TestHotStatsCap500 — 500건 상한: 오래된 샘플은 버리고 최근 500 보관.
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
	// 보관 샘플 101..600, p95 근사 인덱스 = ceil(0.95*500)-1 = 474 → 575.
	if p95 != 575 {
		t.Fatalf("p95=%d, want 575", p95)
	}
}

// TestHotRecordsRuneLen — hot 출력 후 글자수(rune)가 기록된다.
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
	// 절단 시 접미사("…[truncated]")를 포함한 출력 그대로의 rune 수가 기록되어야 한다.
	s := ops.HotLensSnapshot()
	if want := len([]rune(strings.TrimRight(out, "\n"))); len(s) == 0 || s[0] != want {
		t.Fatalf("기록 글자수=%v, want %d(출력 rune 수)", s, want)
	}
}

// TestHotStatsPersistRoundtrip — persist→load 누적 왕복: 단발 CLI 매 실행 값 보존.
func TestHotStatsPersistRoundtrip(t *testing.T) {
	resetMetrics()
	// baseline 초기화(직전 실행 없음)
	if _, _, ok := LoadHotStats(func(string) (string, error) { return "", nil }); ok {
		t.Fatalf("빈 meta 로드 ok=true, want false")
	}
	RecordHotLen(10)
	RecordHotLen(20)
	stored := ""
	if err := PersistHotStats(func(k, v string) error { stored = v; return nil }); err != nil {
		t.Fatalf("persist 오류: %v", err)
	}
	// 새 프로세스 시뮬레이션: 메모리 비우고 로드
	resetMetrics()
	calls, p95, ok := LoadHotStats(func(string) (string, error) { return stored, nil })
	if !ok || calls != 2 {
		t.Fatalf("로드 calls=%d ok=%v, want 2/true", calls, ok)
	}
	if p95 != 20 {
		t.Fatalf("로드 p95=%d, want 20", p95)
	}
	// 이어지는 실행: 누적 합산으로 값 증가
	RecordHotLen(30)
	if err := PersistHotStats(func(k, v string) error { stored = v; return nil }); err != nil {
		t.Fatalf("persist(2회차) 오류: %v", err)
	}
	calls2, _, ok2 := LoadHotStats(func(string) (string, error) { return stored, nil })
	if !ok2 || calls2 != 3 {
		t.Fatalf("누적 calls=%d ok=%v, want 3/true(증가)", calls2, ok2)
	}
}

// TestHotStatsLoadCorruptFallback — 파손된 hot_stats는 기본값 폴백.
func TestHotStatsLoadCorruptFallback(t *testing.T) {
	resetMetrics()
	calls, p95, ok := LoadHotStats(func(string) (string, error) { return "{not-json", nil })
	if ok || calls != 0 || p95 != 0 {
		t.Fatalf("파손 로드=%d/%d/%v, want 0/0/false", calls, p95, ok)
	}
	// 폴백 후 persist는 파손값 없이 현실행만 기록
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

// TestStatusIncludesHotStats — status 한 줄 JSON에 hot_calls·hot_p95 노출.
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

// (v0.42.0 T2 폐지 — TestReadStatsAccumulate·TestReadStatsMixed·
// TestReadStatsCorruptFallback·TestReadStatsMissingFallback: read_stats
// blob(BumpReadStats·LoadReadStats) 폐지. 누적·분류 계측은 read_events
// 파생 경로로 이전했고 동등성은 lib/ops/report_parity_test.go가 담당한다.)

// TestReadZeroPct — 비율: calls 0이면 0, 아니면 정수 백분율.
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

// TestStatusIncludesReadStats — status 한 줄 JSON에 read_calls·read_zero_pct 노출.
// v0.42.0 T2: read_stats blob 폐지 — read_calls는 read_events 파생 집계
// (ops.LoadReadStatsFromMeta)에서 나온다. 같은 픽스처 수치(calls 4·zero 1 →
// 25%)로 전환 전과 동일 노출을 담보한다.
func TestStatusIncludesReadStats(t *testing.T) {
	resetMetrics()
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM read_events"):
			return [][]any{{int64(4), int64(1)}}, nil // 파생 누적 calls 4·zero 1
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"1"}}, nil // schema_version
		}
		return [][]any{{int64(0)}}, nil // pending 등
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

// TestReadWritesNoReadStats — v0.42.0 T2: read 실행(제로히트)이 meta
// read_stats를 쓰지 않는다(쓰기 4→2 — 계측 원천은 read_events).
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
		return nil, nil // active_knowledge 조회 → 0행
	}
	withFake(t, f)
	if out, code := mustRun(t, []string{"read", "flint"}, ""); code != 1 {
		t.Fatalf("read exit=%d, want 1; out=%s", code, out)
	}
	if _, ok := meta["read_stats"]; ok {
		t.Fatalf("read_stats=%q 기록 — blob 쓰기는 폐지돼야 한다", meta["read_stats"])
	}
}

// TestHotStatsWiringAccumulates — hot 경로 wiring: 시작 시 meta 로드·
// 종료 직전 persist로 별도 프로세스 실행(메모리 초기화) 누적이 status에 보인다.
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

	// 1회차 실행 → status hot_calls 1
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

	// 별도 프로세스 시뮬레이션(메모리 초기화, meta는 유지) → 누적 2(증가)
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

// TestStatusLoadsHotStatsFromMeta — 프로세스 분리 시뮬레이션(메모리 비어 있음):
// status는 meta hot_stats baseline을 로드해 직전 실행 누적을 보고한다(F-T56-1).
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
