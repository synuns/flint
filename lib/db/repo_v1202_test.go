package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/synuns/flint/internal/testengine"
)

func TestSupersedeExecutesOnLockHeldConnV1202(t *testing.T) {
	if os.Getenv("SUPERLITE_REAL") == "1" {
		t.Skip("REAL 모드 — 시임 스왑 전제(실경로는 slow 라인이 검증)")
	}
	var lockExec, poolExec []transitionCall
	prevLock, prevTrans := supersedeLockFn, transitionExecFn
	supersedeLockFn = func(ctx context.Context, r *Repo) (supersedeExecFn, supersedeQueryFn, func(), error) {
		exec := func(ctx context.Context, query string, args ...any) (int64, error) {
			lockExec = append(lockExec, transitionCall{sql: query, args: args})
			return 1, nil
		}
		query := func(ctx context.Context, q string, args ...any) pgx.Row {
			return fakeSupersedeRow{}
		}
		return exec, query, func() {}, nil
	}
	transitionExecFn = func(ctx context.Context, r *Repo, query string, args ...any) (int64, error) {
		poolExec = append(poolExec, transitionCall{sql: query, args: args})
		return 1, nil
	}
	t.Cleanup(func() {
		supersedeLockFn = prevLock
		transitionExecFn = prevTrans
	})

	if _, err := (&Repo{}).Supersede(context.Background(), 5, 9, time.Now().UTC()); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	if len(lockExec) != 1 {
		t.Fatalf("lock-held 실행 %d회, want 1 (가드 UPDATE는 락 커넥션에서 실행)", len(lockExec))
	}
	if !strings.Contains(lockExec[0].sql, "superseded_by = $2") || !strings.Contains(lockExec[0].sql, "NOT EXISTS") {
		t.Errorf("lock-held SQL 가드 미병기: %q", lockExec[0].sql)
	}
	if len(poolExec) != 0 {
		t.Fatalf("pool 경유 실행 잔여 %d건 — supersede가 두 번째 풀 슬롯을 소모함(r88 결함): %v",
			len(poolExec), poolExec)
	}
}

func TestSlowSupersedeParallelBeyondPoolV1202(t *testing.T) {
	ctx := context.Background()
	scratchDSN := testengine.Scratch(t)
	cfg, err := PoolConfig(scratchDSN)
	if err != nil {
		t.Fatalf("PoolConfig: %v", err)
	}
	const maxConns = 4
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	r := &Repo{pool: pool, dsn: scratchDSN}
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	const parallel = 14
	recs := make([]map[string]any, 0, parallel*2)
	for i := 0; i < parallel; i++ {
		recs = append(recs,
			knowledgeRec(fmt.Sprintf("v1202-pool-old-%02d", i)),
			knowledgeRec(fmt.Sprintf("v1202-pool-new-%02d", i)))
	}
	ids, err := r.InsertBatch(ctx, recs)
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	at := time.Now().UTC()
	errs := make([]error, parallel)
	var wg sync.WaitGroup
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			oldID, newID := ids[i*2], ids[i*2+1]
			n, err := r.Supersede(cctx, oldID, newID, at)
			if err == nil && n != 1 {
				err = fmt.Errorf("supersede[%d]: %d rows, want 1", i, n)
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("병렬 supersede[%d] 실패 — 풀 고갈(커넥션 2개 소모 잔여 가능): %v", i, err)
		}
	}

	var done int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM knowledge
		WHERE status = 'invalidated' AND superseded_by IS NOT NULL`).Scan(&done); err != nil {
		t.Fatalf("결과 조회: %v", err)
	}
	if done != parallel {
		t.Fatalf("완결 supersede=%d, want %d", done, parallel)
	}
}
