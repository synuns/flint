// reads_v06_test.go — T62 BumpReads(항목 참조 계측, schema v4) slow 테스트.
package db

import (
	"context"
	"testing"
	"time"
)

// TestBumpReadsIncrements — 증분·last_read_at 갱신·미지정 행 무변화·no-op.
func TestBumpReadsIncrements(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.EnsureSchemaV4(ctx); err != nil {
		t.Fatalf("EnsureSchemaV4: %v", err)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("bump-1"),
		knowledgeRec("bump-2"),
		knowledgeRec("bump-3"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := r.BumpReads(ctx, []int{ids[0], ids[2]}); err != nil {
		t.Fatalf("BumpReads: %v", err)
	}
	var c1, c2, c3 int
	var l1, l3 *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count, last_read_at FROM knowledge WHERE id = $1`, ids[0]).
		Scan(&c1, &l1); err != nil {
		t.Fatalf("read row1: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count FROM knowledge WHERE id = $1`, ids[1]).Scan(&c2); err != nil {
		t.Fatalf("read row2: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count, last_read_at FROM knowledge WHERE id = $1`, ids[2]).
		Scan(&c3, &l3); err != nil {
		t.Fatalf("read row3: %v", err)
	}
	if c1 != 1 || c3 != 1 {
		t.Errorf("read_count = (%d,%d), want (1,1)", c1, c3)
	}
	if c2 != 0 {
		t.Errorf("미지정 행 read_count = %d, want 0", c2)
	}
	if l1 == nil || l3 == nil {
		t.Error("last_read_at 미갱신 (want non-null for bumped rows)")
	}
	// 빈 슬라이스는 no-op — 에러 없이 무변화.
	if err := r.BumpReads(ctx, nil); err != nil {
		t.Fatalf("BumpReads(nil): %v", err)
	}
	if err := r.BumpReads(ctx, []int{}); err != nil {
		t.Fatalf("BumpReads(empty): %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count FROM knowledge WHERE id = $1`, ids[0]).Scan(&c1); err != nil {
		t.Fatalf("re-read row1: %v", err)
	}
	if c1 != 1 {
		t.Errorf("empty BumpReads 후 read_count = %d, want 1 (무변화)", c1)
	}
}

// TestBumpReadsDuplicateIDs — Review Focus(numeric): id=ANY 중복 무결 —
// 슬라이스에 같은 id가 두 번 있어도 행은 1회만 증분된다(WHERE 매칭 기준).
func TestBumpReadsDuplicateIDs(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.EnsureSchemaV4(ctx); err != nil {
		t.Fatalf("EnsureSchemaV4: %v", err)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("bump-dup")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := r.BumpReads(ctx, []int{ids[0], ids[0], ids[0]}); err != nil {
		t.Fatalf("BumpReads with dup ids: %v", err)
	}
	var c int
	if err := r.pool.QueryRow(ctx,
		`SELECT read_count FROM knowledge WHERE id = $1`, ids[0]).Scan(&c); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if c != 1 {
		t.Errorf("중복 id 3회 전달 시 read_count = %d, want 1 (행 기준 1회)", c)
	}
}
