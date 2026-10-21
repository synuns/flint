// admin.go — T17 갱신·집계. approve·invalidate·merge는 갱신 전용 허용
// 경로다(전역 제약: write INSERT 전용, projects 제외 UPDATE 금지 —
// 승인/무효화/병합은 spec §8 큐레이션 경로, 사유를 dolt 커밋 메시지에 남긴다).
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound — 대상 id가 존재하지 않을 때(CLI exit 1 매핑).
var ErrNotFound = errors.New("flint: not found")

// Counts — maintain --check 보고용 집계.
type Counts struct {
	Pending    int // status=pending 지식 수
	DupSuspect int // 같은 topic에 active 2건 이상인 중복 의심 항목 수
}

// EnsureSchemaV2는 meta.schema_version<2인 DB를 v2로 무손실 승격한다:
// sessions.tasks·projects.note 컬럼을 ADD COLUMN IF NOT EXISTS로 추가하고
// 버전 2를 기록한다(repo.go Migrate는 v1 소유라 건드리지 않는다).
// 이미 v2면 no-op이며, 신규 DB(schema.sql v2로 생성)에서도 멱등하다.
func (r *Repo) EnsureSchemaV2(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v2: check version: %w", err)
	}
	if version == "2" {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v2: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS tasks text`,
		`ALTER TABLE projects ADD COLUMN IF NOT EXISTS note text`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v2: alter: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '2')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v2: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v2: commit: %w", err)
	}
	return nil
}

// Approve는 pending 지식을 active로 승격한다(spec §8: 승인 = flint approve).
// 이미 active면 멱등 no-op, 없는 id는 ErrNotFound.
func (r *Repo) Approve(ctx context.Context, id int) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET status = 'active' WHERE id = $1 AND status = 'pending'`, id)
	if err != nil {
		return fmt.Errorf("approve: %w", err)
	}
	if ct.RowsAffected() == 1 {
		_, err := r.Commit(ctx, fmt.Sprintf(
			"approve: id=%d pending→active (승인 갱신 경로) [session:0]", id))
		if err != nil {
			return fmt.Errorf("approve: commit: %w", err)
		}
		return nil
	}
	// 갱신 0행: 멱대상 확인 — active면 멱등 no-op, 없으면 ErrNotFound.
	var status string
	if err := r.pool.QueryRow(ctx,
		`SELECT status FROM knowledge WHERE id = $1`, id).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("approve: lookup: %w", err)
	}
	if status == "active" {
		return nil
	}
	return fmt.Errorf("approve: id=%d 예상 밖 상태 %q", id, status)
}

// Invalidate는 지식에 invalid_at=now를 새긴다(무효화 갱신 경로).
// 이미 무효화됐으면 멱등 no-op, 없는 id는 ErrNotFound.
func (r *Repo) Invalidate(ctx context.Context, id int, reason string) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET invalid_at = now() WHERE id = $1 AND invalid_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("invalidate: %w", err)
	}
	if ct.RowsAffected() == 1 {
		_, err := r.Commit(ctx, fmt.Sprintf(
			"invalidate: id=%d 사유: %s (무효화 갱신 경로) [session:0]", id, reason))
		if err != nil {
			return fmt.Errorf("invalidate: commit: %w", err)
		}
		return nil
	}
	return r.ensureExists(ctx, id, "invalidate")
}

// Merge는 from을 무효화하고 to summary에 병합 표시를 남긴다(중복 정리).
func (r *Repo) Merge(ctx context.Context, fromID, toID int) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET invalid_at = now() WHERE id = $1 AND invalid_at IS NULL`, fromID)
	if err != nil {
		return fmt.Errorf("merge: invalidate from: %w", err)
	}
	if ct.RowsAffected() == 0 {
		if err := r.ensureExists(ctx, fromID, "merge from"); err != nil {
			return err
		}
	}
	marker := fmt.Sprintf(" [merged:from=%d]", fromID)
	ct, err = r.pool.Exec(ctx,
		`UPDATE knowledge SET summary = summary || $2
		 WHERE id = $1 AND summary NOT LIKE '%' || $2`, toID, marker)
	if err != nil {
		return fmt.Errorf("merge: mark to: %w", err)
	}
	if ct.RowsAffected() == 0 {
		if err := r.ensureExists(ctx, toID, "merge to"); err != nil {
			return err
		}
		// 이미 표시돼 있으면 멱등 통과.
	}
	_, err = r.Commit(ctx, fmt.Sprintf(
		"merge: from=%d into=%d (중복 정리 병합 갱신 경로) [session:0]", fromID, toID))
	if err != nil {
		return fmt.Errorf("merge: commit: %w", err)
	}
	return nil
}

// Counts는 pending 수와 중복 의심 수를 센다(maintain --check 입력).
func (r *Repo) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge
		 WHERE status = 'pending' AND invalid_at IS NULL`).Scan(&c.Pending); err != nil {
		return c, fmt.Errorf("counts: pending: %w", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT coalesce(sum(n), 0) FROM (
		    SELECT count(*) AS n FROM knowledge
		    WHERE invalid_at IS NULL AND status = 'active'
		    GROUP BY topic HAVING count(*) > 1
		 ) d`).Scan(&c.DupSuspect); err != nil {
		return c, fmt.Errorf("counts: dup suspect: %w", err)
	}
	return c, nil
}

// StaleSessionRuns는 agent의 최근 세션부터 knowledge write 없이 이어진
// 연속 수다(done 경고 판정). 기록 있는 세션을 만나면 집계가 멈춘다.
func (r *Repo) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT s.id,
		        EXISTS (SELECT 1 FROM knowledge k WHERE k.session_id = s.id)
		 FROM sessions s WHERE s.agent = $1
		 ORDER BY s.ts DESC, s.id DESC`, agent)
	if err != nil {
		return 0, fmt.Errorf("stale runs: %w", err)
	}
	defer rows.Close()
	stale := 0
	for rows.Next() {
		var id int
		var hasKnowledge bool
		if err := rows.Scan(&id, &hasKnowledge); err != nil {
			return 0, fmt.Errorf("stale runs: scan: %w", err)
		}
		if hasKnowledge {
			break
		}
		stale++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("stale runs: rows: %w", err)
	}
	return stale, nil
}

// ensureExists는 id가 knowledge에 있는지 확인하고 없으면 ErrNotFound를 돌려준다.
func (r *Repo) ensureExists(ctx context.Context, id int, op string) error {
	var one int
	err := r.pool.QueryRow(ctx,
		`SELECT 1 FROM knowledge WHERE id = $1`, id).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("%s: lookup: %w", op, err)
	}
	return nil
}
