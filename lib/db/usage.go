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
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/synuns/flint/lib/store"
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
// dup, additive) + brief(v0.34.1 brief 유입 계측, additive). last_<kind>_at을
// RFC3339로 upsert하고(dup는
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
	case "write", "hot", "read", "dup", "dup_hook", "dup_manual", "dup_mcp", "brief":
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

// ReadEvent — 효과 계측 이벤트 1행(v0.42.0 T3 구조체화 — 7파라미터 산재
// 호출부를 값 하나로 수렴, 지표·저장 불변). IDs는 반환 지식 id CSV(상위
// 10), FirstRank는 첫 topic 정합 순위(0=정합 없음·폴백 제외).
type ReadEvent struct {
	Hits      int
	Zero      bool
	Near      bool
	Fallback  bool
	Session   string
	IDs       string
	FirstRank int
}

// RecordReadEvent — 효과 계측 이벤트 1행 기록(v0.19.0, docs/METRICS.md §4).
// read 1회의 수치·플래그·세션 라벨만 남긴다(쿼리 본문·결과 내용 없음 —
// 개인 문구 부재 원칙). v0.20.0: 반환 지식 id CSV(ids, 상위 10) 추가 —
// v0.30.0: 첫 topic 정합 순위(firstRank, 0=정합 없음·폴백 제외) 추가.
// v0.42.0: ReadEvent 구조체 1값 수렴. 사용 연쇄(chain) 산출 원천.
// INSERT 전용이라 읽기-쓰기 경합(40001)이 없고 재시도 없이 단발 실행한다.
// v1.0.1 F2: 성공 경로에서 maybePruneAfterRead(24h 게이트 정리 훅)을
// 함께 발화한다 — read가 RecordUsage를 우회하는 v0.42 구조에서도 정리가
// 일어나야 하기 때문(아래 훅 주석 참조).
func (r *Repo) RecordReadEvent(ctx context.Context, ev ReadEvent) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback, session, ids, first_rank)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ev.Hits, ev.Zero, ev.Near, ev.Fallback, ev.Session, ev.IDs, ev.FirstRank)
	if err != nil {
		return fmt.Errorf("record read event: %w", err)
	}
	r.maybePruneAfterRead(ctx)
	return nil
}

// pruneReadGateKey — read 경로 정리 훅의 24h 게이트 meta 키(last_*_at
// RFC3339 관례 준용). last_read_at과는 별개 키다(유입 계측 시각 아님).
const pruneReadGateKey = "last_events_prune_at"

// pruneGateDue — 게이트 판정 순수 함수(단위 테스트 대상): last가 RFC3339로
// 파싱되고 24h 이내면 거짓(정리 스킵), 그 외(부재·공란·파손·24h 경과)는 참.
func pruneGateDue(last string, now time.Time) bool {
	if last == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return true
	}
	return now.Sub(t) >= 24*time.Hour
}

// pruneGateCheckedAt — 프로세스 내 게이트 재확인 캐시(v1.0.2 R2 성능):
// 마지막 게이트 확인 시각의 unix 초(atomic — Repo 인스턴스 무관 공유).
// 0=미확인. RecordReadEvent마다 meta 게이트 SELECT가 남던 read 핫패스
// 왕복을 1h마다 1회로 축소한다.
var pruneGateCheckedAt atomic.Int64

// pruneGateCacheTTL — 프로세스 내 게이트 캐시 수명. DB 게이트(24h)보다
// 짧은 1h — 단발 CLI는 물론 장기 프로세스도 최대 1h마다 게이트를 재평가
// 해 정리가 하루 통째로 밀리지 않게 한다.
const pruneGateCacheTTL = time.Hour

// pruneGateProbe — 게이트 캐시 판정(순수·단위 테스트 대상): 마지막 확인
// 후 TTL 이내면 거짓(게이트 meta SELECT 스킵 — TTL 내 재확인 0회), 경과
// 했거나 최초면 참이고 확인 시각을 스탬프한다(SELECT 1회). 스탬프는 게이트
// 확인 시점 기준 — 이후 정리 실패가 있어도 DB 게이트는 새겨지지 않아 다른
// 프로세스·TTL 경과 후 재시도가 열려 있다.
func pruneGateProbe(now time.Time) bool {
	if last := pruneGateCheckedAt.Load(); last != 0 &&
		now.Sub(time.Unix(last, 0)) < pruneGateCacheTTL {
		return false
	}
	pruneGateCheckedAt.Store(now.Unix())
	return true
}

// maybePruneAfterRead — read 전용 세션의 정리 훅(v1.0.1 F2). v0.42.0 T2가
// RecordReadUsage(read daily 쓰기)를 폐지하면서 read 경로가
// recordWithRetry(롤오버 훅)를 지나지 않게 됐다 — write가 없는 세션에서는
// PruneReadEvents가 발화되지 않아 read_events가 보존 한게 넘게 무한
// 증가한다. RecordReadEvent 성공 경로에서 last_events_prune_at 게이트가
// 열릴 때(마지막 정리 후 24h — 매 read마다 DELETE·meta 왕복은 핫패스
// 낭비) PruneDaily·PruneReadEvents를 실행한다. v1.0.2 R2(성능): 게이트
// 재확인은 프로세스 내 1h 캐시(pruneGateProbe)로 축소 — 캐시 미충족
// 시에만 DB 게이트(24h) SELECT를 실행한다. 정리 성공 시에만 DB 게이트를
// 새겨 실패한 정리가 하루 동안 묻히지 않게 한다(스탬프-후-실패면 재시도
// 없이 24h 스킵). 병렬 read가 동시에 통과해도 DELETE는 멱등 — 중복 실행
// 무해하다. 게이트 읽기·정리·스탬프 실패는 조용히 넘긴다(계측 원칙).
func (r *Repo) maybePruneAfterRead(ctx context.Context) {
	if !pruneGateProbe(time.Now().UTC()) {
		return
	}
	var last string
	_ = r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = $1`, pruneReadGateKey).Scan(&last)
	if !pruneGateDue(last, time.Now().UTC()) {
		return
	}
	if err := r.PruneDaily(ctx); err != nil {
		return
	}
	if err := r.PruneReadEvents(ctx); err != nil {
		return
	}
	_, _ = r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		pruneReadGateKey, time.Now().UTC().Format(time.RFC3339))
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

// thresholdsForPrune — PruneReadEvents·PruneDaily 공유: meta 임계값
// 오버라이드를 등록표 파생 경로(store.ParseThresholds — threshold_meta.go
// 가드 대상)로 읽는다(v0.42.0 T1 §2 — 키별 SELECT+Atoi 재파싱 제거).
// 조회 실패·키 부재·파손·0(미설정)은 기본값(DefaultThresholds) 폴백
// — 이전 per-key 규약(n>0만 수용)과 동일 결과.
func (r *Repo) thresholdsForPrune(ctx context.Context, keys ...string) (store.Thresholds, error) {
	def := store.DefaultThresholds()
	rows, err := r.pool.Query(ctx,
		`SELECT key, value FROM meta WHERE key = ANY($1)`, keys)
	if err != nil {
		return def, nil // 메타 읽기 실패는 기본값 폴백(이전 규약 승계)
	}
	defer rows.Close()
	var kv [][2]any
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		kv = append(kv, [2]any{k, v})
	}
	th, err := store.ParseThresholds(kv)
	if err != nil {
		return def, nil
	}
	if th.EventsRetentionD == 0 {
		th.EventsRetentionD = def.EventsRetentionD
	}
	if th.HotRetentionD == 0 {
		th.HotRetentionD = def.HotRetentionD
	}
	if th.DailyRetentionD == 0 {
		th.DailyRetentionD = def.DailyRetentionD
	}
	return th, nil
}

// PruneReadEvents — 보존 한도를 넘은 효과 계측 이벤트 삭제. v0.20.0:
// read_events·session_starts는 14일(daily 카운터와 동일 정책), hot_events는
// 30일(dead_pct 노출 정합 가드 창과 정렬 — 14일이면 가드가 문서 계약의
// 절반만 커버한다, 리뷰 #3). v0.20.4: 보존일은 meta 오버라이드
// (events_retention_days·hot_events_retention_days, TUNING §2) — 키
// 부재·파손은 기본값. 트리거는 2개(PruneDaily와 동시 발화): ① recordWithRetry의
// 롤오버 훅(오늘 daily 키 신규 생성 — 하루 첫 write 계측), ② v1.0.1 F2
// maybePruneAfterRead(RecordReadEvent 성공 + last_events_prune_at 24h 게이트
// — read만 하는 세션에서도 정리가 일어난다). 하루 1회 수준이라 메타
// 읽기 왕복 부담이 없다. 실패는 호출부가 조용히 넘긴다(계측 원칙).
func (r *Repo) PruneReadEvents(ctx context.Context) error {
	th, _ := r.thresholdsForPrune(ctx, "events_retention_days", "hot_events_retention_days")
	retention := map[string]int{
		"read_events":    th.EventsRetentionD,
		"session_starts": th.EventsRetentionD,
		"hot_events":     th.HotRetentionD,
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

// recordWithRetry — daily 증분 기록 공통 절차(RecordUsageCount용.
// v0.42.0 T2: RecordReadUsage(read kind daily 쓰기)는 폐지 — read 유입
// 계측의 원천은 read_events다). fn을 40001 재시도(usageRetryBackoff)로 실행하고, fn이 오늘 daily 키
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

// readDaily — daily:<date> 현재 값을 읽는다(bumpDaily 읽기 절반).
// 파손된 값은 0에서 재기작 대상
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
// 보존일은 meta 오버라이드(daily_retention_days) — v0.42.0 T1: 파생
// 경로(thresholdsForPrune)로 재파싱(키 부재·파손·0은 기본값).
func (r *Repo) PruneDaily(ctx context.Context) error {
	th, _ := r.thresholdsForPrune(ctx, "daily_retention_days")
	days := th.DailyRetentionD
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
