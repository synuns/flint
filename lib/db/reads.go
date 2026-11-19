// reads.go — 항목 참조 계측(schema v4). read 결과·hot 주입 항목의
// read_count·last_read_at 증분. 신규 인덱스 없음(spec §2 — status 인덱스
// 재사용, 개인 규모 시퀀셜 수용). 호출부(cli)는 실패를 조용히 무시한다.
package db

import (
	"context"
	"fmt"
	"time"
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
// (schema v10 — spec §2.1 G4 정합·§2.6 회상 운명의 원천).
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

// BumpRecallScores는 회상 강화(schema v12 — recall append-only): ids 행의
// 회상 이벤트를 recall_events에 1행씩 적립한다(INSERT 전용 — 다중 기기
// 동기화에서 쓰기 충돌 없는 회상 원천. 구 v11 UPDATE knowledge 증분은
// 폐지 — knowledge 행은 회상 경로에서 더 이상 갱신되지 않는다). read 히트
// 경로가 RecordKnowledgeReads와 함께 호출한다(계측 관례 — 실패는 호출부가
// 조용히 무시 §13). 감쇠 유효점수는 이벤트 파생(집계 조인 — 랭킹 전환은
// knowledge 패키지 소관)이고 read_count 캐시(BumpReads)·항목별 이력
// (knowledge_reads)과는 별개 계층이다. 빈 슬라이스 no-op·중복 id는 행
// 기준 1회(DISTINCT — 구 UPDATE id=ANY 계약 승계).
func (r *Repo) BumpRecallScores(ctx context.Context, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO recall_events (knowledge_id)
		 SELECT DISTINCT u FROM unnest($1::int[]) AS u`, ids); err != nil {
		return fmt.Errorf("bump recall scores: %w", err)
	}
	return nil
}

// PruneRecallEvents는 보존기한을 넘은 회상 이벤트 DELETE의 단일 경로다
// (schema v12 append-only 원천의 성장 상한 — R42-D). maintain
// --prune-invalidated --commit이 recall_event_retention_days(기본 90,
// meta 오버라이드)로 위임한다. 삭제 0행은 정상(멱등 재실행). dolt 흡수:
// recall_events는 감사 스테이징(auditStageTables) 대상이라 같은 경로의
// 감사 커밋이 곧바로 흡수한다(계측 테이블과 달리 별도 회수 불필요).
// 랭킹 정합(EventEffectiveScore = count−rate×days_since(last)): rate
// 기본 1.0에서 마지막 회상이 보존기한(90일)+경과한 행은 유효 점수가
// 이미 0 하한으로 소멸해 정리가 랭킹을 바꾸지 않는다. 최근 회상이 있는
// 행은 보존 창 내 이벤트만 카운트되는 창 상한 효과(의도된 성장 경계).
func (r *Repo) PruneRecallEvents(ctx context.Context, before time.Time) (int64, error) {
	n, err := transitionExecFn(ctx, r,
		`DELETE FROM recall_events WHERE recalled_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("prune_recall_events: %w", err)
	}
	return n, nil
}

// RecallReplay — 이식 스코어 재생 1행분(import --format jsonl — R42-A):
// 적립 완료 행 id와 이식 원천의 스코어·최종 회상 시각(0값=now() 폴백).
type RecallReplay struct {
	ID           int
	Score        int
	LastRecalled time.Time
}

// ReplayRecallEvents — 이식된 행들의 회상 이력을 합성 이벤트로 재생한다
// (import --format jsonl 전용 — R42-A). v12에서 knowledge의
// recall_score·last_recalled_at 2열은 폐기돼 INSERT 컬럼으로 이관할 수
// 없어, InsertBatch 커밋 후 jsonl이 실어온 (score, last) 쌍을
// EnsureSchemaV12 백필과 동일 산식(score수만큼 1행씩, recalled_at=
// COALESCE(last, now()))으로 recall_events에 적립한다 — 파생 랭킹(cnt·
// last_at)이 원천 DB와 동등하게 복원된다. score<=0 행은 재생 생략(백필
// WHERE 관례와 동일), 빈 슬라이스·전부 0은 no-op. 적립분은 감사 커밋
// (Commit)의 recall_events 스테이징에 자동 흡수된다(auditStageTables).
func (r *Repo) ReplayRecallEvents(ctx context.Context, items []RecallReplay) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int, 0, len(items))
	scores := make([]int, 0, len(items))
	lasts := make([]*time.Time, 0, len(items))
	for _, it := range items {
		if it.ID <= 0 || it.Score <= 0 {
			continue // score 0·키 부재 행은 재생 생략(원천 미회상)
		}
		ids = append(ids, it.ID)
		scores = append(scores, it.Score)
		var last *time.Time
		if !it.LastRecalled.IsZero() {
			t := it.LastRecalled.UTC()
			last = &t
		}
		lasts = append(lasts, last)
	}
	if len(ids) == 0 {
		return nil
	}
	// unnest 3배열 + generate_series — 백필(FROM knowledge k,
	// generate_series(1, k.recall_score) g)과 동일 암묵 lateral 패턴.
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO recall_events (knowledge_id, recalled_at)
		 SELECT u.id, COALESCE(u.last, now())
		 FROM unnest($1::int[], $2::int[], $3::timestamptz[]) AS u(id, score, last),
		      generate_series(1, u.score) g
		 WHERE u.score > 0`, ids, scores, lasts); err != nil {
		return fmt.Errorf("replay recall events: %w", err)
	}
	return nil
}
