// done_flatkey_test.go — the recallStarvedToday recording-source switch
// regression. Writes come from the flat key (daily:write:<date> int);
// reads from the count of same-day (UTC) read_events rows — the verdict
// path of the recall-warning death where counters froze permanently at 0
// after removing the legacy daily:<date> JSON fallback.
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

// flatKeyFake — the minimal fake answering meta flat keys·the same-day
// read_events COUNT (inheriting the fakeStore convention — via queryFn,
// unspecified queries are a no-op). readEvents mimics read_events ts
// rows: rows are filtered by the [lo, hi) query-argument bounds and
// counted exactly as SQL WHERE ts >= $1 AND ts < $2 would.
func flatKeyFake(t *testing.T, meta map[string]string, readEvents []time.Time, readErr error) *fakeStore {
	t.Helper()
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM meta"):
			if len(args) == 0 {
				return nil, nil
			}
			if v, ok := meta[fmt.Sprint(args[0])]; ok {
				return [][]any{{v}}, nil
			}
			return nil, nil // key absent — the GetMetaKVErr ("", false, nil) contract
		case strings.Contains(sql, "FROM read_events"):
			if readErr != nil {
				return nil, readErr
			}
			if len(args) < 2 {
				return nil, nil
			}
			lo, _ := args[0].(time.Time)
			hi, _ := args[1].(time.Time)
			n := 0
			for _, ts := range readEvents {
				if !ts.Before(lo) && ts.Before(hi) {
					n++
				}
			}
			return [][]any{{int64(n)}}, nil
		}
		return nil, nil
	}
	return f
}

// flatTestThresholds — the verdict threshold fixed (immune to default
// drift).
func flatTestThresholds() store.Thresholds {
	return store.Thresholds{RecallWarnWrites: 5}
}

// TestRecallStarvedFlatKey — daily:write:<today>=5 + 0 read_events rows
// → (5, true). Also confirms the legacy daily:<today> JSON key is ignored
// (if the fallback were read, the verdict would flip to write=99·read=9
// — the fallback-removal contract).
func TestRecallStarvedFlatKey(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	f := flatKeyFake(t, map[string]string{
		"daily:write:" + today: "5",
		"daily:" + today:       `{"write":99,"read":9}`,
	}, nil, nil)
	w, starved := recallStarvedToday(context.Background(), f, flatTestThresholds())
	if w != 5 || !starved {
		t.Fatalf("(%d, %v), want (5, true) — 플랫 키 직독·구 JSON 키 무시", w, starved)
	}
}

// TestRecallStarvedReadEventsToday — the read counter is the same-day
// (UTC) row count of read_events: 2 rows today + 1 tomorrow means read=2
// (tomorrow's row excluded) → recalls exist; writes are returned as-is
// without a warning.
func TestRecallStarvedReadEventsToday(t *testing.T) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	f := flatKeyFake(t,
		map[string]string{"daily:write:" + now.Format("2006-01-02"): "7"},
		[]time.Time{
			today.Add(1 * time.Hour),              // today
			today.Add(23*time.Hour - time.Minute), // today (just before midnight)
			today.AddDate(0, 0, 1).Add(time.Hour), // tomorrow — excluded
		}, nil)
	w, starved := recallStarvedToday(context.Background(), f, flatTestThresholds())
	if starved {
		t.Fatalf("(%d, %v), want starved=false — 당일 read 2행", w, starved)
	}
	if w != 7 {
		t.Fatalf("writes=%d, want 7(회상 있어도 기록 수는 그대로 반환)", w)
	}
}

// TestRecallStarvedCorruptValue — a corrupt (non-int) daily:write:<today>
// value is quietly 0 as before → (0, false).
func TestRecallStarvedCorruptValue(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	f := flatKeyFake(t, map[string]string{"daily:write:" + today: "abc"}, nil, nil)
	w, starved := recallStarvedToday(context.Background(), f, flatTestThresholds())
	if w != 0 || starved {
		t.Fatalf("(%d, %v), want (0, false) — 파손 플랫 값 조용히 0", w, starved)
	}
}

// TestRecallStarvedReadQueryError — a read_events lookup failure (an old
// schema without the table etc.) is quietly (0, false), not treated as
// read=0 (§13 exit-path invariance — a lookup error is a warning-free
// false).
func TestRecallStarvedReadQueryError(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	f := flatKeyFake(t,
		map[string]string{"daily:write:" + today: "5"},
		nil, errors.New(`relation "read_events" does not exist`))
	w, starved := recallStarvedToday(context.Background(), f, flatTestThresholds())
	if w != 0 || starved {
		t.Fatalf("(%d, %v), want (0, false) — 조회 실패는 경고 없이 종료", w, starved)
	}
}
