// admin_v1201_test.go — v1.20.1 patch slow tests (real DB, synthetic
// demo rows only). Covers the schema v15 uniqueness seal (r86): the
// v14→v15 gate collapsing pre-seal duplicate link rows and creating
// uq_knowledge_links_triple; the race-proof idempotent INSERT (ON
// CONFLICT — 5 parallel writers accrue 1 row); the serialized cross
// supersede (r87 — parallel A→B·B→A never both land); and the
// conservative depth-cap refusal of the lineage walk (r87 — a
// 500-hop chain's tail→head supersede is refused, not "safe no-cycle").
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSlowV15GateDedupeUniqueIndex — promotes a reproduced v14 DB (with
// pre-seal duplicate link rows) to v15: duplicates collapse to the
// lowest id per (from,to,relation)·the unique index exists·version
// 15·the rerun idempotent.
func TestSlowV15GateDedupeUniqueIndex(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// Reproduce a faithful v14 DB — drop the v15 seal the fresh
	// schema.sql terminus already carries.
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
	// Two pre-seal duplicate rows plus one distinct relation.
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
	if n != 2 { // refines collapsed to 1 + supports 1
		t.Fatalf("링크 수=%d want 2 (duplicates collapsed)", n)
	}
	// The seal is behavioral (Doltgres exposes no
	// information_schema.indexes): a duplicate triple INSERT is now
	// refused by the unique index (23505).
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge_links (from_id, to_id, relation) VALUES ($1, $2, 'refines')`,
		ids[0], ids[1]); err == nil {
		t.Fatal("중복 triple INSERT 허용 — unique 봉인 미적용")
	}
	// The rerun is idempotent — version 15 kept·no error.
	if err := r.EnsureSchemaV15(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV15 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "15" {
		t.Fatalf("재호출 후 schema_version=%q want 15", v)
	}
}

// TestSlowParallelLinkInsertSingleRowV1201 — r86: 5 parallel writers
// INSERT the same (from,to,relation) with ON CONFLICT DO NOTHING —
// exactly 1 row (the pre-patch NOT EXISTS guard left a
// check-then-insert window that accrued duplicates).
func TestSlowParallelLinkInsertSingleRowV1201(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v1201-par-link-a"), knowledgeRec("v1201-par-link-b"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = r.pool.Exec(ctx,
				`INSERT INTO knowledge_links (from_id, to_id, relation)
				 VALUES ($1, $2, $3)
				 ON CONFLICT (from_id, to_id, relation) DO NOTHING`,
				ids[0], ids[1], "refines")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("병렬 INSERT[%d]: %v", i, err)
		}
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

// TestSlowSupersedeCrossRaceV1201 — r87: 20 rounds of the cross
// supersede race (A→B ∥ B→A on fresh active rows). The advisory-lock
// serialization + the embedded cycle guard must yield EXACTLY ONE
// winner per round; the loser gets the 0-rows ErrNotFound steward —
// a planted 2-cycle (both succeed) is the patched defect.
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
		// The planted lineage must read as a chain, never a loop.
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

// TestSlowSupersedeChainDepthCapV1201 — r87: a 500-hop linear chain;
// supersede(chain tail → head) would close a loop only visible beyond
// the 64-depth walk cap. WouldCreateCycle must refuse conservatively
// (an error), not return the old "safe no-cycle" verdict.
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
	// Build the linear chain i → i+1 (each hop is cycle-free).
	for i := 0; i+1 < hops; i++ {
		if _, err := r.Supersede(ctx, ids[i], ids[i+1], at); err != nil {
			t.Fatalf("chain hop %d: %v", i, err)
		}
	}
	// tail → head: the walk from head reaches the cap before the tail.
	_, err = r.WouldCreateCycle(ctx, ids[hops-1], ids[0])
	if err == nil {
		t.Fatal("500홉 체인 꼬리→헤드: no-cycle 판정 — 보수 거부 계약 위반")
	}
	if !strings.Contains(err.Error(), "depth cap") {
		t.Fatalf("err=%v want depth-cap refusal diagnostic", err)
	}
	// The guarded UPDATE path refuses too (0 rows → ErrNotFound).
	if _, err := r.Supersede(ctx, ids[hops-1], ids[0], at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tail→head Supersede err=%v want ErrNotFound (refused)", err)
	}
}
