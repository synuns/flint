package db

import (
	"context"
	"fmt"

	"github.com/synuns/flint/lib/wire"
)

const diffCommitsMax = 20

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
