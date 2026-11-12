// pending_count_test.go — CountPending 단일 조회 계약(빈 응답·해석 불가
// 값은 조용한 0, 조회 실패는 오류 전파 — 기존 cli.countPending 관례 불변).
// v0.39.0 T4: lib/render/text_test.go에서 이동(정의의 knowledge 이관에
// 동반 — 룰링: render에는 임시 소속이었다).
package knowledge

import (
	"context"
	"errors"
	"testing"
)

// fakeCountStore — CountPending 검증용 최소 wire.Store 대역. Query만
// 동작하고 나머지는 미사용(호출되면 즉시 실패).
type fakeCountStore struct {
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

// TestCountPendingSingleLookup — pending 수 단일 조회(빈 응답·해석 불가
// 값은 조용한 0, 조회 실패는 오류 전파 — 기존 cli.countPending 관례 불변).
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
