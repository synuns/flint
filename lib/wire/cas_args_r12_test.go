package wire

import (
	"context"
	"testing"
)

// fakeArgStore — Query 호출의 실제 인자를 포횰하는 최소 fake(스프레드
// 검증 전용 — wire_test.go의 fakeStore는 SQL 문자열 캡처 중심).
type fakeArgStore struct {
	query func(ctx context.Context, sql string, args ...any) ([][]any, error)
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

// TestCompareSetMetaKVArgSpread — v1.0.11(R12-A1): variadic Query에 인자
// 3개가 스프레드로 전달되는지(슬라이스 1개 래핑이 CAS를 전면 불능으로
// 만들었던 결함의 재발 방지 — fake Store가 실제 인자 수를 검증).
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

// TestCompareSetMetaKVLost — WHERE 미충족(0행)은 lost=false.
func TestCompareSetMetaKVLost(t *testing.T) {
	st := &fakeArgStore{query: func(_ context.Context, _ string, _ ...any) ([][]any, error) {
		return nil, nil // RETURNING 0행
	}}
	won, err := CompareSetMetaKV(context.Background(), st, "k", "old", "new")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if won {
		t.Fatal("0행인데 won=true")
	}
}
