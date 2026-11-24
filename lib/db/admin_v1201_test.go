package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestSlowV15GateDedupeUniqueIndex(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if _, err := r.pool.Exec(ctx, `DROP INDEX IF EXISTS uq_knowledge_links_triple`); err != nil {
		t.Fatalf("v14 재현 인덱스 삭제: %v", err)
	}
	setVersion(t, r, "14")

	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v1201-seal-a"), knowledgeRec("v1201-seal-b"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	for _, rel := range []string{"refines", "refines", "supports"} {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO knowledge_links (from_id, to_id, relation) VALUES ($1, $2, $3)`,
			ids[0], ids[1], rel); err != nil {
			t.Fatalf("중복 링크 심기: %v", err)
		}
	}
	if err := r.EnsureSchemaV15(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV15: %v", err)
	}
	if v := readVersion(t, r); v != "15" {
		t.Fatalf("schema_version=%q want 15", v)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_links WHERE from_id = $1`, ids[0]).Scan(&n); err != nil {
		t.Fatalf("링크 수 조회: %v", err)
	}
	if n != 2 {
		t.Fatalf("링크 수=%d want 2 (duplicates collapsed)", n)
	}

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge_links (from_id, to_id, relation) VALUES ($1, $2, 'refines')`,
		ids[0], ids[1]); err == nil {
		t.Fatal("중복 triple INSERT 허용 — unique 봉인 미적용")
	}

	if err := r.EnsureSchemaV15(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV15 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "15" {
		t.Fatalf("재호출 후 schema_version=%q want 15", v)
	}
}

func isSequencingUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if pgErr.Code != "XX000" {
		return false
	}
	named := pgErr.ConstraintName == "uq_knowledge_links_triple" ||
		strings.Contains(pgErr.Message, "uq_knowledge_links_triple")
	return named && strings.Contains(pgErr.Message, "transaction sequencing")
}

func retryParallelInsert(ctx context.Context, r *Repo, query string, args ...any) (int, error) {
	const attempts = 4
	var err error
	tries := 0
	for i := 0; i < attempts; i++ {
		tries++
		if _, err = r.pool.Exec(ctx, query, args...); err == nil || !isSequencingUniqueViolation(err) {
			return tries, err
		}
		time.Sleep(time.Duration(i+1) * 25 * time.Millisecond)
	}
	return tries, err
}

func runParallelIdempotentInsert(ctx context.Context, r *Repo, fromID, toID int) ([]error, int) {
	var wg sync.WaitGroup
	errs := make([]error, 5)
	var retries atomic.Int64
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			t, err := retryParallelInsert(ctx, r,
				`INSERT INTO knowledge_links (from_id, to_id, relation)
				 VALUES ($1, $2, $3)
				 ON CONFLICT (from_id, to_id, relation) DO NOTHING`,
				fromID, toID, "refines")
			errs[i] = err
			retries.Add(int64(t) - 1)
		}(i)
	}
	wg.Wait()
	return errs, int(retries.Load())
}

func TestSequencingViolationClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"커밋 시퀀싱 유니크 위반(XX000·uq triple·sequencing)은 재시도 대상", &pgconn.PgError{
			Code: "XX000",
			Message: "Committing this transaction resulted in a working set with constraint violations ... " +
				"Unique Key Constraint Violation, Name: uq_knowledge_links_triple. " +
				"This constraint violation may be the result of a previous merge or the result of transaction sequencing",
		}, true},
		{"ConstraintName 필드 매칭도 재시도 대상", &pgconn.PgError{
			Code:           "XX000",
			Message:        "constraint violations ... the result of transaction sequencing",
			ConstraintName: "uq_knowledge_links_triple",
		}, true},
		{"XX000이라도 uq triple이 아니면 전파", &pgconn.PgError{
			Code: "XX000", Message: "Unique Key Constraint Violation, Name: other_key — transaction sequencing",
		}, false},
		{"XX000이라도 sequencing 근거 없으면 전파(마스킹 금지)", &pgconn.PgError{
			Code:           "XX000",
			Message:        "Unique Key Constraint Violation, Name: uq_knowledge_links_triple (2 row(s))",
			ConstraintName: "uq_knowledge_links_triple",
		}, false},
		{"23505 일반 unique 위반은 전파", &pgconn.PgError{
			Code: "23505", Message: `duplicate key value violates unique constraint "uq_knowledge_links_triple"`,
		}, false},
		{"pgconn.PgError 외 오류는 전파", fmt.Errorf("plain error"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSequencingUniqueViolation(tc.err); got != tc.want {
				t.Fatalf("isSequencingUniqueViolation = %v, want %v (err=%v)", got, tc.want, tc.err)
			}
		})
	}
}

func TestSlowParallelLinkInsertSingleRowV1201(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v1201-par-link-a"), knowledgeRec("v1201-par-link-b"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	errs, retries := runParallelIdempotentInsert(ctx, r, ids[0], ids[1])
	for i, err := range errs {
		if err != nil {
			if isSequencingUniqueViolation(err) {
				t.Fatalf("병렬 INSERT[%d]: 커밋 시퀀싱 XX000 재시도 소진(4회): %v", i, err)
			}
			t.Fatalf("병렬 INSERT[%d]: %v", i, err)
		}
	}
	if retries > 0 {
		t.Logf("시퀀싱 경합 관측: 재시도 %d회 — 멱등 패자 재시도 계약 동작", retries)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_links WHERE from_id = $1 AND to_id = $2 AND relation = 'refines'`,
		ids[0], ids[1]).Scan(&n); err != nil {
		t.Fatalf("링크 수 조회: %v", err)
	}
	if n != 1 {
		t.Fatalf("동일 링크 행=%d want 1 (ON CONFLICT 병렬 멱등)", n)
	}
}

func TestSlowSuiteSerialGreen(t *testing.T) {
	loops := 100
	if v := os.Getenv("FLINT_SLOW_LOOPS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("FLINT_SLOW_LOOPS=%q: 양의 정수 필요", v)
		}
		loops = n
	}
	r := newTestRepo(t)
	ctx := context.Background()
	rounds := make([]int, 0, loops)
	for round := 0; round < loops; round++ {
		ids, err := r.InsertBatch(ctx, []map[string]any{
			knowledgeRec(fmt.Sprintf("v1201-dist-a-%04d", round)),
			knowledgeRec(fmt.Sprintf("v1201-dist-b-%04d", round)),
		})
		if err != nil {
			t.Fatalf("round %d InsertBatch: %v", round, err)
		}
		errs, retries := runParallelIdempotentInsert(ctx, r, ids[0], ids[1])
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d 병렬 INSERT[%d]: %v (커밋 시퀀싱 재시도 소진 포함 — 100회 루프 분포 red)", round, i, err)
			}
		}
		rounds = append(rounds, retries)
		var n int
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*) FROM knowledge_links WHERE from_id = $1 AND to_id = $2 AND relation = 'refines'`,
			ids[0], ids[1]).Scan(&n); err != nil {
			t.Fatalf("round %d 링크 수 조회: %v", round, err)
		}
		if n != 1 {
			t.Fatalf("round %d 동일 링크 행=%d want 1 (병렬 멱등)", round, n)
		}
		if _, err := r.pool.Exec(ctx,
			`DELETE FROM knowledge_links WHERE from_id = $1 AND to_id = $2 AND relation = 'refines'`,
			ids[0], ids[1]); err != nil {
			t.Fatalf("round %d 링크 정리: %v", round, err)
		}
		if _, err := r.pool.Exec(ctx,
			fmt.Sprintf(`DELETE FROM knowledge WHERE id IN (%d, %d)`, ids[0], ids[1])); err != nil {
			t.Fatalf("round %d 지식 행 정리: %v", round, err)
		}
	}
	contentious := 0
	for _, retries := range rounds {
		if retries > 0 {
			contentious++
		}
	}
	t.Logf("분포 수집(%d회 루프): 시퀀싱 경합 라운드 %d/%d — red 0건", loops, contentious, loops)
}

func TestSlowSupersedeCrossRaceV1201(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	at := time.Now().UTC()
	for round := 0; round < 20; round++ {
		ids, err := r.InsertBatch(ctx, []map[string]any{
			knowledgeRec(fmt.Sprintf("v1201-race-a-%02d", round)),
			knowledgeRec(fmt.Sprintf("v1201-race-b-%02d", round)),
		})
		if err != nil {
			t.Fatalf("round %d InsertBatch: %v", round, err)
		}
		a, b := ids[0], ids[1]
		res := make([]error, 2)
		var wg sync.WaitGroup
		for i, pair := range [][2]int{{a, b}, {b, a}} {
			wg.Add(1)
			go func(i int, oldID, newID int) {
				defer wg.Done()
				_, res[i] = r.Supersede(ctx, oldID, newID, at)
			}(i, pair[0], pair[1])
		}
		wg.Wait()
		wins := 0
		for i, err := range res {
			if err == nil {
				wins++
				continue
			}
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("round %d loser[%d] err=%v want ErrNotFound steward", round, i, err)
			}
		}
		if wins != 1 {
			t.Fatalf("round %d winners=%d want exactly 1 (both=%d is a planted cycle; zero is a lost write)",
				round, wins, 2-wins)
		}

		var supA, supB *int
		if err := r.pool.QueryRow(ctx,
			`SELECT (SELECT superseded_by FROM knowledge WHERE id=$1),
			        (SELECT superseded_by FROM knowledge WHERE id=$2)`, a, b).Scan(&supA, &supB); err != nil {
			t.Fatalf("round %d lineage 조회: %v", round, err)
		}
		if supA != nil && supB != nil && *supA == b && *supB == a {
			t.Fatalf("round %d: 2-cycle 적립 (a→b && b→a)", round)
		}
	}
}

func TestSlowSupersedeChainDepthCapV1201(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	at := time.Now().UTC()
	const hops = 500
	recs := make([]map[string]any, hops)
	for i := range recs {
		recs[i] = knowledgeRec(fmt.Sprintf("v1201-chain-%03d", i))
	}
	ids, err := r.InsertBatch(ctx, recs)
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	for i := 0; i+1 < hops; i++ {
		if _, err := r.Supersede(ctx, ids[i], ids[i+1], at); err != nil {
			t.Fatalf("chain hop %d: %v", i, err)
		}
	}

	_, err = r.WouldCreateCycle(ctx, ids[hops-1], ids[0])
	if err == nil {
		t.Fatal("500홉 체인 꼬리→헤드: no-cycle 판정 — 보수 거부 계약 위반")
	}
	if !strings.Contains(err.Error(), "depth cap") {
		t.Fatalf("err=%v want depth-cap refusal diagnostic", err)
	}

	if _, err := r.Supersede(ctx, ids[hops-1], ids[0], at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tail→head Supersede err=%v want ErrNotFound (refused)", err)
	}
}
