// pending_count_test.go — the CountPending single-lookup contract (empty
// response·unparseable value are a quiet 0; a lookup failure propagates as
// an error — the existing cli.countPending convention unchanged).
package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

// fakeCountStore — the minimal wire.Store double for CountPending
// verification. Only Query works; the rest are unused (fail immediately
// if called). The HistoryStubs embedding absorbs the five history methods
// with unsupported default implementations.
type fakeCountStore struct {
	wire.HistoryStubs
	queryFn func(sql string, args []any) ([][]any, error)
}

func (f *fakeCountStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	return f.queryFn(sql, args)
}

func (f *fakeCountStore) Migrate(ctx context.Context) error { return errors.New("unused") }

func (f *fakeCountStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, errors.New("unused")
}

func (f *fakeCountStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, errors.New("unused")
}

func (f *fakeCountStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, errors.New("unused")
}

func (f *fakeCountStore) Commit(ctx context.Context, msg string) (string, error) {
	return "", errors.New("unused")
}

func (f *fakeCountStore) Ping(ctx context.Context) error { return errors.New("unused") }

func (f *fakeCountStore) Close() {}

// TestCountPendingSingleLookup — single lookup of the pending count
// (empty response·unparseable value are a quiet 0; a lookup failure
// propagates as an error — the existing cli.countPending convention
// unchanged).
func TestCountPendingSingleLookup(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		queryFn func(sql string, args []any) ([][]any, error)
		want    int
		wantErr bool
	}{
		{"정상 count", func(string, []any) ([][]any, error) { return [][]any{{int64(7)}}, nil }, 7, false},
		{"빈 응답은 0", func(string, []any) ([][]any, error) { return nil, nil }, 0, false},
		{"빈 행은 0", func(string, []any) ([][]any, error) { return [][]any{{}}, nil }, 0, false},
		{"해석 불가 값은 0", func(string, []any) ([][]any, error) { return [][]any{{"pending"}}, nil }, 0, false},
		{"조회 실패 전파", func(string, []any) ([][]any, error) { return nil, errors.New("boom") }, 0, true},
	}
	for _, tc := range cases {
		f := &fakeCountStore{queryFn: tc.queryFn}
		got, err := CountPending(ctx, f)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: 오류 미전파 (got=%d)", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: 예기치 않은 오류: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: CountPending = %d, want %d", tc.name, got, tc.want)
		}
	}
}
