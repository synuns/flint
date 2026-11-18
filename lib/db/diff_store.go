// diff_store.go — Diff 계열 3메서드(wire 히스토리 계약의 Doltgres
// 구현). diff 원시 SQL의 유일 소유(lib/ops/diff.go·audit.go에서 이식,
// 바이트 동치 — 파라미터화 승계). 출력 살균(render.Text)은 호출측
// 책임이라 이 층은 원시 값만 반환한다. 질의 실패는 재시도 1회
// (spec §7 — queryRetry 공용 헬퍼, asof_query.go).
package db

import (
	"context"
	"fmt"

	"github.com/synuns/flint/lib/wire"
)

// diffCommitsMax — 요약 커밋 목록 상한(계약: 기본 20). lib/ops/diff.go
// 원문 상수 이관.
const diffCommitsMax = 20

// diffCommitsSQL — from(초과)~to(이하) 커밋 범위의 로그×테이블 diff
// 요약. lib/ops/diff.go 원문 그대로(LEFT JOIN·범위 서브쿼리·GROUP BY·
// DESC·LIMIT 상수 조립).
var diffCommitsSQL = fmt.Sprintf(`SELECT l.commit_hash, l.date, l.message,
  COALESCE(bool_or(d.data_change), false),
  COALESCE(bool_or(d.schema_change), false)
FROM dolt_log l
LEFT JOIN dolt_diff d ON d.commit_hash = l.commit_hash
WHERE l.commit_order > (SELECT commit_order FROM dolt_log WHERE commit_hash = $1)
  AND l.commit_order <= (SELECT commit_order FROM dolt_log WHERE commit_hash = $2)
GROUP BY l.commit_hash, l.commit_order, l.date, l.message
ORDER BY l.commit_order DESC
LIMIT %d`, diffCommitsMax)

// DiffRows — 두 커밋 간 knowledge 행 diff. 원문(lib/ops/diff.go):
// dolt_commit_diff_knowledge to/from 커밋 필터 각 1개(쿼리 계획
// 요구사항 — 실측), added/removed 공용 COALESCE 표현. 인자 순서는
// 원문 승계: $1=to, $2=from.
func (r *Repo) DiffRows(ctx context.Context, fromHash, toHash string) ([]wire.KnowledgeDiffRow, error) {
	rows, err := r.queryRetry(ctx,
		`SELECT diff_type, COALESCE(to_id, from_id), COALESCE(to_topic, from_topic)
FROM dolt_commit_diff_knowledge WHERE to_commit = $1 AND from_commit = $2`,
		toHash, fromHash)
	if err != nil {
		return nil, fmt.Errorf("diff rows: %w", err)
	}
	defer rows.Close()
	var out []wire.KnowledgeDiffRow
	for rows.Next() {
		var d wire.KnowledgeDiffRow
		if err := rows.Scan(&d.DiffType, &d.ID, &d.Topic); err != nil {
			return nil, fmt.Errorf("diff rows: scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DiffStat — 커밋 해시 목록의 테이블별 변경 통계. 원문(lib/ops/audit.go):
// 커밋당 N+1 회피용 ANY($1) 단일 배치 질의. 살균은 호출측 출구와의
// 일관성 유지 목적이므로 이 층은 원시 값 반환.
func (r *Repo) DiffStat(ctx context.Context, hashes []string) ([]wire.TableDiffStat, error) {
	if hashes == nil {
		hashes = []string{}
	}
	rows, err := r.queryRetry(ctx,
		`SELECT commit_hash, table_name, data_change, schema_change
FROM dolt_diff WHERE commit_hash = ANY($1)`, hashes)
	if err != nil {
		return nil, fmt.Errorf("diff stat: %w", err)
	}
	defer rows.Close()
	var out []wire.TableDiffStat
	for rows.Next() {
		var s wire.TableDiffStat
		if err := rows.Scan(&s.CommitHash, &s.Table, &s.DataChange, &s.SchemaChange); err != nil {
			return nil, fmt.Errorf("diff stat: scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DiffCommits — from(초과)~to(이하) 커밋 요약 최신순 상한 20. date는
// parseLogDate 파싱(fake/실DB 레이아웃 공용 — LogEntries 승계).
func (r *Repo) DiffCommits(ctx context.Context, fromHash, toHash string) ([]wire.DiffCommitSummary, error) {
	rows, err := r.queryRetry(ctx, diffCommitsSQL, fromHash, toHash)
	if err != nil {
		return nil, fmt.Errorf("diff commits: %w", err)
	}
	defer rows.Close()
	var out []wire.DiffCommitSummary
	for rows.Next() {
		var c wire.DiffCommitSummary
		var d any
		if err := rows.Scan(&c.Hash, &d, &c.Message, &c.DataChange, &c.SchemaChange); err != nil {
			return nil, fmt.Errorf("diff commits: scan: %w", err)
		}
		t, ok := parseLogDate(d)
		if !ok {
			return nil, fmt.Errorf("diff commits: unparseable date %v", d)
		}
		c.Date = t
		out = append(out, c)
	}
	return out, rows.Err()
}
