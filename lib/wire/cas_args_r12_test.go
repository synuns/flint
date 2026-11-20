package wire

import (
	"context"
	"testing"
)

// fakeArgStore — a minimal fake capturing the actual arguments of Query calls
// (for spread verification — wire_test.go's fakeStore focuses on SQL string capture).
type fakeArgStore struct {
	query func(ctx context.Context, sql string, args ...any) ([][]any, error)
	HistoryStubs
}

func (f *fakeArgStore) Migrate(ctx context.Context) error { return nil }
func (f *fakeArgStore) InsertBatch(ctx context.Context, r []map[string]any) ([]int, error) {
	return nil, nil
}
func (f *fakeArgStore) ExistsHash(ctx context.Context, h string) (bool, error) { return false, nil }
func (f *fakeArgStore) ExistsProject(ctx context.Context, n string) (bool, error) {
	return false, nil
}
func (f *fakeArgStore) Commit(ctx context.Context, m string) (string, error) { return "", nil }
func (f *fakeArgStore) Ping(ctx context.Context) error                       { return nil }
func (f *fakeArgStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	return f.query(ctx, sql, args...)
}
func (f *fakeArgStore) Close() {}

// TestCompareSetMetaKVArgSpread — verifies that the 3 arguments are passed
// spread to the variadic Query (preventing a recurrence of the defect where
// wrapping as 1 slice broke CAS entirely — the fake Store verifies the actual argument count).
func TestCompareSetMetaKVArgSpread(t *testing.T) {
	var gotArgs []any
	st := &fakeArgStore{query: func(_ context.Context, _ string, args ...any) ([][]any, error) {
		gotArgs = args
		return [][]any{{"k"}}, nil
	}}
	won, err := CompareSetMetaKV(context.Background(), st, "k", "old", "new")
	if err != nil || !won {
		t.Fatalf("won=%v err=%v", won, err)
	}
	if len(gotArgs) != 3 {
		t.Fatalf("인자 %d개 — 스프레드 누락(CAS 불능 회귀): %v", len(gotArgs), gotArgs)
	}
	if gotArgs[0] != "k" || gotArgs[1] != "old" || gotArgs[2] != "new" {
		t.Fatalf("인자 순서 오류: %v", gotArgs)
	}
}

// TestCompareSetMetaKVLost — a WHERE miss (0 rows) is lost=false.
func TestCompareSetMetaKVLost(t *testing.T) {
	st := &fakeArgStore{query: func(_ context.Context, _ string, _ ...any) ([][]any, error) {
		return nil, nil // RETURNING 0 rows
	}}
	won, err := CompareSetMetaKV(context.Background(), st, "k", "old", "new")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if won {
		t.Fatal("0행인데 won=true")
	}
}
