package wire_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

type queryOnceFake struct {
	calls int
	errs  []error
	rows  [][]any
	wire.HistoryStubs
}

func (f *queryOnceFake) Migrate(ctx context.Context) error { return nil }
func (f *queryOnceFake) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (f *queryOnceFake) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (f *queryOnceFake) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (f *queryOnceFake) Commit(ctx context.Context, msg string) (string, error) {
	return "abc123", nil
}
func (f *queryOnceFake) Ping(ctx context.Context) error { return nil }
func (f *queryOnceFake) Close()                         {}
func (f *queryOnceFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	i := f.calls
	f.calls++
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.rows, nil
}

func TestQueryOnceCanceledReturnsImmediately(t *testing.T) {
	f := &queryOnceFake{errs: []error{context.Canceled}}
	rows, err := wire.QueryOnce(context.Background(), f, `SELECT 1`, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if rows != nil {
		t.Fatalf("rows=%v, want nil", rows)
	}
	if f.calls != 1 {
		t.Fatalf("Query 호출 %d회, want 1(영구 오류는 재시도 금지)", f.calls)
	}
}

func TestQueryOnceWrappedCanceledNoRetry(t *testing.T) {
	f := &queryOnceFake{errs: []error{fmt.Errorf("query: %w", context.Canceled)}}
	_, err := wire.QueryOnce(context.Background(), f, `SELECT 1`, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want 래핑된 context.Canceled", err)
	}
	if f.calls != 1 {
		t.Fatalf("Query 호출 %d회, want 1(래핑된 취소도 재시도 금지)", f.calls)
	}
}

func TestQueryOnceRetriesTransientError(t *testing.T) {
	want := [][]any{{int64(1)}}
	f := &queryOnceFake{errs: []error{errors.New("connection refused")}, rows: want}
	rows, err := wire.QueryOnce(context.Background(), f, `SELECT 1`, nil)
	if err != nil {
		t.Fatalf("err=%v, want 일시 오류 1회 재시도 후 성공", err)
	}
	if len(rows) != 1 || rows[0][0] != int64(1) {
		t.Fatalf("rows=%v, want 재시도 결과 행", rows)
	}
	if f.calls != 2 {
		t.Fatalf("Query 호출 %d회, want 2(일시 오류는 기존 재시도 유지)", f.calls)
	}
}

func TestQueryOnceSuccessSingleCall(t *testing.T) {
	f := &queryOnceFake{rows: [][]any{{int64(7)}}}
	rows, err := wire.QueryOnce(context.Background(), f, `SELECT 1`, nil)
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if rows[0][0] != int64(7) {
		t.Fatalf("rows=%v, want 7", rows)
	}
	if f.calls != 1 {
		t.Fatalf("Query 호출 %d회, want 1", f.calls)
	}
}

func TestQueryOnceRealCanceled(t *testing.T) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		t.Skip("SUPERLITE_REAL=1일 때만 실 store 검증")
	}
	pool, err := db.Connect(context.Background(), testengine.ResolveDSN())
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	st := &poolStore{pool: pool}
	t.Cleanup(st.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = wire.QueryOnce(ctx, st, `SELECT 1`, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
}

func TestQueryOnceDeadlineExceededReturnsImmediatelyV1321(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	st := &countingQueryStore{}
	_, err := wire.QueryOnce(ctx, st, `SELECT 1`, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if st.calls != 1 {
		t.Fatalf("calls = %d, want 1(만료는 영구 — 재시도 없음)", st.calls)
	}
}

type countingQueryStore struct {
	wire.Store
	calls int
}

func (c *countingQueryStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	c.calls++
	return nil, ctx.Err()
}

type wrappingQueryStore struct {
	wire.Store
	calls int
}

func (w *wrappingQueryStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	w.calls++
	return nil, fmt.Errorf("engine: query failed: %w", context.DeadlineExceeded)
}

func TestQueryOnceWrappedDeadlineNotRetriedV1321(t *testing.T) {
	st := &wrappingQueryStore{}
	_, err := wire.QueryOnce(context.Background(), st, `SELECT 1`, nil)
	if st.calls != 1 {
		t.Fatalf("calls = %d, want 1(래핑된 만료도 영구)", st.calls)
	}
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want wrapped DeadlineExceeded", err)
	}
}
