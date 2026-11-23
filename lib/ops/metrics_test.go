package ops

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestHotLensTruncateConsistency(t *testing.T) {
	ResetMetrics()

	for i := 0; i < 400; i++ {
		RecordHotLen(10)
	}
	if err := PersistHotStats(func(_, _ string) error { return nil }); err != nil {
		t.Fatalf("persist(1) 오류: %v", err)
	}

	for i := 0; i < 200; i++ {
		RecordHotLen(20)
	}
	calls, p95, max := HotStats()

	if calls != 600 {
		t.Fatalf("calls=%d, want 600(pending 200 전량 반영)", calls)
	}

	if p95 != 20 {
		t.Fatalf("p95=%d, want 20", p95)
	}
	if max != 20 {
		t.Fatalf("max=%d, want 20", max)
	}

	stored := ""
	if err := PersistHotStats(func(_, v string) error { stored = v; return nil }); err != nil {
		t.Fatalf("persist(2) 오류: %v", err)
	}
	ResetMetrics()
	gotCalls, _, ok := LoadHotStats(func(string) (string, error) { return stored, nil })
	if !ok || gotCalls != 600 {
		t.Fatalf("왕복 calls=%d ok=%v, want 600/true", gotCalls, ok)
	}
}

func TestHotLensTruncatePastPersisted(t *testing.T) {
	ResetMetrics()
	for i := 0; i < 10; i++ {
		RecordHotLen(5)
	}
	if err := PersistHotStats(func(_, _ string) error { return nil }); err != nil {
		t.Fatalf("persist 오류: %v", err)
	}

	for i := 0; i < 610; i++ {
		RecordHotLen(7)
	}
	calls, _, _ := HotStats()
	if want := 10 + 500; calls != want {
		t.Fatalf("calls=%d, want %d(baseline 10 + pending 500)", calls, want)
	}
}

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

func TestScanStatsCapAndRoundtrip(t *testing.T) {
	ResetMetrics()

	for i := 0; i < 400; i++ {
		RecordEchoScan(10 * time.Microsecond)
	}
	if err := PersistScanStats(func(_, _ string) error { return nil }); err != nil {
		t.Fatalf("persist(1) 오류: %v", err)
	}

	for i := 0; i < 200; i++ {
		RecordEchoScan(20 * time.Microsecond)
	}
	echo, _ := ScanStats()
	if echo.Calls != 600 {
		t.Fatalf("calls=%d, want 600(트렁케이션에도 pending 전량 반영)", echo.Calls)
	}

	if echo.P95 != 20 {
		t.Fatalf("p95=%d, want 20", echo.P95)
	}

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
	ResetMetrics()
	if ok := LoadScanStats(func(string) (string, error) { return stored, nil }); !ok {
		t.Fatalf("load ok=false, want true")
	}
	echo, _ = ScanStats()
	if echo.Calls != 600 || echo.P95 != 20 {
		t.Fatalf("왕복 echo = {calls %d, p95 %d}, want {600, 20}", echo.Calls, echo.P95)
	}
}

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

	RecordEchoScan(time.Microsecond)
	persistScanStatsToMeta(func(string) (string, error) { return "", nil },
		func(_, _ string) error { return errors.New("boom") })
	echo, _ := ScanStats()
	if echo.Calls != 1 {
		t.Fatalf("calls=%d, want 1(실패 후에도 in-memory 유지)", echo.Calls)
	}
}

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
