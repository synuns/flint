package cli

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// resetMetrics — 프로세스 내 통계 초기화(테스트 격리). 영속 baseline도
// 함께 지운다(프로세스 분리 시뮬레이션의 전제).
func resetMetrics() {
	hotMu.Lock()
	defer hotMu.Unlock()
	hotLens = hotLens[:0]
	hotBase = hotStatsJSON{}
	hotPersisted = 0
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
	if want := len([]rune(strings.TrimRight(out, "\n"))); hotLens[0] != want {
		t.Fatalf("기록 글자수=%d, want %d(출력 rune 수)", hotLens[0], want)
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

// readStatsMeta — meta read_stats를 임시 저장하는 get/set fake 쌍.
func readStatsMeta(meta map[string]string) (func(string) (string, error), func(string, string) error) {
	get := func(k string) (string, error) { return meta[k], nil }
	set := func(k, v string) error { meta[k] = v; return nil }
	return get, set
}

// TestReadStatsAccumulate — BumpReadStats 2회 누적: calls 2·zero 2(T56).
func TestReadStatsAccumulate(t *testing.T) {
	meta := map[string]string{}
	get, set := readStatsMeta(meta)
	if err := BumpReadStats(true, get, set); err != nil {
		t.Fatalf("bump(1) 오류: %v", err)
	}
	if err := BumpReadStats(true, get, set); err != nil {
		t.Fatalf("bump(2) 오류: %v", err)
	}
	calls, zero, ok := LoadReadStats(get)
	if !ok || calls != 2 || zero != 2 {
		t.Fatalf("누적=%d/%d ok=%v, want 2/2/true; meta=%v", calls, zero, ok, meta)
	}
}

// TestReadStatsMixed — zeroHits=false는 calls만 증가.
func TestReadStatsMixed(t *testing.T) {
	meta := map[string]string{}
	get, set := readStatsMeta(meta)
	if err := BumpReadStats(false, get, set); err != nil {
		t.Fatalf("bump(false) 오류: %v", err)
	}
	if err := BumpReadStats(true, get, set); err != nil {
		t.Fatalf("bump(true) 오류: %v", err)
	}
	calls, zero, _ := LoadReadStats(get)
	if calls != 2 || zero != 1 {
		t.Fatalf("누적=%d/%d, want 2/1", calls, zero)
	}
}

// TestReadStatsCorruptFallback — 파손된 read_stats는 기본값 폴백 후 재누적.
func TestReadStatsCorruptFallback(t *testing.T) {
	meta := map[string]string{"read_stats": "{not-json"}
	get, set := readStatsMeta(meta)
	if err := BumpReadStats(true, get, set); err != nil {
		t.Fatalf("파손 상태 bump 오류: %v", err)
	}
	if meta["read_stats"] != `{"calls":1,"zero":1}` {
		t.Fatalf("폴백 후 저장값=%q, want {\"calls\":1,\"zero\":1}", meta["read_stats"])
	}
	if calls, zero, ok := LoadReadStats(get); !ok || calls != 1 || zero != 1 {
		t.Fatalf("폴백 후 로드=%d/%d/%v, want 1/1/true", calls, zero, ok)
	}
}

// TestReadStatsMissingFallback — 키 부재·getKV 오류는 0에서 시작(폴백).
func TestReadStatsMissingFallback(t *testing.T) {
	if calls, zero, ok := LoadReadStats(func(string) (string, error) { return "", nil }); ok || calls != 0 || zero != 0 {
		t.Fatalf("부재 로드=%d/%d/%v, want 0/0/false", calls, zero, ok)
	}
	if calls, zero, ok := LoadReadStats(func(string) (string, error) { return "", context.DeadlineExceeded }); ok || calls != 0 || zero != 0 {
		t.Fatalf("오류 로드=%d/%d/%v, want 0/0/false", calls, zero, ok)
	}
}

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
func TestStatusIncludesReadStats(t *testing.T) {
	resetMetrics()
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case len(args) == 1 && args[0] == "read_stats":
			return [][]any{{`{"calls":4,"zero":1}`}}, nil
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"1"}}, nil // schema_version
		}
		return [][]any{{int64(0)}}, nil // pending
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

// TestReadWiringBumpsReadStats — read 실행(0행 = zero hit)이 meta read_stats 누적.
func TestReadWiringBumpsReadStats(t *testing.T) {
	meta := map[string]string{}
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "INSERT INTO meta"):
			meta[args[0].(string)] = fmt.Sprint(args[1])
			return nil, nil
		case len(args) == 1 && args[0] == "read_stats":
			if v, ok := meta["read_stats"]; ok {
				return [][]any{{v}}, nil
			}
			return nil, nil
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"2"}}, nil
		}
		return nil, nil // active_knowledge 조회 → 0행
	}
	withFake(t, f)
	if out, code := mustRun(t, []string{"read", "flint"}, ""); code != 0 {
		t.Fatalf("read exit=%d, want 0; out=%s", code, out)
	}
	if meta["read_stats"] != `{"calls":1,"zero":1}` {
		t.Fatalf("read_stats=%q, want {\"calls\":1,\"zero\":1}(zero hit 1회)", meta["read_stats"])
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
