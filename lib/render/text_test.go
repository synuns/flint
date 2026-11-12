// text_test.go — TruncateRunes·CountPending 테스트. v0.34.3 단일 구현
// 수렴 관례를 lib/render 이동 후에도 승계한다(스펙 §2).
package render

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestTruncateRunesSingleSource — rune 절단(멀티바이트 안전) 단일 구현.
// 상한 이하·빈 문자열 불변, 혼합 문자열 룬 단위 절단.
func TestTruncateRunesSingleSource(t *testing.T) {
	long := strings.Repeat("한", 60) // 60룬(180바이트) — 바이트 절단이면 파손
	if got := TruncateRunes(long, 40); len([]rune(got)) != 40 || got != strings.Repeat("한", 40) {
		t.Errorf("룬 절단 위반: %d룬", len([]rune(got)))
	}
	if got := TruncateRunes("abc한", 3); got != "abc" {
		t.Errorf("혼합 문자열 절단=%q, want %q", got, "abc")
	}
	if got := TruncateRunes("한글", 2); got != "한글" {
		t.Errorf("경계(상한==길이) 불변 위반: %q", got)
	}
	if got := TruncateRunes("한글", 40); got != "한글" {
		t.Errorf("상한 이하 불변 위반: %q", got)
	}
	if got := TruncateRunes("", 40); got != "" {
		t.Errorf("빈 문자열: %q", got)
	}
}

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
