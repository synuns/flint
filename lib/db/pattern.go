package db

import (
	"context"
	"fmt"
	"strings"
)

type Pattern struct {
	Topic    string
	Kind     string
	Evidence []string
}

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
