package cli

import (
	"strings"
	"sync"
	"testing"
)

// resetMetrics — 프로세스 내 통계 초기화(테스트 격리).
func resetMetrics() {
	hotMu.Lock()
	defer hotMu.Unlock()
	hotLens = hotLens[:0]
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
