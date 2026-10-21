package cli

import (
	"strings"
	"testing"
)

// resetMetrics — 프로세스 내 통계 초기화(테스트 격리).
func resetMetrics() {
	hotLens = hotLens[:0]
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
