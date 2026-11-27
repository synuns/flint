package db

import (
	"context"
	"fmt"
	"testing"

	"github.com/synuns/flint/lib/store"
)

func seedV1390Projects(t *testing.T, r *Repo, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 1; i <= n; i++ {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO projects (name) VALUES ($1)`, fmt.Sprintf("v1390-demo-%d", i)); err != nil {
			t.Fatalf("project 시딩 %d: %v", i, err)
		}
	}
}

func TestLatestProjectForSessionResolves(t *testing.T) {
	r := newTestRepo(t)
	seedV1390Projects(t, r, 7)
	ctx := context.Background()
	if _, _, err := r.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "v1390 chain older", "session_label": "s-abc",
	}); err != nil {
		t.Fatalf("older open: %v", err)
	}
	if _, _, err := r.OpenWorkState(ctx, map[string]any{
		"project_id": 7, "task": "v1390 chain newer", "session_label": "s-abc",
	}); err != nil {
		t.Fatalf("newer open: %v", err)
	}
	got, err := r.LatestProjectForSession(ctx, "s-abc")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != 7 {
		t.Fatalf("project = %d, want 7(최근 행 해석)", got)
	}
}

func TestLatestProjectForSessionNullNoRetry(t *testing.T) {
	r := newTestRepo(t)
	seedV1390Projects(t, r, 1)
	ctx := context.Background()
	if _, _, err := r.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "v1390 null older", "session_label": "s-null",
	}); err != nil {
		t.Fatalf("older open: %v", err)
	}
	if _, _, err := r.OpenWorkState(ctx, map[string]any{
		"task": "v1390 null newer", "session_label": "s-null",
	}); err != nil {
		t.Fatalf("newer open: %v", err)
	}
	got, err := r.LatestProjectForSession(ctx, "s-null")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != 0 {
		t.Fatalf("project = %d, want 0(최근 행 NULL — 재시도 금지)", got)
	}
}

func TestLatestProjectForSessionEmptySessionZero(t *testing.T) {
	r := newTestRepo(t)
	seedV1390Projects(t, r, 1)
	ctx := context.Background()
	if _, _, err := r.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "v1390 unlabeled",
	}); err != nil {
		t.Fatalf("unlabeled open: %v", err)
	}
	got, err := r.LatestProjectForSession(ctx, "")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != 0 {
		t.Fatalf("project = %d, want 0(빈 라벨 — 라벨 없음 행 매칭 금지)", got)
	}
	got, err = r.LatestProjectForSession(ctx, "s-unknown")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != 0 {
		t.Fatalf("project = %d, want 0(미매칭 라벨)", got)
	}
}

func TestOpenWorkStateRecordsSessionId(t *testing.T) {
	r := newTestRepo(t)
	seedV1390Projects(t, r, 1)
	ctx := context.Background()
	id, res, err := r.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "v1390 labeled open", "session_label": "s-abc",
	})
	if err != nil || res != "open" {
		t.Fatalf("res = %q, err = %v, want open/nil", res, err)
	}
	var label string
	if err := r.pool.QueryRow(ctx,
		`SELECT session_label FROM work_state WHERE id = $1`, id).Scan(&label); err != nil {
		t.Fatalf("session_label 조회: %v", err)
	}
	if label != "s-abc" {
		t.Fatalf("session_label = %q, want s-abc(INSERT 채움)", label)
	}
	if _, res, err := r.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "v1390 labeled open",
	}); err != nil || res != "noop" {
		t.Fatalf("res = %q, err = %v, want noop(기존 계약 유지)", res, err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT session_label FROM work_state WHERE id = $1`, id).Scan(&label); err != nil {
		t.Fatalf("재조회: %v", err)
	}
	if label != "s-abc" {
		t.Fatalf("session_label = %q, want s-abc(noop 경로 라벨 보존)", label)
	}
	id2, res, err := r.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "v1390 bare open",
	})
	if err != nil || res != "open" {
		t.Fatalf("res = %q, err = %v, want open/nil", res, err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT session_label FROM work_state WHERE id = $1`, id2).Scan(&label); err != nil {
		t.Fatalf("미라벨 행 조회: %v", err)
	}
	if label != "" {
		t.Fatalf("session_label = %q, want 빈 값(미제공 — NULL 채움 계약)", label)
	}
}

func TestOpenWorkStateSanitizesSessionLabel(t *testing.T) {
	r := newTestRepo(t)
	seedV1390Projects(t, r, 1)
	ctx := context.Background()
	raw := "s-[flint]abc\x07def"
	id, res, err := r.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "v1390 label sanitize", "session_label": raw,
	})
	if err != nil || res != "open" {
		t.Fatalf("res = %q, err = %v, want open/nil", res, err)
	}
	var label string
	if err := r.pool.QueryRow(ctx,
		`SELECT session_label FROM work_state WHERE id = $1`, id).Scan(&label); err != nil {
		t.Fatalf("session_label 조회: %v", err)
	}
	if want := store.SanitizeText(raw); label != want {
		t.Fatalf("session_label = %q, want 살균 저장 %q", label, want)
	}
}

func TestOpenWorkStateBackfillsSessionLabel(t *testing.T) {
	r := newTestRepo(t)
	seedV1390Projects(t, r, 1)
	ctx := context.Background()
	id, res, err := r.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "v1390 backfill task",
	})
	if err != nil || res != "open" {
		t.Fatalf("res = %q, err = %v, want open/nil", res, err)
	}
	readLabel := func() string {
		t.Helper()
		var label string
		if err := r.pool.QueryRow(ctx,
			`SELECT session_label FROM work_state WHERE id = $1`, id).Scan(&label); err != nil {
			t.Fatalf("session_label 조회: %v", err)
		}
		return label
	}
	if readLabel() != "" {
		t.Fatalf("session_label = %q, want 빈 값(v21 이전 행 모사)", readLabel())
	}
	_, res, err = r.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "v1390 backfill task", "detail": "d2", "session_label": "s-back",
	})
	if err != nil || res != "updated" {
		t.Fatalf("res = %q, err = %v, want updated/nil(재보고 백필)", res, err)
	}
	if got := readLabel(); got != "s-back" {
		t.Fatalf("session_label = %q, want s-back(updated 경로 백필)", got)
	}
	_, res, err = r.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "v1390 backfill task", "detail": "d2", "session_label": "s-back",
	})
	if err != nil || res != "noop" {
		t.Fatalf("res = %q, err = %v, want noop/nil", res, err)
	}
	_, res, err = r.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "v1390 backfill task",
	})
	if err != nil || res != "updated" {
		t.Fatalf("res = %q, err = %v, want updated/nil(detail 누락은 빈 문자열 비교 — 기존 계약)", res, err)
	}
	if got := readLabel(); got != "s-back" {
		t.Fatalf("session_label = %q, want s-back(빈 라벨이 기존 라벨 미서명)", got)
	}
	_, res, err = r.OpenWorkState(ctx, map[string]any{
		"project_id": 1, "task": "v1390 backfill task", "detail": "d3",
	})
	if err != nil || res != "updated" {
		t.Fatalf("res = %q, err = %v, want updated/nil", res, err)
	}
	if got := readLabel(); got != "s-back" {
		t.Fatalf("session_label = %q, want s-back(라벨 없는 UPDATE 경로 보존)", got)
	}
}
