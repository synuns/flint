// asof_query_test.go — QueryAsOf(wire.AsOfQuery 계약의 Doltgres 구현)
// slow 실DB 회귀. 조립형 AS OF 질의(count(*)·WHERE $N+ORDER BY 꼬리 절)·
// 테이블 화이트리스트·해시 문자셋·컬럼 식별자 검증·뷰(active_knowledge)
// 비호환 시 base 재시도 없는 안내 문구 오류(원본 동치)를 실측한다.
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/synuns/flint/lib/wire"
)

// t4Rec — 도메인 지정 합성 데모 지식(knowledgeRec 기본 domain=main).
func t4Rec(hash, domain string) map[string]any {
	rec := knowledgeRec(hash)
	rec["domain"] = domain
	return rec
}

// TestRetryOnce — 읽기 재시도 1회 계약(spec §7, wire.QueryOnce 동등의 db
// 구현 사본). 첫 실패만 재실행(성공·최종 실패 모두 2회 상한).
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

// TestQueryAsOfSlowAssembly — 조립·검증 경로. 시드: main 2건+other 1건
// 커밋 후 그 시점으로 질의.
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
	// 시드를 T8(status --as-of) 실경로와 동일하게 — status 컬럼 기본값은
	// 'active'이므로 pending으로 바꾼 뒤 커밋한다.
	if _, err := r.pool.Exec(ctx, `UPDATE knowledge SET status = 'pending'`); err != nil {
		t.Fatalf("seed status pending: %v", err)
	}
	h, err := r.Commit(ctx, "flint v0180 t4 demo seed")
	if err != nil || h == "" {
		t.Fatalf("Commit: %v (hash %q)", err, h)
	}

	// count(*) — 스칼라 1행.
	got, err := r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "knowledge", Columns: []string{"count(*)"}})
	if err != nil {
		t.Fatalf("QueryAsOf(count): %v", err)
	}
	if len(got) != 1 || fmt.Sprint(got[0][0]) != "3" {
		t.Fatalf("QueryAsOf(count) = %v, want [[3]]", got)
	}

	// WHERE $1 + ORDER BY 꼬리 절 + LIMIT 리터럴 — Args 순서 바인딩.
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

	// 조건-only Where 폼 — status(T8) 경로 재현. 절 키워드 없는 조건은
	// 조립부가 " WHERE " 접두를 붙여야 한다(붙지 않으면
	// "... AS OF 'h' status = ..." 문법 오류 42601 — CI run 44225427183).
	got, err = r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "knowledge",
		Columns: []string{"count(*)"}, Where: "status = 'pending' AND invalid_at IS NULL"})
	if err != nil {
		t.Fatalf("QueryAsOf(조건-only Where): %v", err)
	}
	if len(got) != 1 || fmt.Sprint(got[0][0]) != "3" {
		t.Fatalf("QueryAsOf(조건-only Where) = %v, want [[3]] (시드 3건 전부 pending)", got)
	}

	// 뷰 경로 — active_knowledge AS OF 정상 조회(전 행 active).
	got, err = r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "active_knowledge", Columns: []string{"count(*)"}})
	if err != nil {
		t.Fatalf("QueryAsOf(view count): %v", err)
	}
	if len(got) != 1 || fmt.Sprint(got[0][0]) != "3" {
		t.Fatalf("QueryAsOf(view count) = %v, want [[3]]", got)
	}

	// 화이트리스트 외 테이블 — 거부(sessions는 실재 테이블이지만 불허).
	if _, err := r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "sessions", Columns: []string{"count(*)"}}); err == nil ||
		!strings.Contains(err.Error(), "sessions") {
		t.Fatalf("QueryAsOf(화이트리스트 외) 오류 없음/메시지 부재: %v", err)
	}

	// 무효 해시 — 대문자·과단 접두어 거부 + 원문 오류 문구 정합(read.go).
	for _, bad := range []string{"ABC12345", "abc", "v0180;--"} {
		_, err := r.QueryAsOf(ctx, wire.AsOfQuery{Hash: bad, Table: "knowledge", Columns: []string{"count(*)"}})
		if err == nil {
			t.Fatalf("QueryAsOf(해시 %q) 오류 없음", bad)
		}
		if !strings.Contains(err.Error(), "as-of hash charset violation") {
			t.Fatalf("QueryAsOf(해시 %q) 오류 문구 = %q, want 원문 'as-of hash charset violation'", bad, err)
		}
	}

	// 무효 컬럼 식별자 — 함수·별칭 문자셋 밖 거부.
	for _, bad := range []string{"topic; DELETE FROM knowledge", "Topic", "topic--x"} {
		if _, err := r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "knowledge", Columns: []string{bad}}); err == nil {
			t.Fatalf("QueryAsOf(컬럼 %q) 오류 없음", bad)
		}
	}
}

// TestQueryAsOfSlowViewFallback — 뷰 비호환 안내 오류(폴백 없음, 원본
// 동치). 시드 커밋(hA) 후 베이스에 컬럼을 추가하고 뷰를 그 컬럼을 참조
// 하도록 재정의(커밋 불필요 — 뷰는 항상 현재 정의). hA 시점 행에는 해당
// 컬럼이 없어 뷰 AS OF 질의가 "column ... does not exist"로 실패하고,
// base(knowledge) 재시도 없이 즉시 안내 문구 랩 오류를 반환해야 한다
// (base는 invalid_at 필터가 없어 재시도 성공은 무효 지식 노출이다).
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
	// 직전 실행 잔여 컬럼 회수(부재 DROP 오류는 무시 — Doltgres는
	// DROP COLUMN IF EXISTS 미지원) 후 추가해 멱등 시딩.
	dropExtra()
	if _, err := r.pool.Exec(ctx, "ALTER TABLE knowledge ADD COLUMN "+extra+" text"); err != nil {
		t.Fatalf("alter add column: %v", err)
	}
	t.Cleanup(func() { dropExtra() })
	// 원본 뷰 명시 복원 — 이 테스트가 active_knowledge를 실험 정의로
	// 바꾼 것을 배포 정의로 되돌린다. 후속 테스트 newTestRepo→Migrate의
	// views_sig 불일치 재적용이 사실상 복원하긴 하나(자가 치유), 그 암묵
	// 의존을 제거하고 명시적으로 복원한다 — Migrate와 동일한 재적용 경로
	// (reapplyViews: simple-protocol DROP+CREATE)를 재사용. dropExtra보다
	// 나중에 등록해 LIFO로 먼저 실행 — 복원 정의는 extra 컬럼을 참조하지
	// 않아 컬럼 DROP 순서와 무관하다.
	t.Cleanup(func() {
		if err := reapplyViewsFn(r, ctx); err != nil {
			t.Errorf("active_knowledge 원본 복원 실패: %v", err)
		}
	})
	// 뷰 재정의 헬퍼 — Doltgres는 뷰 AS OF 오류 발화 후 유사 질의의
	// 검증 결과가 캐시되는 양상이 있어(실측), 단계마다 재정의로 초기화한다.
	redefineView := func() {
		t.Helper()
		if _, err := r.pool.Exec(ctx,
			"CREATE OR REPLACE VIEW active_knowledge AS SELECT id, topic, "+extra+" FROM knowledge WHERE invalid_at IS NULL"); err != nil {
			t.Fatalf("view redefine: %v", err)
		}
	}
	// 뷰 질의 단계 공통 — fresh 정의에서 hA 시점 뷰 질의는 항상 안내
	// 오류여야 한다(①·③ 샌드위치로 재현성 확인, ②는 폴백 시대에 base
	// 재시도로 행을 돌려준 경로 — 회귀 단언의 핵심).
	viewQ := wire.AsOfQuery{Hash: hA, Table: "active_knowledge", Columns: []string{"id", "topic"}}

	// ① 안내 문구 랩 오류(원문 보존).
	redefineView()
	if _, err := r.QueryAsOf(ctx, viewQ); err == nil || !strings.Contains(err.Error(),
		"try an AS OF query on the base table (knowledge)") {
		t.Fatalf("QueryAsOf(뷰 비호환) = %v, want 안내 문구 랩 오류", err)
	}

	// ② 회귀 — 폴백 전면 제거: 뷰 오류가 나도 base(knowledge) 재시도로
	// 행을 돌려주면 안 된다(무효 지식 노출). 안내 문구 랩 오류여야 한다.
	redefineView()
	if rows, err := r.QueryAsOf(ctx, viewQ); err == nil || !strings.Contains(err.Error(),
		"try an AS OF query on the base table (knowledge)") {
		t.Fatalf("QueryAsOf(폴백 제거 회귀) = (%v, %v), want 안내 문구 랩 오류(base 재시도 금지)", rows, err)
	}

	// ③ 재현성 — fresh 정의에서 다시 랩 오류(샌드위치 하단).
	redefineView()
	if _, err := r.QueryAsOf(ctx, viewQ); err == nil || !strings.Contains(err.Error(),
		"try an AS OF query on the base table (knowledge)") {
		t.Fatalf("QueryAsOf(재현) = %v, want 안내 문구 랩 오류", err)
	}
}

// TestActiveKnowledgeRestoredAfterViewFallback — TestQueryAsOfSlowViewFallback
// 이 active_knowledge를 배포 정의로 복원했는지 검증(명시적 복원 회귀).
// 복원 cleanup이 없던 시대에는 뷰가 실험 정의(extra 컬럼)로 남아 이
// 단언이 실패한다. newTestRepo를 쓰지 않는다 — 그 안의 Migrate가 뷰를
// 재적용(자가 치유)해 복원 누락을 가리기 때문. 원시 접속으로 뷰 최신성
// (views_hash·views_sig 이중 게이트)을 그대로 본다.
func TestActiveKnowledgeRestoredAfterViewFallback(t *testing.T) {
	unlock := sharedDBLock(t)
	t.Cleanup(unlock)
	useFakeSQL(t)
	ctx := context.Background()
	r, err := NewRepo(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	t.Cleanup(r.Close)
	// meta에 뷰 지문 기록이 없는 완전 fresh DB에서는 비교 재료가 없다 —
	// 최초 기록(Migrate) 후에만 판정한다(이 실행에 폴백 테스트가 오염시킬
	// 경로는 없다). 기록이 있는데 최신이 아니면 복원 누락이다.
	var recorded string
	switch err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'views_hash'`).Scan(&recorded); {
	case err == nil:
	case errors.Is(err, pgx.ErrNoRows) || isPgCode(err, "42P01"):
		if err := r.Migrate(ctx); err != nil {
			t.Fatalf("fresh Migrate: %v", err)
		}
	default:
		t.Fatalf("views_hash 조회: %v", err)
	}
	if !r.viewsCurrent(ctx, viewsSQLFn()) {
		t.Fatal("active_knowledge가 배포 정의가 아니다 — ViewFallback 테스트 원본 복원 누락")
	}
}
