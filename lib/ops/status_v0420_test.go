package ops

import (
	"context"
	"strings"
	"testing"
	"time"
)

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

func TestLastReadAtMergesNewerSide(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	older := now.Add(-2 * time.Hour).Truncate(time.Second)

	st := lastReadFake(map[string]string{"last_read_at": older.Format(time.RFC3339)}, now, false)
	if got := lastReadAtOrNull(context.Background(), st, metaKVOf(st)); got == nil || *got != now.Format(time.RFC3339) {
		t.Errorf("이벤트 최신: got %v, want %v", got, now.Format(time.RFC3339))
	}

	st = lastReadFake(map[string]string{"last_read_at": now.Format(time.RFC3339)}, older, false)
	if got := lastReadAtOrNull(context.Background(), st, metaKVOf(st)); got == nil || *got != now.Format(time.RFC3339) {
		t.Errorf("meta 최신: got %v, want %v", got, now.Format(time.RFC3339))
	}
}

func TestLastReadAtQueryErrorKeepsMeta(t *testing.T) {
	want := time.Now().UTC().Add(-time.Minute).Truncate(time.Second).Format(time.RFC3339)
	st := lastReadFake(map[string]string{"last_read_at": want}, nil, true)
	got := lastReadAtOrNull(context.Background(), st, metaKVOf(st))
	if got == nil || *got != want {
		t.Errorf("조회 실패: got %v, want 잔존 meta %q", got, want)
	}
}

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
