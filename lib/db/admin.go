// admin.go — T17 갱신·집계. approve·invalidate·merge는 갱신 전용 허용
// 경로다(전역 제약: write INSERT 전용, projects 제외 UPDATE 금지 —
// 승인/무효화/병합은 spec §8 큐레이션 경로, 사유를 dolt 커밋 메시지에 남긴다).
package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound — 대상 id가 존재하지 않을 때(CLI exit 1 매핑).
var ErrNotFound = errors.New("flint: not found")

// Counts — maintain --check 보고용 집계.
type Counts struct {
	Pending    int // status=pending 지식 수
	DupSuspect int // 같은 topic에 active 2건 이상인 중복 의심 항목 수
}

// gateRetryBackoff — 승격 커밋의 40001(직렬화 실패) 재시도 간격. 병렬
// 기동(예: e2e 8동시 writer)에서는 다른 writer의 승격 커밋과 충돌날 수
// 있어, views 재적용(repo.go Migrate)과 동일한 백오프로 회복한다.
// 재시도 도중 다른 writer가 승격을 완료하면 각 게이트의 수치 비교
// 단락으로 즉시 no-op가 된다(CI run 39717583172).
var gateRetryBackoff = []time.Duration{0, time.Second, 3 * time.Second, 5 * time.Second, 9 * time.Second}

// EnsureSchemaV2는 meta.schema_version<2인 DB를 v2로 무손실 승격한다:
// sessions.tasks·projects.note 컬럼을 ADD COLUMN IF NOT EXISTS로 추가하고
// 버전 2를 기록한다(repo.go Migrate는 v1 소유라 건드리지 않는다).
// 이미 v2 이상이면 트랜잭션·커밋 시도 자체를 하지 않는다(수치 비교
// 단락 — 문자열 동등 검사였을 때는 상위 버전 DB에서도 승격 트랜잭션을
// 열어 버전을 되돌리며 병렬 기동의 40001 창을 넓혔다). 신규 DB(schema.sql
// v2로 생성)에서도 멱등하다.
func (r *Repo) EnsureSchemaV2(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV2(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV2(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v2: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		// meta 테이블 자체가 없다(신규 DB) — 게이트는 스킵하고 Migrate가
		// 스키마를 만들면 2차 게이트(newRealStore)가 승격한다.
		return nil
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 2 {
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

// EnsureSchemaV3는 meta.schema_version<3인 DB를 v3로 무손실 승격한다:
// knowledge 필터 경로(entity·domain·status) B-tree 인덱스 3종을
// CREATE INDEX IF NOT EXISTS로 만들고 버전 3을 기록한다(ADR-0008:
// %q% 무료텍스트는 B-tree가 불가해 시퀀셜 스캔을 수용한다).
// 이미 v3 이상이면 트랜잭션·커밋 없이 no-op(수치 비교 단락)이며,
// v1·v2 게이트를 경유한 어느 DB에서도 멱등하다.
func (r *Repo) EnsureSchemaV3(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV3(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV3(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v3: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // 신규 DB(테이블 부재) — ensureSchemaV2 42P01 주석 참조
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 3 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v3: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS flint_knowledge_entity_idx ON knowledge (entity)`,
		`CREATE INDEX IF NOT EXISTS flint_knowledge_domain_idx ON knowledge (domain)`,
		`CREATE INDEX IF NOT EXISTS flint_knowledge_status_idx ON knowledge (status)`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v3: index: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '3')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v3: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v3: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV4는 meta.schema_version<4인 DB를 v4로 무손실 승격한다:
// knowledge 참조 계측 컬럼(read_count·last_read_at)을 ADD COLUMN IF NOT EXISTS로
// 추가하고 버전 4를 기록한다(spec §2: 신규 인덱스 없음 — status 인덱스 재사용,
// 개인 규모 시퀀셜 수용). 승격 전 기존 행은 기본값 0/null을 받는다.
// 이미 v4면 트랜잭션·커밋 없이 no-op(수치 비교 단락)이며, v1~v3 게이트를
// 경유한 어느 DB에서도 멱등하다.
func (r *Repo) EnsureSchemaV4(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV4(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV4(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v4: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // 신규 DB(테이블 부재) — ensureSchemaV2 42P01 주석 참조
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 4 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v4: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []string{
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS read_count integer NOT NULL DEFAULT 0`,
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS last_read_at timestamptz`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema v4: alter: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '4')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v4: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v4: commit: %w", err)
	}
	return nil
}

// EnsureSchemaV5는 meta.schema_version<5인 DB를 v5로 무손실 승격한다:
// knowledge 계보 컬럼(superseded_by)을 ADD COLUMN IF NOT EXISTS로 추가하고
// 버전 5를 기록한다(spec v0.8 §2: 배치 무효화가 대체 지식 id를 새긴다.
// views 무변경 — 컬럼은 기존 뷰 정의에 영향 없다). 승격 전 기존 행은
// 기본값 null을 받는다. 이미 v5 이상이면 트랜잭션·커밋 없이 no-op
// (수치 비교 단락 — 문자열 비교 금지, v0.6 다운그레이드 사고 재발 방지)이며,
// v1~v4 게이트를 경유한 어느 DB에서도 멱등하다.
func (r *Repo) EnsureSchemaV5(ctx context.Context) error {
	return Retry(ctx, func() error { return r.ensureSchemaV5(ctx) }, gateRetryBackoff)
}

func (r *Repo) ensureSchemaV5(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isPgCode(err, "42P01") {
		return fmt.Errorf("schema v5: check version: %w", err)
	}
	if isPgCode(err, "42P01") {
		return nil // 신규 DB(테이블 부재) — ensureSchemaV2 42P01 주석 참조
	}
	if n, perr := strconv.Atoi(version); perr == nil && n >= 5 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema v5: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS superseded_by int`); err != nil {
		return fmt.Errorf("schema v5: alter: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '5')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		return fmt.Errorf("schema v5: meta: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema v5: commit: %w", err)
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

// DupGroup — maintain --dup-suspects 보고용 중복 그룹(active 동일 topic 묶음).
type DupGroup struct {
	Topic string
	IDs   []int
}

// DupGroups는 active 중 동일 topic 2건 이상 그룹을 반환한다(보고 전용).
// 산식은 Counts의 dup_suspect와 동일한 GROUP BY이며, ids는 오름차순,
// 그룹은 topic 오름차순·limit 상한이다.
func (r *Repo) DupGroups(ctx context.Context, limit int) ([]DupGroup, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT topic, array_agg(id ORDER BY id) FROM knowledge
		WHERE invalid_at IS NULL AND status = 'active'
		GROUP BY topic HAVING count(*) > 1
		ORDER BY topic
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("dup groups: %w", err)
	}
	defer rows.Close()
	groups := []DupGroup{}
	for rows.Next() {
		var g DupGroup
		if err := rows.Scan(&g.Topic, &g.IDs); err != nil {
			return nil, fmt.Errorf("dup groups: scan: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dup groups: rows: %w", err)
	}
	return groups, nil
}

// ConflictGroup — maintain --conflict-suspects 보고용 그룹(DupGroup에
// 행별 요약을 더한 것. summaries는 ids와 동일한 id 오름차순).
type ConflictGroup struct {
	Topic     string
	IDs       []int
	Summaries []string
}

// ConflictGroups는 active 중 동일 topic 2건 이상 그룹을 요약과 함께
// 반환한다(보고 전용). 그룹핑 산식은 DupGroups와 동일하되 요약 유사도
// 분류(conflict-suspects)에 필요한 summary를 같이 실어 올린다 — 기존
// DupGroups 시그니처·출력은 불변(병행 신규 조회).
func (r *Repo) ConflictGroups(ctx context.Context, limit int) ([]ConflictGroup, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT topic, array_agg(id ORDER BY id), array_agg(summary ORDER BY id)
		FROM knowledge
		WHERE invalid_at IS NULL AND status = 'active'
		GROUP BY topic HAVING count(*) > 1
		ORDER BY topic
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("conflict groups: %w", err)
	}
	defer rows.Close()
	groups := []ConflictGroup{}
	for rows.Next() {
		var g ConflictGroup
		if err := rows.Scan(&g.Topic, &g.IDs, &g.Summaries); err != nil {
			return nil, fmt.Errorf("conflict groups: scan: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conflict groups: rows: %w", err)
	}
	return groups, nil
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
