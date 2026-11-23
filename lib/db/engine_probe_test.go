package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/synuns/flint/lib/wire"
)

type probeFakeStore struct {
	queryFn func(sql string, args []any) ([][]any, error)
}

func (f *probeFakeStore) Migrate(ctx context.Context) error { return nil }
func (f *probeFakeStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (f *probeFakeStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (f *probeFakeStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (f *probeFakeStore) Commit(ctx context.Context, msg string) (string, error) {
	return "", nil
}
func (f *probeFakeStore) Ping(ctx context.Context) error { return nil }
func (f *probeFakeStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	return f.queryFn(sql, args)
}
func (f *probeFakeStore) Close() {}
func (f *probeFakeStore) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	return nil, nil
}
func (f *probeFakeStore) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	return nil, nil
}
func (f *probeFakeStore) DiffRows(ctx context.Context, fromHash, toHash string) ([]wire.KnowledgeDiffRow, error) {
	return nil, nil
}
func (f *probeFakeStore) DiffStat(ctx context.Context, hashes []string) ([]wire.TableDiffStat, error) {
	return nil, nil
}
func (f *probeFakeStore) DiffCommits(ctx context.Context, fromHash, toHash string) ([]wire.DiffCommitSummary, error) {
	return nil, nil
}

func TestDoltLogDepthProbeOwnedByDB(t *testing.T) {
	var got string
	f := &probeFakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		got = sql
		return [][]any{{int64(4242)}}, nil
	}}
	depth, err := DoltLogDepth(context.Background(), f)
	if err != nil {
		t.Fatalf("DoltLogDepth err: %v", err)
	}
	if depth != 4242 {
		t.Errorf("깊이=%d, want 4242", depth)
	}

	if !strings.Contains(got, "SELECT commit_order::int8 FROM "+DoltLogTable+" ORDER BY commit_order DESC LIMIT 1") {
		t.Errorf("O(1) 프로브 아님: %s", got)
	}

	rawNum := pgtype.Numeric{}
	if err := rawNum.ScanInt64(pgtype.Int8{Int64: 370, Valid: true}); err != nil {
		t.Fatalf("Numeric 구성 실패: %v", err)
	}
	numF := &probeFakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return [][]any{{rawNum}}, nil
	}}
	if d, err := DoltLogDepth(context.Background(), numF); err != nil || d != 370 {
		t.Errorf("numeric 셀 깊이=%d err=%v, want 370/nil (r109-1)", d, err)
	}
	if strings.Contains(strings.ToLower(got), "count(") {
		t.Errorf("count(*) 프로브 금지(상시 성장 테이블 전수 계수): %s", got)
	}

	empty := &probeFakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	if d, err := DoltLogDepth(context.Background(), empty); err != nil || d != 0 {
		t.Errorf("빈 로그 depth=%d err=%v, want 0/nil", d, err)
	}

	boom := errors.New("no such table")
	failing := &probeFakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, boom
	}}
	if _, err := DoltLogDepth(context.Background(), failing); !errors.Is(err, boom) {
		t.Errorf("질의 실패 전파 아님: %v", err)
	}
}
