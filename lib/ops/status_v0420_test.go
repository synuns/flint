// status_v0420_test.go — v0.42.0 T2 회귀: read 경로가 meta last_read_at을
// 더 쓰지 않는다(RecordReadUsage 폐지) — status last_read_at은 잔존 meta
// 값과 read_events max(ts) 중 더 최근 쪽(doctor checkLastSeen과 동일 룰).
// meta 값만으로 보고하면 전환 후 영구히 과거 시각에 갇힌다.
package ops

import (
	"context"
	"strings"
	"testing"
	"time"
)

// lastReadFake — meta kv 맵 + read_events max(ts) 셀을 반환하는 fake.
func lastReadFake(kv map[string]string, evMax any, evErr bool) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "max(ts) FROM read_events") {
			if evErr {
				return nil, context.Canceled
			}
			return [][]any{{evMax}}, nil
		}
		if strings.Contains(sql, "FROM meta") && len(args) > 0 {
			if k, ok := args[0].(string); ok {
				if v, hit := kv[k]; hit {
					return [][]any{{v}}, nil
				}
			}
			return nil, nil
		}
		return nil, nil
	}}
}

// TestLastReadAtDerivedFromEvents — meta 부재 시 read_events max(ts) 노출.
func TestLastReadAtDerivedFromEvents(t *testing.T) {
	ev := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	st := lastReadFake(nil, ev, false)
	got := lastReadAtOrNull(context.Background(), st, metaKVOf(st))
	if got == nil {
		t.Fatal("last_read_at=nil, want read_events max(ts) 노출")
	}
	if want := ev.Format(time.RFC3339); *got != want {
		t.Errorf("last_read_at=%q, want %q", *got, want)
	}
}

// TestLastReadAtMergesNewerSide — 잔존 meta 값과 이벤트 시각 중 최근 쪽.
func TestLastReadAtMergesNewerSide(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	older := now.Add(-2 * time.Hour).Truncate(time.Second)
	// 이벤트가 더 최신 → 이벤트 승.
	st := lastReadFake(map[string]string{"last_read_at": older.Format(time.RFC3339)}, now, false)
	if got := lastReadAtOrNull(context.Background(), st, metaKVOf(st)); got == nil || *got != now.Format(time.RFC3339) {
		t.Errorf("이벤트 최신: got %v, want %v", got, now.Format(time.RFC3339))
	}
	// 잔존 meta가 더 최신 → meta 승(이벤트는 무시).
	st = lastReadFake(map[string]string{"last_read_at": now.Format(time.RFC3339)}, older, false)
	if got := lastReadAtOrNull(context.Background(), st, metaKVOf(st)); got == nil || *got != now.Format(time.RFC3339) {
		t.Errorf("meta 최신: got %v, want %v", got, now.Format(time.RFC3339))
	}
}

// TestLastReadAtQueryErrorKeepsMeta — 이벤트 조회 실패 시 잔존 meta 유지.
func TestLastReadAtQueryErrorKeepsMeta(t *testing.T) {
	want := time.Now().UTC().Add(-time.Minute).Truncate(time.Second).Format(time.RFC3339)
	st := lastReadFake(map[string]string{"last_read_at": want}, nil, true)
	got := lastReadAtOrNull(context.Background(), st, metaKVOf(st))
	if got == nil || *got != want {
		t.Errorf("조회 실패: got %v, want 잔존 meta %q", got, want)
	}
}

// metaKVOf — fake에서 동작하는 getKV(MetaGetKV와 동일 계약 래퍼).
func metaKVOf(st *fakeStore) func(string, string) (string, error) {
	return func(key, def string) (string, error) {
		rows, err := st.Query(context.Background(), `SELECT value FROM meta WHERE key = $1`, key)
		if err != nil {
			return def, err
		}
		if len(rows) == 0 || len(rows[0]) == 0 {
			return def, nil
		}
		return rows[0][0].(string), nil
	}
}
