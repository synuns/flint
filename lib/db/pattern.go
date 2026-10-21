// pattern.go — T36 패턴 탐지(maintain --distill 입력). sessions+knowledge
// 축적에서 같은 topic의 성공 반복·실패→성공 전환을 순수 SQL 집계로 찾는다.
// 실패 판정은 CONTENT.md §4 어휘표의 failure 태그(실패 학습) — failure가
// 아닌 active 지식이 그 topic의 성공 기록이 된다.
package db

import (
	"context"
	"fmt"
	"strings"
)

// Pattern — 플레이북 후보 제안용 패턴. Evidence는 기여 레코드의
// "id=<id>: <summary>" 문자열이다(사람/에이전트 승인용 관측 근거).
type Pattern struct {
	Topic    string
	Kind     string // "repeat-success" | "fail-then-success"
	Evidence []string
}

// 패턴 후보 상한 — maintain 제안 노이즈 방지(spec §자가발전 루프).
// SQL이 LIKE '%:...%' 와일드카드를 쓰므로 상한은 쿼리 리터럴(20)과
// TestPatternCapTwenty이 함께 지킨다.

// patternSQL — topic별 성공/실행 집계에서 후보를 뽑고 증거를 묶는다.
// kind 우선순위: 실패 뒤 성공 전환(fail-then-success)이 반복 성공보다
// 강한 신호라 우선하고, 마지막 실패보다 나중에 성공이 있어야 전환이다.
// Doltgres 호환을 위해 FILTER 대신 CASE 집계를 쓴다.
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

// PatternCandidates는 플레이북 증류 후보를 돌려준다(상한 20건).
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
