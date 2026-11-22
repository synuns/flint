// metrics_test.go — hotPersisted offset consistency when hotLens is
// truncated at the cap. When a truncation shaves the front of persisted,
// the offset must shrink with it — otherwise the unsaved (pending)
// samples lose exactly the truncated amount and calls/p95 are under-reported.
package ops

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestHotLensTruncateConsistency — lens/persisted consistency in a session exceeding the cap.
// Step 1: after persist, additional records exceed the cap and truncate — all pending kept.
func TestHotLensTruncateConsistency(t *testing.T) {
	ResetMetrics()
	// round 1: record 400 samples then persist (all moved to the baseline).
	for i := 0; i < 400; i++ {
		RecordHotLen(10)
	}
	if err := PersistHotStats(func(_, _ string) error { return nil }); err != nil {
		t.Fatalf("persist(1) 오류: %v", err)
	}
	// step 2: simulate a long-running session — 200 more samples, 600 total ->
	// the front 100 (the persisted part) truncated -> 500 kept = persisted 300 + pending 200.
	for i := 0; i < 200; i++ {
		RecordHotLen(20)
	}
	calls, p95, max := HotStats()
	// baseline (400) + pending (200) = 600 — the pending must not shrink from the truncation.
	if calls != 600 {
		t.Fatalf("calls=%d, want 600(pending 200 전량 반영)", calls)
	}
	// sorted samples: 10x400 + 20x200 -> p95 index ceil(0.95*600)=570 -> 20.
	if p95 != 20 {
		t.Fatalf("p95=%d, want 20", p95)
	}
	if max != 20 {
		t.Fatalf("max=%d, want 20", max)
	}
	// step 3: re-persist — the cumulative must also stay 600 (no under-summing).
	stored := ""
	if err := PersistHotStats(func(_, v string) error { stored = v; return nil }); err != nil {
		t.Fatalf("persist(2) 오류: %v", err)
	}
	ResetMetrics() // simulate a new process
	gotCalls, _, ok := LoadHotStats(func(string) (string, error) { return stored, nil })
	if !ok || gotCalls != 600 {
		t.Fatalf("왕복 calls=%d ok=%v, want 600/true", gotCalls, ok)
	}
}

// TestHotLensTruncatePastPersisted — when the truncation crosses the persisted
// boundary entirely: if the whole truncated portion is unsaved samples, the offset bottoms out at 0.
func TestHotLensTruncatePastPersisted(t *testing.T) {
	ResetMetrics()
	for i := 0; i < 10; i++ {
		RecordHotLen(5)
	}
	if err := PersistHotStats(func(_, _ string) error { return nil }); err != nil {
		t.Fatalf("persist 오류: %v", err)
	}
	// 610 more -> 620 total -> the front 120 truncated (including 10 persisted) ->
	// the 500 kept are all unsaved -> pending = 500.
	for i := 0; i < 610; i++ {
		RecordHotLen(7)
	}
	calls, _, _ := HotStats()
	if want := 10 + 500; calls != want {
		t.Fatalf("calls=%d, want %d(baseline 10 + pending 500)", calls, want)
	}
}

// TestScanStatsSamplesAndP95 — v1.21.0 A6: per-kind sample accumulation and
// the p95 nearest-rank ceil(0.95n)-1 (the HotP95 convention) at odd and even n.
// Durations are exact microsecond ints so the rank arithmetic is hand-computable:
// odd n=21 (1..21us) -> idx ceil(19.95)=20 -> sorted[19]=20; even n=20 (1..20us)
// -> idx ceil(19)=19 -> sorted[18]=19.
func TestScanStatsSamplesAndP95(t *testing.T) {
	ResetMetrics()
	for i := 1; i <= 21; i++ {
		RecordEchoScan(time.Duration(i) * time.Microsecond)
	}
	for i := 1; i <= 20; i++ {
		RecordSuggestScan(time.Duration(i) * time.Microsecond)
	}
	echo, sug := ScanStats()
	if echo.Calls != 21 || echo.P95 != 20 {
		t.Fatalf("echo = {calls %d, p95 %d}, want {21, 20}(홀수 n 최근접 순위)", echo.Calls, echo.P95)
	}
	if sug.Calls != 20 || sug.P95 != 19 {
		t.Fatalf("suggest = {calls %d, p95 %d}, want {20, 19}(짝수 n 최근접 순위)", sug.Calls, sug.P95)
	}
}

// TestScanStatsCapAndRoundtrip — the sample cap keeps the most recent 500 per
// kind while cumulative calls survive the truncation (the TestHotLensTruncateConsistency
// convention), and the meta scan_stats roundtrip preserves calls+p95 across processes.
func TestScanStatsCapAndRoundtrip(t *testing.T) {
	ResetMetrics()
	// round 1: 400 samples of 10us, persist (all move to the baseline).
	for i := 0; i < 400; i++ {
		RecordEchoScan(10 * time.Microsecond)
	}
	if err := PersistScanStats(func(_, _ string) error { return nil }); err != nil {
		t.Fatalf("persist(1) 오류: %v", err)
	}
	// round 2: 200 more of 20us -> 600 kept-sample candidates, front 100 truncated
	// -> kept = 300 persisted + 200 pending; calls = baseline 400 + pending 200 = 600.
	for i := 0; i < 200; i++ {
		RecordEchoScan(20 * time.Microsecond)
	}
	echo, _ := ScanStats()
	if echo.Calls != 600 {
		t.Fatalf("calls=%d, want 600(트렁케이션에도 pending 전량 반영)", echo.Calls)
	}
	// sorted 10us x400 + 20us x200 -> p95 idx ceil(570)=570 -> 20us.
	if echo.P95 != 20 {
		t.Fatalf("p95=%d, want 20", echo.P95)
	}
	// roundtrip: persist -> new process -> load.
	storedKey, stored := "", ""
	if err := PersistScanStats(func(k, v string) error { storedKey, stored = k, v; return nil }); err != nil {
		t.Fatalf("persist(2) 오류: %v", err)
	}
	if storedKey != "scan_stats" {
		t.Fatalf("meta 키 = %q, want scan_stats", storedKey)
	}
	var shape map[string]any
	if json.Unmarshal([]byte(stored), &shape) != nil {
		t.Fatalf("scan_stats JSON 파싱 실패: %q", stored)
	}
	if _, ok := shape["echo"]; !ok {
		t.Fatalf("scan_stats JSON에 echo 키 부재: %q", stored)
	}
	if _, ok := shape["suggest"]; !ok {
		t.Fatalf("scan_stats JSON에 suggest 키 부재: %q", stored)
	}
	ResetMetrics() // simulate a new process
	if ok := LoadScanStats(func(string) (string, error) { return stored, nil }); !ok {
		t.Fatalf("load ok=false, want true")
	}
	echo, _ = ScanStats()
	if echo.Calls != 600 || echo.P95 != 20 {
		t.Fatalf("왕복 echo = {calls %d, p95 %d}, want {600, 20}", echo.Calls, echo.P95)
	}
}

// TestScanStatsQuietOnFailure — corrupt JSON falls back to defaults (ok=false),
// a getKV error resets the baseline, and a setKV failure is quiet (returned,
// never panicking — §13 metering must not change the outcome).
func TestScanStatsQuietOnFailure(t *testing.T) {
	ResetMetrics()
	if ok := LoadScanStats(func(string) (string, error) { return "{not json", nil }); ok {
		t.Fatalf("corrupt JSON ok=true, want false(기본 폴백)")
	}
	if ok := LoadScanStats(func(string) (string, error) { return "", errors.New("boom") }); ok {
		t.Fatalf("getKV 오류 ok=true, want false")
	}
	if err := PersistScanStats(func(_, _ string) error { return errors.New("boom") }); err == nil {
		t.Fatalf("setKV 오류 시 nil, want err 전파(호출자 조용 처리)")
	}
	// persistScanStatsToMeta swallows the failure — no panic, baseline stays usable.
	RecordEchoScan(time.Microsecond)
	persistScanStatsToMeta(func(string) (string, error) { return "", nil },
		func(_, _ string) error { return errors.New("boom") })
	echo, _ := ScanStats()
	if echo.Calls != 1 {
		t.Fatalf("calls=%d, want 1(실패 후에도 in-memory 유지)", echo.Calls)
	}
}

// TestScanStatsLazyBaselineLoadOnce — persistScanStatsToMeta loads the stored
// baseline exactly once (the first persist point), then keeps its own baseline —
// a later external change to meta does not re-enter the in-process accounting.
func TestScanStatsLazyBaselineLoadOnce(t *testing.T) {
	ResetMetrics()
	calls := 0
	getKV := func(string) (string, error) {
		calls++
		return `{"echo":{"calls":7,"samples":[10]},"suggest":{"calls":0}}`, nil
	}
	setKV := func(_, _ string) error { return nil }
	persistScanStatsToMeta(getKV, setKV)
	persistScanStatsToMeta(getKV, setKV)
	if calls != 1 {
		t.Fatalf("getKV 호출 %d회, want 1(최초 저장 시점 1회 지연 로드)", calls)
	}
	echo, _ := ScanStats()
	if echo.Calls != 7 {
		t.Fatalf("calls=%d, want 7(저장 시점 베이스라인 합산)", echo.Calls)
	}
}
