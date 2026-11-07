// drill_v0162_test.go — v0.16.2 R1 드릴 계약: DROP 후 pg_database 잔존
// 검증·종료 정리 ctx 독립. 시임 주입으로 실재현 불가능한 시나리오(무음
// no-op DROP·재생 ctx 만료)를 결정적으로 검증한다.
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

// miniDump — 최소 덤프 1개. 이 계약 테스트는 재생 결과가 아니라 DROP
// 경로만 본다.
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

// shrinkDrillBackoff — DROP 재시도 대기 제거. 주입 실패는 3회 모두
// 실패하므로 백오프(총 4s) 대기 없이 즉시 소진시킨다.
func shrinkDrillBackoff(t *testing.T) {
	t.Helper()
	saved := drillDropBackoff
	drillDropBackoff = []time.Duration{0, 0, 0}
	t.Cleanup(func() { drillDropBackoff = saved })
}

// TestDropDBVerifiesResidue — DROP이 nil을 반환해도(무음 no-op 엔진
// 결함) pg_database에 잔존하면 RunDrill은 성공을 가장하지 않고 명시적
// 잔존 에러를 돌려준다. 선행 정리 단계에서 걸리므로 성공 기록도 없다.
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
		return 1, nil // DROP은 nil — pg_database는 잔존 보고
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
	if !strings.Contains(err.Error(), "잔존") ||
		!strings.Contains(err.Error(), "수동 DROP 필요") {
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
	// 주입은 pg_database 조회 결과만 흉내 — 실 드릴 DB는 만들어지지 않았다.
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

// TestCleanupUsesIndependentContext — 재생 경로 ctx가 만료되어도 종료
// 정리 DROP은 독립 ctx로 항상 실행된다. 주입 카운터로 DROP 호출과 그
// ctx를 포획해 검증한다.
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

	// 재생 ctx를 테스트가 소유 — 선행 정리 직후 만료시켜 "재생 타임아웃
	// 소진"을 결정적으로 만든다(이후 문은 만료 ctx로 실패한다).
	replayCtx, replayCancel := context.WithCancel(context.Background())
	savedCtxFn := drillCtxFn
	drillCtxFn = func() (context.Context, context.CancelFunc) { return replayCtx, replayCancel }
	type dropCall struct {
		ctx     context.Context
		expired bool // 호출 시점의 ctx 만료 여부
	}
	var drops []dropCall
	savedDrop := dropDrillDBFn
	dropDrillDBFn = func(c context.Context, p *pgxpool.Pool) error {
		drops = append(drops, dropCall{ctx: c, expired: c.Err() != nil})
		if len(drops) == 1 {
			replayCancel() // 선행 정리 성공 직후 재생 ctx 만료
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
	// 호출 시점에 만료되지 않은 별개의 ctx로 실행됐는지 본다(반환 후
	// ccancel로 정리 ctx도 스스로 만료되므로 시점 포획이 필요하다).
	if cleanup.ctx == replayCtx || cleanup.expired {
		t.Fatalf("정리 DROP이 재생 ctx 공유/만료로 실행: shared=%v expired=%v",
			cleanup.ctx == replayCtx, cleanup.expired)
	}
	// CREATE가 만료 ctx로 실패 — 실 드릴 DB 잔존 없음(잔존 시 실구현 정리).
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
