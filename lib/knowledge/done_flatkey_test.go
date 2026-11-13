// done_flatkey_test.go — v1.2.3 T1(R17 A1): recallStarvedToday 기록 원천
// 전환 회귀. write는 v1.2.0 T1 플랫 키(daily:write:<date> int), read는
// v0.42 read_events 당일(UTC) 행 수 — 구 daily:<date> JSON 폴백 제거로
// 카운터가 영구 0 동결되던 회상 경고 사망(A1)의 판정 경로.
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

// flatKeyFake — meta 플랫 키·read_events 당일 COUNT를 응답하는 최소 fake
// (fakeStore 관례 승계 — queryFn 경유, 미지정 쿼리는 영(零) 동작).
// readEvents는 read_events ts 행을 흉내 낸다: 조회 인자 [lo, hi) 경계로
// 행을 걸러 SQL WHERE ts >= $1 AND ts < $2와 동일하게 세어 돌려준다.
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
			return nil, nil // 키 부재 — GetMetaKVErr ("", false, nil) 계약
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

// flatTestThresholds — 판정 임계 고정(기본값 변동과 무결).
func flatTestThresholds() store.Thresholds {
	return store.Thresholds{RecallWarnWrites: 5}
}

// TestRecallStarvedFlatKey — daily:write:<오늘>=5 + read_events 0행 →
// (5, true). 구 daily:<오늘> JSON 키는 무시됨을 함께 확인(폴백이 읽히면
// write=99·read=9로 판정이 뒤집힌다 — 폴백 제거 계약).
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

// TestRecallStarvedReadEventsToday — read 카운터는 read_events 당일(UTC)
// 행 수: 오늘 2행 + 내일 1행이면 read=2(내일 행 제외) → 회상 있음,
// writes를 그대로 돌려주되 경고 없음.
func TestRecallStarvedReadEventsToday(t *testing.T) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	f := flatKeyFake(t,
		map[string]string{"daily:write:" + now.Format("2006-01-02"): "7"},
		[]time.Time{
			today.Add(1 * time.Hour),              // 오늘
			today.Add(23*time.Hour - time.Minute), // 오늘 (자정 직전)
			today.AddDate(0, 0, 1).Add(time.Hour), // 내일 — 제외
		}, nil)
	w, starved := recallStarvedToday(context.Background(), f, flatTestThresholds())
	if starved {
		t.Fatalf("(%d, %v), want starved=false — 당일 read 2행", w, starved)
	}
	if w != 7 {
		t.Fatalf("writes=%d, want 7(회상 있어도 기록 수는 그대로 반환)", w)
	}
}

// TestRecallStarvedCorruptValue — daily:write:<오늘> 파손값(비int)은
// 기존처럼 조용히 0 → (0, false).
func TestRecallStarvedCorruptValue(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	f := flatKeyFake(t, map[string]string{"daily:write:" + today: "abc"}, nil, nil)
	w, starved := recallStarvedToday(context.Background(), f, flatTestThresholds())
	if w != 0 || starved {
		t.Fatalf("(%d, %v), want (0, false) — 파손 플랫 값 조용히 0", w, starved)
	}
}

// TestRecallStarvedReadQueryError — read_events 조회 실패(테이블 없는 구
// 스키마 등)는 read=0 취급이 아니라 조용히 (0, false)(§13 종료 경로
// 불변 — 조회 에러는 경고 없이 false).
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
