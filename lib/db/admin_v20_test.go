package db

import (
	"context"
	"testing"
	"time"
)

func v20SetPre(t *testing.T, r *Repo) context.Context {
	t.Helper()
	ctx := context.Background()
	setVersion(t, r, "19")
	return ctx
}

func v20Gate(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()
	if err := r.EnsureSchemaV20(ctx); err != nil {
		t.Fatalf("EnsureSchemaV20: %v", err)
	}
	if v := readVersion(t, r); v != "20" {
		t.Fatalf("schema_version = %q, want 20", v)
	}
}

func TestV20BackfillClaimsAllExistingRowsAsEvent(t *testing.T) {
	r := newTestRepo(t)
	ctx := v20SetPre(t, r)

	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v20-claim-a"), knowledgeRec("v20-claim-b"), knowledgeRec("v20-claim-c"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("ids %d건, want 3", len(ids))
	}

	v20Gate(t, r, ctx)

	var unclassified int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge WHERE claim = '' OR claim IS NULL`).Scan(&unclassified); err != nil {
		t.Fatalf("미분류 카운트: %v", err)
	}
	if unclassified != 0 {
		t.Fatalf("claim 미분류 %d건 — 기존 행 backfill 누락", unclassified)
	}
	var eventN int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge WHERE claim = 'event'`).Scan(&eventN); err != nil {
		t.Fatalf("event 카운트: %v", err)
	}
	if eventN != 3 {
		t.Fatalf("claim='event' %d건, want 3", eventN)
	}

	if err := r.EnsureSchemaV20(ctx); err != nil {
		t.Fatalf("재실행 EnsureSchemaV20(멱등): %v", err)
	}
}

func TestV20PartialUniqueRejectsSecondOpenSameHash(t *testing.T) {
	r := newTestRepo(t)
	ctx := v20SetPre(t, r)
	v20Gate(t, r, ctx)

	var projectID int
	if err := r.pool.QueryRow(ctx,
		`INSERT INTO projects (name) VALUES ('v20-demo') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatalf("project 생성: %v", err)
	}
	first := `INSERT INTO work_state (project_id, task, state_hash) VALUES ($1, 'v20 demo task', 'hash-v20')`
	if _, err := r.pool.Exec(ctx, first, projectID); err != nil {
		t.Fatalf("1차 open INSERT: %v", err)
	}
	if _, err := r.pool.Exec(ctx, first, projectID); !isPgCode(err, "23505") {
		t.Fatalf("2차 open INSERT err = %v, want unique violation(23505)", err)
	}

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO work_state (project_id, task, state_hash, status) VALUES ($1, 'v20 demo task', 'hash-v20', 'resolved')`,
		projectID); err != nil {
		t.Fatalf("동일 해시 resolved INSERT 허용되어야 함: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO work_state (task, state_hash) VALUES ('v20 orphan task', 'hash-orphan')`); err != nil {
		t.Fatalf("project_id NULL open INSERT: %v", err)
	}
}

func TestV20ViewsExcludeEvent(t *testing.T) {
	r := newTestRepo(t)
	ctx := v20SetPre(t, r)

	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v20-view-decision"), knowledgeRec("v20-view-event"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	v20Gate(t, r, ctx)

	var eventN int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge WHERE claim = 'event'`).Scan(&eventN); err != nil {
		t.Fatalf("backfill event 카운트: %v", err)
	}
	if eventN != 2 {
		t.Fatalf("backfill 후 claim='event' %d건, want 2", eventN)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET claim = 'decision' WHERE id = $1`, ids[0]); err != nil {
		t.Fatalf("decision 재분류: %v", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT id, topic, summary, tags, entity, domain, claim, confidence, conditions
		 FROM recall_knowledge`)
	if err != nil {
		t.Fatalf("recall_knowledge 질의: %v", err)
	}
	defer rows.Close()
	seen := map[int]string{}
	for rows.Next() {
		var id int
		var topic, summary, tags, domain, claim, conditions string
		var entity *string
		var confidence float64
		if err := rows.Scan(&id, &topic, &summary, &tags, &entity, &domain, &claim, &confidence, &conditions); err != nil {
			t.Fatalf("recall_knowledge 스캔: %v", err)
		}
		seen[id] = claim
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("recall_knowledge rows: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("recall_knowledge %d건, want 1(event 제외)", len(seen))
	}
	if got, ok := seen[ids[0]]; !ok || got != "decision" {
		t.Fatalf("recall_knowledge에 decision 행 부재(또는 claim=%q)", got)
	}

	var projectID int
	if err := r.pool.QueryRow(ctx,
		`INSERT INTO projects (name) VALUES ('v20-view-demo') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatalf("project 생성: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO work_state (project_id, task, state_hash) VALUES ($1, 'v20 view task', 'hash-view')`, projectID); err != nil {
		t.Fatalf("open work_state 생성: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO work_state (project_id, task, state_hash, status) VALUES ($1, 'v20 resolved task', 'hash-view-r', 'resolved')`,
		projectID); err != nil {
		t.Fatalf("resolved work_state 생성: %v", err)
	}

	var gotID int
	var gotProjectID *int
	var gotTask, gotDetail, gotNextAction string
	var gotUpdated time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT id, project_id, task, detail, next_action, updated_at FROM open_work_state`).
		Scan(&gotID, &gotProjectID, &gotTask, &gotDetail, &gotNextAction, &gotUpdated); err != nil {
		t.Fatalf("open_work_state 질의: %v", err)
	}
	if gotTask != "v20 view task" {
		t.Fatalf("open_work_state task = %q, want v20 view task(resolved 제외)", gotTask)
	}
	if gotProjectID == nil || *gotProjectID != projectID {
		t.Fatalf("open_work_state project_id = %v, want %d", gotProjectID, projectID)
	}
	if gotDetail != "" || gotNextAction != "" {
		t.Fatalf("open_work_state 기본값 detail=%q next_action=%q, want 빈 문자열", gotDetail, gotNextAction)
	}
}
