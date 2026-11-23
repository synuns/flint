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
			return nil, nil
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

func flatTestThresholds() store.Thresholds {
	return store.Thresholds{RecallWarnWrites: 5}
}

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

func TestRecallStarvedReadEventsToday(t *testing.T) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	f := flatKeyFake(t,
		map[string]string{"daily:write:" + now.Format("2006-01-02"): "7"},
		[]time.Time{
			today.Add(1 * time.Hour),
			today.Add(23*time.Hour - time.Minute),
			today.AddDate(0, 0, 1).Add(time.Hour),
		}, nil)
	w, starved := recallStarvedToday(context.Background(), f, flatTestThresholds())
	if starved {
		t.Fatalf("(%d, %v), want starved=false — 당일 read 2행", w, starved)
	}
	if w != 7 {
		t.Fatalf("writes=%d, want 7(회상 있어도 기록 수는 그대로 반환)", w)
	}
}

func TestRecallStarvedCorruptValue(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	f := flatKeyFake(t, map[string]string{"daily:write:" + today: "abc"}, nil, nil)
	w, starved := recallStarvedToday(context.Background(), f, flatTestThresholds())
	if w != 0 || starved {
		t.Fatalf("(%d, %v), want (0, false) — 파손 플랫 값 조용히 0", w, starved)
	}
}

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
