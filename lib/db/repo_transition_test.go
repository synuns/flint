// repo_transition_test.go — v0.41.0 T2: knowledge 상태 전이 UPDATE의 repo
// 단일화(Invalidate·Supersede — rows-affected 후견사 내장). fake는
// transitionExecFn seam으로 SQL·파라미터를 가로채고(SUPERLITE_REAL=1이면
// 실구현 — 실경로 왕복은 slow 실DB 라인이 검증한다).
package db

import (
	"context"
	"errors"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// transitionRecorder — fake 실행 seam 기록(호출별 SQL·파라미터)과
// 스크립트된 rows-affected.
type transitionRecorder struct {
	calls []transitionCall
	n     int64 // 각 호출이 돌려줄 rows-affected(기본 1)
}

type transitionCall struct {
	sql  string
	args []any
}

// useFakeTransitionExec — transitionExecFn seam 교체. SUPERLITE_REAL=1이면
// 실구현을 그대로 둔다(시험 seam 체크 — 그 경우 호출자는 slow 라인으로).
func useFakeTransitionExec(t *testing.T) *transitionRecorder {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") == "1" {
		return nil
	}
	rec := &transitionRecorder{n: 1}
	prev := transitionExecFn
	transitionExecFn = func(ctx context.Context, r *Repo, query string, args ...any) (int64, error) {
		rec.calls = append(rec.calls, transitionCall{sql: query, args: args})
		return rec.n, nil
	}
	t.Cleanup(func() { transitionExecFn = prev })
	return rec
}

// updateKnowledgeRe — 유일 소스 단언: ops·knowledge 비테스트 소스의
// UPDATE knowledge 리터럴 탐지.
var updateKnowledgeRe = regexp.MustCompile(`(?i)UPDATE\s+knowledge`)

// transitionSources — 전이 위임 대상 소스(maintain 갱신 3경로·approve 갱신).
var transitionSources = []string{
	"../ops/maintain.go",
	"../knowledge/approve.go",
}

// assertNoUpdateKnowledgeLiterals — 소스에 UPDATE knowledge 리터럴 잔여 0
// (전부 Repo 경유 — Review Focus: TestInvalidateSupersedeSingleSource).
func assertNoUpdateKnowledgeLiterals(t *testing.T) {
	t.Helper()
	for _, path := range transitionSources {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("소스 읽기 %s: %v", path, err)
		}
		if loc := updateKnowledgeRe.FindStringIndex(string(b)); loc != nil {
			t.Errorf("%s에 UPDATE knowledge 리터럴 잔여(오프셋 %d) — Repo 단일화 위반", path, loc[0])
		}
	}
}

// TestInvalidateSupersedeSingleSource — Review Focus: 전이 UPDATE는 Repo
// 단일 경로 — ① Invalidate·Supersede SQL 병기 단언(fake repo 레벨)
// ② maintain·approve 소스에 UPDATE knowledge 리터럴 0.
func TestInvalidateSupersedeSingleSource(t *testing.T) {
	assertNoUpdateKnowledgeLiterals(t)
	if os.Getenv("SUPERLITE_REAL") == "1" {
		t.Skip("REAL 모드 — SQL 캡처는 fake seam 전제(실경로는 DB 라인이 검증)")
	}
	rec := useFakeTransitionExec(t)
	r := &Repo{}
	ctx := context.Background()

	n, err := r.Invalidate(ctx, []int{1, 2}, "invalidate-stale")
	if err != nil || n != 1 {
		t.Fatalf("Invalidate = (%d, %v), want (1, nil)", n, err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("전이 실행 %d회, want 1", len(rec.calls))
	}
	if sqlText := rec.calls[0].sql; !strings.Contains(sqlText, "status = 'invalidated'") ||
		!strings.Contains(sqlText, "invalid_at = now()") ||
		!strings.Contains(sqlText, "WHERE id = ANY($1)") {
		t.Errorf("Invalidate SQL 병기 미충족: %q", sqlText)
	}
	if len(rec.calls[0].args) != 1 || !reflect.DeepEqual(rec.calls[0].args[0], []int{1, 2}) {
		t.Errorf("Invalidate 파라미터=%v, want [[1 2]]", rec.calls[0].args)
	}

	at := time.Now().Add(-24 * time.Hour)
	n, err = r.Supersede(ctx, 3, 7, at)
	if err != nil || n != 1 {
		t.Fatalf("Supersede = (%d, %v), want (1, nil)", n, err)
	}
	if len(rec.calls) != 2 {
		t.Fatalf("전이 실행 %d회, want 2", len(rec.calls))
	}
	if supSQL := rec.calls[1].sql; !strings.Contains(supSQL, "status = 'invalidated'") ||
		!strings.Contains(supSQL, "superseded_by = $2") ||
		!strings.Contains(supSQL, "invalid_at = $3") ||
		!strings.Contains(supSQL, "WHERE id = $1 AND status = 'active' AND invalid_at IS NULL") {
		t.Errorf("Supersede SQL 병기 미충족: %q", supSQL)
	}
	if !reflect.DeepEqual(rec.calls[1].args, []any{3, 7, at}) {
		t.Errorf("Supersede 파라미터=%v, want [3 7 %v]", rec.calls[1].args, at)
	}

	// 0행 후견사 — 경합 무음 성공 금지(ErrNotFound 랩: 기존 supersede
	// 후견사 계약 승계).
	rec.n = 0
	if _, err := r.Invalidate(ctx, []int{9}, "경합"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Invalidate 0행 오류 = %v, want ErrNotFound 랩", err)
	}
	if _, err := r.Supersede(ctx, 3, 7, at); !errors.Is(err, ErrNotFound) {
		t.Errorf("Supersede 0행 오류 = %v, want ErrNotFound 랩", err)
	}
}

// TestSupersedeRecordsLineage — Review Focus: 계보는 superseded_by 병기
// 단일 모델(텍스트 마커 이중 모델 폐지). fake — SQL이 superseded_by를
// 기록하고 summary 텍스트 마커를 남기지 않음. REAL — 실DB에서
// superseded_by·invalid_at=NEW.valid_at 기록 확인.
func TestSupersedeRecordsLineage(t *testing.T) {
	if os.Getenv("SUPERLITE_REAL") == "1" {
		testSupersedeRecordsLineageDB(t)
		return
	}
	rec := useFakeTransitionExec(t)
	at := time.Now().Add(-24 * time.Hour)
	if _, err := (&Repo{}).Supersede(context.Background(), 11, 12, at); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("전이 실행 %d회, want 1", len(rec.calls))
	}
	if !strings.Contains(rec.calls[0].sql, "superseded_by = $2") {
		t.Errorf("계보 미기록: %q", rec.calls[0].sql)
	}
	if strings.Contains(rec.calls[0].sql, "merged:from") ||
		strings.Contains(rec.calls[0].sql, "summary") {
		t.Errorf("텍스트 마커 이중 모델 잔여: %q", rec.calls[0].sql)
	}
}

// testSupersedeRecordsLineageDB — slow 실DB 라인(seam 체크 — REAL 모드).
func testSupersedeRecordsLineageDB(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	newAt := time.Now().Add(-2 * time.Hour).Truncate(time.Microsecond)
	newRec := knowledgeRec("super-new")
	newRec["valid_at"] = newAt
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("super-old"), newRec,
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	oldID, newID := ids[0], ids[1]
	if _, err := r.Supersede(ctx, oldID, newID, newAt); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	var status string
	var supBy *int
	var invalidAt *time.Time
	var summary string
	if err := r.pool.QueryRow(ctx,
		`SELECT status, superseded_by, invalid_at, summary FROM knowledge WHERE id = $1`, oldID).
		Scan(&status, &supBy, &invalidAt, &summary); err != nil {
		t.Fatalf("조회: %v", err)
	}
	if status != "invalidated" || supBy == nil || *supBy != newID {
		t.Fatalf("계보 미기록: status=%s superseded_by=%v", status, supBy)
	}
	if invalidAt == nil || !invalidAt.Equal(newAt) {
		t.Fatalf("invalid_at=%v, want NEW.valid_at %v", invalidAt, newAt)
	}
	if strings.Contains(summary, "merged:from") {
		t.Error("텍스트 마커 이중 모델 — summary에 merged 마커 잔여")
	}
	// 재호출(이미 invalidated) — 0행 후견사.
	if _, err := r.Supersede(ctx, oldID, newID, newAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("0행 재시도 오류 = %v, want ErrNotFound 랩", err)
	}
}
