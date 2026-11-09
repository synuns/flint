// usage.go — T37 플레이북 사용 로그·성공률. 신규 테이블 금지 제약(schema v2
// 유지)이라 meta 키 pb_<id>_uses/pb_<id>_ok 증분으로 기록한다. 사용 로그는
// 의도적으로 비멱등이다 — 같은 호출이 두 번 세지는 게 결함이 아니라 로그다.
package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Stat — 플레이북별 사용 통계(내부 유틸 — 소비 CLI 경로 없음, v0.25.4 U4 표기 정정).
type Stat struct {
	ID   int
	Uses int
	OK   int
	Rate float64 // OK/Uses, 셋째 자리 반올림(2/3 → 0.667)
}

// LogPlaybookUse는 플레이북 사용 1회를 meta에 증분 기록한다. ok=true면
// uses·ok 둘 다, 아니면 uses만 증가한다(실패도 사용 수에 들어간다).
func (r *Repo) LogPlaybookUse(ctx context.Context, id int, ok bool) error {
	if err := bumpMeta(ctx, r.pool, fmt.Sprintf("pb_%d_uses", id)); err != nil {
		return fmt.Errorf("log playbook use: uses: %w", err)
	}
	if ok {
		if err := bumpMeta(ctx, r.pool, fmt.Sprintf("pb_%d_ok", id)); err != nil {
			return fmt.Errorf("log playbook use: ok: %w", err)
		}
	}
	return nil
}

// bumpMeta는 meta key를 1 증분 upsert한다(단일 문장 — 읽고 쓰는 경합 없음).
func bumpMeta(ctx context.Context, pool *pgxpool.Pool, key string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, '1')
		 ON CONFLICT (key) DO UPDATE SET value = (meta.value::bigint + 1)::text`, key)
	return err
}

// usageRetryBackoff — RecordUsage 기록의 40001(직렬화 실패) 재시도 간격.
// 병렬 기동(공유 DB 풀 스위트 부하)에서 daily 카운터 upsert가 타 세션
// 커밋과 낙관적 충돌한다 — 드릴 DROP·승격 커밋과 같은 회복 패턴.
var usageRetryBackoff = []time.Duration{0, time.Second, 3 * time.Second}

// RecordUsage — v0.6 유입 계측 1회를 meta에 기록한다(플랜 T60).
// count 일괄 증분 본체는 RecordUsageCount — 이 메서드는 count=1 위임.
func (r *Repo) RecordUsage(ctx context.Context, kind, source string) error {
	return r.RecordUsageCount(ctx, kind, source, 1)
}

// RecordUsageCount — 유입 계측 count회를 daily 카운터에 일괄 증분한다
// (dup N 회계의 O(N) 읽기-쓰기 회피 — bumpDaily가 delta map을 수용).
// kind = write|hot|read|dup + dup_hook|dup_manual|dup_mcp(v0.10.2 R2 소스별
// dup, additive). last_<kind>_at을 RFC3339로 upsert하고(dup는
// 시각 키 없음 — write 시각 준용) `daily:YYYY-MM-DD` JSON 카운터를 증분한다.
// write는 source 카운터(hook|manual|mcp)를 병행한다. 14일 초과 daily 키는
// PruneDaily로 정리하되 오늘 daily 키가 신규 생성된 경우(날짜 롤오버 첫
// 이벤트)에만 실행한다 — 매 기록마다 DELETE하면 핫패스 낭비고 duplicate
// write는 2회 실행된다. 정리 실패는 기록에 영향 주지 않는다(무시).
// daily 증분은 읽고-쓰기다: 경합은 last-write-wins로 수용(플랜 계약 —
// bumpMeta 단일 문장 확장은 doltgres에 jsonb_object_agg가 없어 불가,
// 2026-10-22 확인). 파손된 기존 daily JSON은 0에서 재기작한다.
// 기록 upsert는 병렬 세션과 40001 충돌이 가능해 재시도로 회복하며, 소진
// 시 조용히 넘긴다 — 호출부(write·read·hot)가 이미 오류를 폐기하는 계측
// 원칙(사용 로그 유실이 호출 실패보다 낫다)을 RecordUsage도 따른다.
func (r *Repo) RecordUsageCount(ctx context.Context, kind, source string, count int) error {
	switch kind {
	case "write", "hot", "read", "dup", "dup_hook", "dup_manual", "dup_mcp":
	default:
		return fmt.Errorf("record usage: invalid kind %q", kind)
	}
	if count < 1 {
		return fmt.Errorf("record usage: invalid count %d", count)
	}
	delta := map[string]int{kind: count}
	if kind == "write" && source != "" {
		delta[source] = count
	}
	r.recordWithRetry(ctx, func(ctx context.Context, day string) (bool, error) {
		// dup 계열(총계 dup·dup_<source>)은 last_ 시각 키를 기록하지 않는다
		// (계약 승계 — write 시각 준용, 룰링: 미사용 메타 키 누적 방지).
		if !strings.HasPrefix(kind, "dup") {
			now := time.Now().UTC().Format(time.RFC3339)
			if _, err := r.pool.Exec(ctx,
				`INSERT INTO meta (key, value) VALUES ($1, $2)
				 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
				"last_"+kind+"_at", now); err != nil {
				return false, fmt.Errorf("record usage: last_%s_at: %w", kind, err)
			}
		}
		made, err := r.bumpDaily(ctx, day, delta)
		if err != nil {
			return false, fmt.Errorf("record usage: daily: %w", err)
		}
		return made, nil
	})
	return nil
}

// RecordReadEvent — 효과 계측 이벤트 1행 기록(v0.19.0, docs/METRICS.md §4).
// read 1회의 수치·플래그·세션 라벨만 남긴다(쿼리 본문·결과 내용 없음 —
// 개인 문구 부재 원칙). v0.20.0: 반환 지식 id CSV(ids, 상위 10) 추가 —
// v0.30.0: 첫 topic 정합 순위(firstRank, 0=정합 없음·폴백 제외) 추가.
// 사용 연쇄(chain) 산출 원천. 컷오프는 Go 계산 파라미터로 넘긴다(PruneDaily
// 산술·report stale 관례 — SQL interval 곱은 Doltgres 파라미터 추론 결함).
// INSERT 전용이라 읽기-쓰기 경합(40001)이 없고 재시도 없이 단발 실행한다.
func (r *Repo) RecordReadEvent(ctx context.Context, hits int, zero, near, fallback bool, session, ids string, firstRank int) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback, session, ids, first_rank)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`, hits, zero, near, fallback, session, ids, firstRank)
	if err != nil {
		return fmt.Errorf("record read event: %w", err)
	}
	return nil
}

// RecordSessionStart — 세션 시작 경계 1행(v0.20.0, session-start 커맨드).
// 세션 구간 [start_i, start_{i+1}) 배정의 원천 — 출력 없는 내부 계정이라
// 어느 프로파일에서도 기록한다(§13 — 에이전트 노출 아님).
func (r *Repo) RecordSessionStart(ctx context.Context, session string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO session_starts (session) VALUES ($1)`, session)
	if err != nil {
		return fmt.Errorf("record session start: %w", err)
	}
	return nil
}

// RecordHotEvent — hot --full 주입 노출 1행(v0.20.0, 실출력에 남은 지식
// id CSV). dead_pct의 hot 노출 보정 원천 — 주입은 --full 경로에서만
// 일어난다(hot.go N3 계약 승계).
func (r *Repo) RecordHotEvent(ctx context.Context, ids string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO hot_events (ids) VALUES ($1)`, ids)
	if err != nil {
		return fmt.Errorf("record hot event: %w", err)
	}
	return nil
}

// PruneReadEvents — 보존 한도를 넘은 효과 계측 이벤트 삭제. v0.20.0:
// read_events·session_starts는 14일(daily 카운터와 동일 정책), hot_events는
// 30일(dead_pct 노출 정합 가드 창과 정렬 — 14일이면 가드가 문서 계약의
// 절반만 커버한다, 리뷰 #3). v0.20.4: 보존일은 meta 오버라이드
// (events_retention_days·hot_events_retention_days, TUNING §2) — 키
// 부재·파손은 기본값. 트리거는 recordWithRetry의 롤오버 훅(PruneDaily와
// 동시) — 첫 read가 날짜를 넘기면 그 시점에 정리된다(하루 1회라 메타
// 읽기 왕복 부담 없음). 실패는 호출부가 조용히 넘긴다(계측 원칙).
func (r *Repo) PruneReadEvents(ctx context.Context) error {
	retention := map[string]int{
		"read_events":    14,
		"session_starts": 14,
		"hot_events":     30,
	}
	for _, kc := range []struct{ key, table string }{
		{"events_retention_days", "read_events"},
		{"events_retention_days", "session_starts"},
		{"hot_events_retention_days", "hot_events"},
	} {
		var v string
		if err := r.pool.QueryRow(ctx,
			`SELECT value FROM meta WHERE key = $1`, kc.key).Scan(&v); err == nil {
			if n, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil && n > 0 {
				retention[kc.table] = n
			}
		}
	}
	now := time.Now().UTC()
	for table, days := range retention {
		if _, err := r.pool.Exec(ctx,
			`DELETE FROM `+table+` WHERE ts < $1`, now.Add(-time.Duration(days)*24*time.Hour)); err != nil {
			return err
		}
	}
	return nil
}

// recordWithRetry — daily 증분 기록 공통 절차(RecordUsageCount·RecordReadUsage
// 공유). fn을 40001 재시도(usageRetryBackoff)로 실행하고, fn이 오늘 daily 키
// 신규 생성(롤오버 첫 이벤트)을 보고하면 PruneDaily·PruneReadEvents(각 14일
// 정리)를 실행한다 — 매 기록마다 DELETE하면 핫패스 낭비고 duplicate write는
// 2회 실행된다. 재시도 소진·정리 실패는 조용히 넘긴다(계측 원칙 — 사용 로그
// 유실이 호출 실패보다 낫다).
func (r *Repo) recordWithRetry(ctx context.Context, fn func(ctx context.Context, day string) (created bool, err error)) {
	day := time.Now().UTC().Format("2006-01-02")
	created := false
	_ = Retry(ctx, func() error {
		made, err := fn(ctx, day)
		if err != nil {
			return err
		}
		created = created || made
		return nil
	}, usageRetryBackoff)
	if created {
		_ = r.PruneDaily(ctx)
		_ = r.PruneReadEvents(ctx)
	}
}

// RecordReadUsage — v0.15.0 R2 read 1회 계측(제로·근접 분류 동반). read
// 카운터에 항상, zero면 read_zero, near면 read_zero_near를 daily delta로
// 증분하고 last_read_at을 갱신한다. last_read_at 행과 daily 행 쓰기는 단일
// 2행 upsert로 병합한다(계측 왕복 1 절감 — RecordReadUsage 경로 전용,
// RecordUsageCount의 다른 kind 경로는 기존 개별 쓰기 불변). daily JSON은
// 기존 읽고-쓰기(40001 재시도·last-write-wins) 계약을 승계한다.
func (r *Repo) RecordReadUsage(ctx context.Context, zero bool, near bool) error {
	delta := map[string]int{"read": 1}
	if zero {
		delta["read_zero"] = 1
	}
	if near {
		delta["read_zero_near"] = 1
	}
	r.recordWithRetry(ctx, func(ctx context.Context, day string) (bool, error) {
		cur, made, err := r.readDaily(ctx, day)
		if err != nil {
			return false, fmt.Errorf("record read usage: daily: %w", err)
		}
		out, err := mergeDaily(cur, delta)
		if err != nil {
			return false, fmt.Errorf("record read usage: %w", err)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO meta (key, value) VALUES ($1, $2), ($3, $4)
			 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
			"last_read_at", now, "daily:"+day, out); err != nil {
			return false, fmt.Errorf("record read usage: upsert: %w", err)
		}
		return made, nil
	})
	return nil
}

// bumpDaily — daily:<date> JSON 카운터 증분. 파손된 값은 0에서 재기작.
// 신규 키 생성(기존 행 부재) 시 created=true를 돌려준다(PruneDaily 트리거).
func (r *Repo) bumpDaily(ctx context.Context, day string, delta map[string]int) (created bool, err error) {
	key := "daily:" + day
	cur, created, err := r.readDaily(ctx, day)
	if err != nil {
		return false, err
	}
	out, err := mergeDaily(cur, delta)
	if err != nil {
		return false, err
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, string(out))
	if err != nil {
		return false, fmt.Errorf("bump daily: upsert: %w", err)
	}
	return created, nil
}

// readDaily — daily:<date> 현재 값을 읽는다(bumpDaily 읽기 절반 —
// RecordReadUsage 병합 upsert 경로 공유). 파손된 값은 0에서 재기작 대상
// 공란, 신규 키 생성(기존 행 부재) 시 created=true를 돌려준다.
func (r *Repo) readDaily(ctx context.Context, day string) (cur map[string]int, created bool, err error) {
	cur = map[string]int{}
	var val string
	err = r.pool.QueryRow(ctx, `SELECT value FROM meta WHERE key = $1`, "daily:"+day).Scan(&val)
	switch {
	case err == nil:
		// 파손(부분 디코드 포함)이거나 JSON null이면 cur를 공란으로 되돌려
		// 0에서 재기작한다(null은 cur=nil map이 되어 증분에서 panic).
		if json.Unmarshal([]byte(val), &cur) != nil || cur == nil {
			cur = map[string]int{}
		}
	case errors.Is(err, pgx.ErrNoRows):
		created = true // 첫 기록 — cur 공란, 이 호출로 키가 생성된다.
	default:
		return nil, false, fmt.Errorf("bump daily: read: %w", err)
	}
	return cur, created, nil
}

// mergeDaily — delta를 cur에 더해 JSON으로 내보낸다(증분 순수 계산 분리).
func mergeDaily(cur map[string]int, delta map[string]int) (string, error) {
	for k, n := range delta {
		cur[k] += n
	}
	out, err := json.Marshal(cur)
	if err != nil {
		return "", fmt.Errorf("bump daily: marshal: %w", err)
	}
	return string(out), nil
}

// PruneDaily — 14일 보존(오늘+직전 13일)을 넘는 daily 키 삭제. 키 날짜는
// UTC(RecordUsage 기록 기준)이므로 기준일도 UTC로 잡는다(서버 TZ 영향 배제).
// 날짜 형식이 아닌 키(daily:*)는 건드리지 않는다(형식 검사 후 cast).
func (r *Repo) PruneDaily(ctx context.Context) error {
	days := 14
	var v string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = $1`, "daily_retention_days").Scan(&v); err == nil {
		if n, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil && n > 0 {
			days = n
		}
	}
	// 컷오프는 Go 계산 date 파라미터(T70 관례 — `date - $1` 산술은
	// Doltgres가 $1을 interval로 추론해 실패한다, v0.24.0 회귀 방어).
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	_, err := r.pool.Exec(ctx,
		`DELETE FROM meta WHERE key ~ '^daily:[0-9]{4}-[0-9]{2}-[0-9]{2}$'
		   AND substring(key from 7)::date <= $1::date`, cutoff)
	return err
}

// PlaybookStats는 pb_* 사용 로그를 플레이북별 통계로 모은다(id 오름차순).
// 형식에 맞지 않는 pb_* 키는 조용히 건너뛴다(스키마 아닌 로그 파싱).
func (r *Repo) PlaybookStats(ctx context.Context) ([]Stat, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT key, value FROM meta WHERE key LIKE 'pb\_%'`)
	if err != nil {
		return nil, fmt.Errorf("playbook stats: query: %w", err)
	}
	defer rows.Close()
	byID := map[int]*Stat{}
	for rows.Next() {
		var key, val string
		if err := rows.Scan(&key, &val); err != nil {
			return nil, fmt.Errorf("playbook stats: scan: %w", err)
		}
		idStr := strings.TrimPrefix(key, "pb_")
		isOK := false
		switch {
		case strings.HasSuffix(idStr, "_uses"):
			idStr = strings.TrimSuffix(idStr, "_uses")
		case strings.HasSuffix(idStr, "_ok"):
			idStr = strings.TrimSuffix(idStr, "_ok")
			isOK = true
		default:
			continue
		}
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		n, _ := strconv.Atoi(val)
		s := byID[id]
		if s == nil {
			s = &Stat{ID: id}
			byID[id] = s
		}
		if isOK {
			s.OK += n
		} else {
			s.Uses += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("playbook stats: rows: %w", err)
	}
	out := make([]Stat, 0, len(byID))
	for _, s := range byID {
		if s.Uses > 0 {
			s.Rate = math.Round(float64(s.OK)/float64(s.Uses)*1000) / 1000
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
