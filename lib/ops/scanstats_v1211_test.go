package ops

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestScanStatsSanitizeOnLoad(t *testing.T) {
	ResetMetrics()
	blob := `{"echo":{"calls":3,"samples":[-5000,100,200]},"suggest":{"calls":-3,"samples":[-1,50]}}`
	echo, suggest := ScanStatsFromMeta(func(string) (string, error) { return blob, nil })
	if echo.Calls != 3 || echo.P95 != 200 {
		t.Fatalf("echo={%d,%d}, want {3,200} (음수 샘플 드롭)", echo.Calls, echo.P95)
	}

	if suggest.Calls != 0 || suggest.P95 != 0 {
		t.Fatalf("suggest={%d,%d}, want {0,0} (음수 calls 클램프)", suggest.Calls, suggest.P95)
	}
}

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

func TestPersistScanStatsToMetaQuietUnderConflict(t *testing.T) {
	prevBackoff := scanFlushBackoff
	scanFlushBackoff = []time.Duration{0, 0, 0}
	t.Cleanup(func() { scanFlushBackoff = prevBackoff })

	ResetMetrics()
	conflict := errors.New("serialization failure: this transaction conflicts with a committed transaction (SQLSTATE 40001)")
	inserts := 0
	var setVal string
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "INSERT INTO meta") {
			inserts++
			if inserts <= 2 {
				return nil, conflict
			}
			setVal = args[1].(string)
			return nil, nil
		}
		return nil, nil
	}}
	RecordSuggestScan(900 * time.Microsecond)
	PersistScanStatsToMeta(context.Background(), st)
	if inserts < 3 || !strings.Contains(setVal, "900") {
		t.Fatalf("40001 재시도 후 미착신: inserts=%d val=%q", inserts, setVal)
	}

	ResetMetrics()
	inserts = 0
	st2 := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "INSERT INTO meta") {
			inserts++
			return nil, conflict
		}
		return nil, nil
	}}
	RecordEchoScan(time.Millisecond)
	PersistScanStatsToMeta(context.Background(), st2)

	if inserts != 8 {
		t.Fatalf("재시도 횟수=%d, want 8 (4 플러시 시도 x QueryOnce 즉시 재수행 — 최종 조용한 포기)", inserts)
	}
}

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

	idx := strings.Index(line, `"vault_changed":`)
	for _, k := range []string{"scan_echo_calls", "scan_echo_p95_us", "scan_suggest_calls", "scan_suggest_p95_us"} {
		i := strings.Index(line, `"`+k+`":`)
		if i < 0 || i < idx {
			t.Fatalf("키 %q이 vault 행 앞/부재 (i=%d vault=%d): %s", k, i, idx, line)
		}
	}
}
