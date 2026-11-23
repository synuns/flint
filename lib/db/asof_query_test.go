package db

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

func t4Rec(hash, domain string) map[string]any {
	rec := knowledgeRec(hash)
	rec["domain"] = domain
	return rec
}

func TestRetryOnce(t *testing.T) {
	t.Run("첫 실패 후 재시도 성공", func(t *testing.T) {
		n := 0
		v, err := retryOnce(func() (string, error) {
			n++
			if n == 1 {
				return "", fmt.Errorf("transient")
			}
			return "ok", nil
		})
		if err != nil || v != "ok" || n != 2 {
			t.Fatalf("retryOnce = (%q, %v) attempts %d, want (\"ok\", nil) 2회", v, err, n)
		}
	})
	t.Run("연속 실패 — 1회만 재시도", func(t *testing.T) {
		n := 0
		_, err := retryOnce(func() (int, error) {
			n++
			return 0, fmt.Errorf("down")
		})
		if err == nil || n != 2 {
			t.Fatalf("retryOnce 연속 실패 = %v attempts %d, want 오류 2회", err, n)
		}
	})
	t.Run("첫 성공 — 재시도 없음", func(t *testing.T) {
		n := 0
		v, err := retryOnce(func() (int, error) {
			n++
			return 7, nil
		})
		if err != nil || v != 7 || n != 1 {
			t.Fatalf("retryOnce 첫 성공 = (%d, %v) attempts %d, want (7, nil) 1회", v, err, n)
		}
	})
}

func TestQueryAsOfSlowAssembly(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{
		t4Rec("v0180-t4-a", "main"),
		t4Rec("v0180-t4-b", "main"),
		t4Rec("v0180-t4-o", "other"),
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	if _, err := r.pool.Exec(ctx, `UPDATE knowledge SET status = 'pending'`); err != nil {
		t.Fatalf("seed status pending: %v", err)
	}
	h, err := r.Commit(ctx, "flint v0180 t4 demo seed")
	if err != nil || h == "" {
		t.Fatalf("Commit: %v (hash %q)", err, h)
	}

	got, err := r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "knowledge", Columns: []string{"count(*)"}})
	if err != nil {
		t.Fatalf("QueryAsOf(count): %v", err)
	}
	if len(got) != 1 || fmt.Sprint(got[0][0]) != "3" {
		t.Fatalf("QueryAsOf(count) = %v, want [[3]]", got)
	}

	got, err = r.QueryAsOf(ctx, wire.AsOfQuery{
		Hash:    h,
		Table:   "knowledge",
		Columns: []string{"topic"},
		Where:   "WHERE domain = $1 ORDER BY topic ASC",
		Args:    []any{"main"},
		Limit:   2,
	})
	if err != nil {
		t.Fatalf("QueryAsOf(where): %v", err)
	}
	if len(got) != 2 || fmt.Sprint(got[0][0]) != "토픽-v0180-t4-a" || fmt.Sprint(got[1][0]) != "토픽-v0180-t4-b" {
		t.Fatalf("QueryAsOf(where+order) = %v, want main 2건 topic ASC", got)
	}

	got, err = r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "knowledge",
		Columns: []string{"count(*)"}, Where: "status = 'pending' AND invalid_at IS NULL"})
	if err != nil {
		t.Fatalf("QueryAsOf(조건-only Where): %v", err)
	}
	if len(got) != 1 || fmt.Sprint(got[0][0]) != "3" {
		t.Fatalf("QueryAsOf(조건-only Where) = %v, want [[3]] (시드 3건 전부 pending)", got)
	}

	got, err = r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "active_knowledge", Columns: []string{"count(*)"}})
	if err != nil {
		t.Fatalf("QueryAsOf(view count): %v", err)
	}
	if len(got) != 1 || fmt.Sprint(got[0][0]) != "3" {
		t.Fatalf("QueryAsOf(view count) = %v, want [[3]]", got)
	}

	if _, err := r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "sessions", Columns: []string{"count(*)"}}); err == nil ||
		!strings.Contains(err.Error(), "sessions") {
		t.Fatalf("QueryAsOf(화이트리스트 외) 오류 없음/메시지 부재: %v", err)
	}

	for _, bad := range []string{"ABC12345", "abc", "v0180;--"} {
		_, err := r.QueryAsOf(ctx, wire.AsOfQuery{Hash: bad, Table: "knowledge", Columns: []string{"count(*)"}})
		if err == nil {
			t.Fatalf("QueryAsOf(해시 %q) 오류 없음", bad)
		}
		if !strings.Contains(err.Error(), "as-of hash charset violation") {
			t.Fatalf("QueryAsOf(해시 %q) 오류 문구 = %q, want 원문 'as-of hash charset violation'", bad, err)
		}
	}

	for _, bad := range []string{"topic; DELETE FROM knowledge", "Topic", "topic--x"} {
		if _, err := r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "knowledge", Columns: []string{bad}}); err == nil {
			t.Fatalf("QueryAsOf(컬럼 %q) 오류 없음", bad)
		}
	}
}

func TestQueryAsOfSlowViewFallback(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{t4Rec("v0180-t4-fb", "main")}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	hA, err := r.Commit(ctx, "flint v0180 t4 demo fallback base")
	if err != nil || hA == "" {
		t.Fatalf("Commit: %v (hash %q)", err, hA)
	}
	const extra = "v0180t4extra"
	dropExtra := func() { _, _ = r.pool.Exec(ctx, "ALTER TABLE knowledge DROP COLUMN "+extra) }

	dropExtra()
	if _, err := r.pool.Exec(ctx, "ALTER TABLE knowledge ADD COLUMN "+extra+" text"); err != nil {
		t.Fatalf("alter add column: %v", err)
	}
	t.Cleanup(func() { dropExtra() })

	t.Cleanup(func() {
		if err := reapplyViewsFn(r, ctx); err != nil {
			t.Errorf("active_knowledge 원본 복원 실패: %v", err)
		}
	})

	redefineView := func() {
		t.Helper()
		if _, err := r.pool.Exec(ctx,
			"CREATE OR REPLACE VIEW active_knowledge AS SELECT id, topic, "+extra+" FROM knowledge WHERE invalid_at IS NULL"); err != nil {
			t.Fatalf("view redefine: %v", err)
		}
	}

	viewQ := wire.AsOfQuery{Hash: hA, Table: "active_knowledge", Columns: []string{"id", "topic"}}

	redefineView()
	if _, err := r.QueryAsOf(ctx, viewQ); err == nil || !strings.Contains(err.Error(),
		"try an AS OF query on the base table (knowledge)") {
		t.Fatalf("QueryAsOf(뷰 비호환) = %v, want 안내 문구 랩 오류", err)
	}

	redefineView()
	if rows, err := r.QueryAsOf(ctx, viewQ); err == nil || !strings.Contains(err.Error(),
		"try an AS OF query on the base table (knowledge)") {
		t.Fatalf("QueryAsOf(폴백 제거 회귀) = (%v, %v), want 안내 문구 랩 오류(base 재시도 금지)", rows, err)
	}

	redefineView()
	if _, err := r.QueryAsOf(ctx, viewQ); err == nil || !strings.Contains(err.Error(),
		"try an AS OF query on the base table (knowledge)") {
		t.Fatalf("QueryAsOf(재현) = %v, want 안내 문구 랩 오류", err)
	}
}

func TestActiveKnowledgeRestoredAfterViewFallback(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	plantStaleView(t, r.dsn)
	if r.viewsCurrent(ctx, viewsSQLFn()) {
		t.Fatal("실험 정의 오염이 views 게이트에 잡히지 않음 — 검증 전제 위반")
	}

	if err := reapplyViewsFn(r, ctx); err != nil {
		t.Fatalf("active_knowledge 원본 복원 실패: %v", err)
	}
	if !r.viewsCurrent(ctx, viewsSQLFn()) {
		t.Fatal("active_knowledge가 배포 정의가 아니다 — ViewFallback 테스트 원본 복원 누락")
	}
}
