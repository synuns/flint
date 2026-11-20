// pattern.go — internal pattern-detection utilities. Note: the
// `maintain --distill` flag does not exist — these functions are
// internal utilities consumed by no CLI path (the source of a future
// distillation-automation milestone G candidate). The tests verify the
// utilities' own contract.
package db

import (
	"context"
	"fmt"
	"strings"
)

// Pattern — a pattern for playbook-candidate suggestions. Evidence is
// the contributing record's "id=<id>: <summary>" string (observable
// grounds for human/agent approval).
type Pattern struct {
	Topic    string
	Kind     string // "repeat-success" | "fail-then-success"
	Evidence []string
}

// The pattern-candidate cap — preventing maintain-suggestion noise (spec
// §self-growth loop). The SQL uses a '%:...%' wildcard LIKE, so the cap
// is upheld together by the query literal (20) and
// TestPatternCapTwenty.

// patternSQL — picks candidates from per-topic success/execution
// aggregates and bundles the evidence. Kind priority:
// fail-then-success is prioritized over repeat-success as the stronger
// signal, and a transition requires a success later than the last
// failure. Uses CASE aggregation instead of FILTER for Doltgres
// compatibility.
const patternSQL = `
WITH agg AS (
    SELECT topic,
           count(CASE WHEN tags LIKE '%:failure:%' THEN 1 END) AS fails,
           count(CASE WHEN tags NOT LIKE '%:failure:%' THEN 1 END) AS oks,
           max(CASE WHEN tags LIKE '%:failure:%' THEN id END) AS last_fail,
           max(CASE WHEN tags NOT LIKE '%:failure:%' THEN id END) AS last_ok
      FROM knowledge
     WHERE invalid_at IS NULL
     GROUP BY topic
), cand AS (
    SELECT topic,
           CASE WHEN fails > 0 AND last_ok > last_fail THEN 'fail-then-success'
                WHEN oks >= 2 THEN 'repeat-success'
           END AS kind
      FROM agg
)
SELECT c.topic, c.kind,
       (SELECT string_agg('id=' || k.id || ': ' || k.summary, ' | ' ORDER BY k.id)
          FROM knowledge k
         WHERE k.invalid_at IS NULL
           AND k.topic = c.topic
           AND (c.kind = 'fail-then-success'
                OR k.tags NOT LIKE '%:failure:%'))
  FROM cand c
 WHERE c.kind IS NOT NULL
 ORDER BY c.topic
 LIMIT 20`

// PatternCandidates returns playbook distillation candidates (capped at
// 20).
func (r *Repo) PatternCandidates(ctx context.Context) ([]Pattern, error) {
	rows, err := r.pool.Query(ctx, patternSQL)
	if err != nil {
		return nil, fmt.Errorf("pattern candidates: %w", err)
	}
	defer rows.Close()
	var pats []Pattern
	for rows.Next() {
		var p Pattern
		var ev string
		if err := rows.Scan(&p.Topic, &p.Kind, &ev); err != nil {
			return nil, fmt.Errorf("pattern candidates: scan: %w", err)
		}
		p.Evidence = strings.Split(ev, " | ")
		pats = append(pats, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pattern candidates: rows: %w", err)
	}
	return pats, nil
}
