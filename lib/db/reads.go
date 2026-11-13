// reads.go — T62 항목 참조 계측(schema v4). read 결과·hot 주입 항목의
// read_count·last_read_at 증분. 신규 인덱스 없음(spec §2 — status 인덱스
// 재사용, 개인 규모 시퀀셜 수용). 호출부(cli)는 실패를 조용히 무시한다.
package db

import (
	"context"
	"fmt"
)

// BumpReads는 ids에 해당하는 knowledge 행의 참조를 1씩 증분한다
// (read_count+1, last_read_at=now()). 빈 슬라이스는 no-op이고, 슬라이스 내
// 중복 id는 WHERE id=ANY의 행 매칭 기준이라 행당 1회만 반영된다.
func (r *Repo) BumpReads(ctx context.Context, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET read_count = read_count + 1, last_read_at = now()
		 WHERE id = ANY($1)`, ids); err != nil {
		return fmt.Errorf("bump reads: %w", err)
	}
	return nil
}

// RecordKnowledgeReads는 항목별 회상 이력 1회분을 knowledge_reads에 남긴다
// (v1.2.0 T2, schema v10 — spec §2.1 G4 정합·§2.6 회상 운명의 원천).
// ids 전수(read 반환 행·hot 주입/헤드라인 노출 행)에 대해 knowledge_id·
// session·kind('read'|'hot' 표면 라벨) 1행씩을 단일 INSERT ... SELECT
// unnest로 적는다(쿼리 본문·요약 미기록 — 개인 문구 부재 원칙). 빈 ids는
// no-op이고 무효 kind는 에러. 이 테이블은 read_count/last_read_at 캐시
// 경로(BumpReads)와 별개의 이력 계층이다 — BumpReads는 불볕.
func (r *Repo) RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error {
	if len(ids) == 0 {
		return nil
	}
	switch kind {
	case "read", "hot":
	default:
		return fmt.Errorf("record knowledge reads: invalid kind %q", kind)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge_reads (knowledge_id, session, kind)
		 SELECT u, $2, $3 FROM unnest($1::int[]) AS u`, ids, session, kind); err != nil {
		return fmt.Errorf("record knowledge reads: %w", err)
	}
	return nil
}
