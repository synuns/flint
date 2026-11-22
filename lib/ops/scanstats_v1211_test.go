// scanstats_v1211_test.go — v1.21.1 A6 completion (r91/r93/r94): the
// load-time sanitize (negative calls clamp, negative samples drop), the
// write/read exit flush (PersistScanStatsToMeta over a fake meta store),
// and the status scan row (keys appended after the vault row).
package ops

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestScanStatsSanitizeOnLoad — a corrupted meta scan_stats payload
// (negative calls, negative samples) loads clamped: the p95 floor cannot
// be poisoned; a fully negative kind reads as quiet zeros.
func TestScanStatsSanitizeOnLoad(t *testing.T) {
	ResetMetrics()
	blob := `{"echo":{"calls":3,"samples":[-5000,100,200]},"suggest":{"calls":-3,"samples":[-1,50]}}`
	echo, suggest := ScanStatsFromMeta(func(string) (string, error) { return blob, nil })
	if echo.Calls != 3 || echo.P95 != 200 {
		t.Fatalf("echo={%d,%d}, want {3,200} (음수 샘플 드롭)", echo.Calls, echo.P95)
	}
	// suggest: calls clamped 0, the negative sample dropped -> the quiet-zero rule.
	if suggest.Calls != 0 || suggest.P95 != 0 {
		t.Fatalf("suggest={%d,%d}, want {0,0} (음수 calls 클램프)", suggest.Calls, suggest.P95)
	}
}

// TestPersistScanStatsToMetaFlushesDirtySamples — a recorded sample makes
// the flush persist meta scan_stats over the store (the write/read exit
// path; the one-shot process otherwise loses its samples).
func TestPersistScanStatsToMetaFlushesDirtySamples(t *testing.T) {
	ResetMetrics()
	var setKey, setVal string
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "INSERT INTO meta") {
			setKey, setVal = args[0].(string), args[1].(string)
			return nil, nil
		}
		return nil, nil
	}}
	RecordEchoScan(1500 * time.Microsecond)
	PersistScanStatsToMeta(context.Background(), st)
	if setKey != "scan_stats" {
		t.Fatalf("persist 키=%q, want scan_stats (더티 샘플 플러시)", setKey)
	}
	if !strings.Contains(setVal, `"echo"`) || !strings.Contains(setVal, "1500") {
		t.Fatalf("scan_stats 페이로드 불완전: %q", setVal)
	}
}

// TestStatusScanRowAppended — the status scan metering row surfaces the
// meta scan_stats cumulative (calls + p95 microseconds per kind),
// appended AFTER the vault row in the key order.
func TestStatusScanRowAppended(t *testing.T) {
	ResetMetrics()
	blob := `{"echo":{"calls":4,"samples":[100,900]},"suggest":{"calls":2,"samples":[50]}}`
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key = 'schema_version'") {
			return [][]any{{"16"}}, nil
		}
		if strings.Contains(sql, "FROM meta WHERE key = $1") && len(args) > 0 {
			if k, ok := args[0].(string); ok && k == "scan_stats" {
				return [][]any{{blob}}, nil
			}
		}
		return nil, nil
	}}
	code, line, m := runStatusJSON(t, st)
	if code != 0 {
		t.Fatalf("exit=%d — %s", code, line)
	}
	if m["scan_echo_calls"].(float64) != 4 || m["scan_echo_p95_us"].(float64) != 900 {
		t.Fatalf("echo 행=%v/%v, want 4/900", m["scan_echo_calls"], m["scan_echo_p95_us"])
	}
	if m["scan_suggest_calls"].(float64) != 2 || m["scan_suggest_p95_us"].(float64) != 50 {
		t.Fatalf("suggest 행=%v/%v, want 2/50", m["scan_suggest_calls"], m["scan_suggest_p95_us"])
	}
	// key order: after the vault row's tail (vault_changed).
	idx := strings.Index(line, `"vault_changed":`)
	for _, k := range []string{"scan_echo_calls", "scan_echo_p95_us", "scan_suggest_calls", "scan_suggest_p95_us"} {
		i := strings.Index(line, `"`+k+`":`)
		if i < 0 || i < idx {
			t.Fatalf("키 %q이 vault 행 앞/부재 (i=%d vault=%d): %s", k, i, idx, line)
		}
	}
}
