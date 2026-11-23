package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
)

func miniDump(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mini.sql")
	if err := os.WriteFile(path,
		[]byte("CREATE TABLE \"knowledge\" (\"id\" integer);\nINSERT INTO \"knowledge\" (\"id\") VALUES (1);\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func shrinkDrillBackoff(t *testing.T) {
	t.Helper()
	saved := drillDropBackoff
	drillDropBackoff = []time.Duration{0, 0, 0}
	t.Cleanup(func() { drillDropBackoff = saved })
}

func TestDropDBVerifiesResidue(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := testDSN()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect doltgres: %v", err)
	}
	defer pool.Close()

	shrinkDrillBackoff(t)
	savedResidue := drillResidueFn
	drillResidueFn = func(context.Context, *pgxpool.Pool) (int, error) {
		return 1, nil
	}
	t.Cleanup(func() { drillResidueFn = savedResidue })

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS maintenance_runs (
		id serial PRIMARY KEY, ts timestamptz NOT NULL DEFAULT now(),
		kind text NOT NULL, details text)`); err != nil {
		t.Fatalf("ensure maintenance_runs: %v", err)
	}
	var before int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM maintenance_runs WHERE kind = 'drill'`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	_, err = RunDrill(dsn, miniDump(t))
	if err == nil {
		t.Fatalf("잔존인데 성공 반환")
	}
	if !strings.Contains(err.Error(), "still present") ||
		!strings.Contains(err.Error(), "manual DROP required") {
		t.Fatalf("잔존 에러 아님: %v", err)
	}
	var after int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM maintenance_runs WHERE kind = 'drill'`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("잔존 시 성공 기록 발생: %d -> %d", before, after)
	}

	var dbs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname = '`+DrillDB+`'`).
		Scan(&dbs); err != nil {
		t.Fatalf("db check: %v", err)
	}
	if dbs != 0 {
		t.Fatalf("%s 실잔존: %d", DrillDB, dbs)
	}
}

func TestCleanupUsesIndependentContext(t *testing.T) {
	unlock := drillLock(t)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := testDSN()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect doltgres: %v", err)
	}
	defer pool.Close()

	replayCtx, replayCancel := context.WithCancel(context.Background())
	savedCtxFn := drillCtxFn
	drillCtxFn = func() (context.Context, context.CancelFunc) { return replayCtx, replayCancel }
	type dropCall struct {
		ctx     context.Context
		expired bool
	}
	var drops []dropCall
	savedDrop := dropDrillDBFn
	dropDrillDBFn = func(c context.Context, p *pgxpool.Pool) error {
		drops = append(drops, dropCall{ctx: c, expired: c.Err() != nil})
		if len(drops) == 1 {
			replayCancel()
		}
		return nil
	}
	shrinkDrillBackoff(t)
	t.Cleanup(func() {
		drillCtxFn = savedCtxFn
		dropDrillDBFn = savedDrop
	})

	_, err = RunDrill(dsn, miniDump(t))
	if err == nil {
		t.Fatalf("재생 ctx 만료인데 성공 반환")
	}
	if replayCtx.Err() == nil {
		t.Fatalf("전제 불성립: 재생 ctx가 만료되지 않음")
	}
	if len(drops) < 2 {
		t.Fatalf("만료 후 종료 정리 DROP 미실행: 호출 %d회", len(drops))
	}
	cleanup := drops[len(drops)-1]

	if cleanup.ctx == replayCtx || cleanup.expired {
		t.Fatalf("정리 DROP이 재생 ctx 공유/만료로 실행: shared=%v expired=%v",
			cleanup.ctx == replayCtx, cleanup.expired)
	}

	var dbs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname = '`+DrillDB+`'`).
		Scan(&dbs); err != nil {
		t.Fatalf("db check: %v", err)
	}
	if dbs != 0 {
		savedDrop(context.Background(), pool)
		t.Fatalf("%s 잔존: %d", DrillDB, dbs)
	}
}
