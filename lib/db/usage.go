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

// Stat — 플레이북별 사용 통계(maintain --distill·성공률 노출용).
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
// kind = write|hot|read|dup. last_<kind>_at을 RFC3339로 upsert하고(dup는
// 시각 키 없음 — write 시각 준용) `daily:YYYY-MM-DD` JSON 카운터를 증분한다.
// write는 source 카운터(hook|manual|mcp)를 병행한다. 기록 후 14일 초과 daily
// 키를 PruneDaily로 정리하되 정리 실패는 기록에 영향 주지 않는다(무시).
// daily 증분은 읽고-쓰기다: 경합은 last-write-wins로 수용(플랜 계약 —
// bumpMeta 단일 문장 확장은 doltgres에 jsonb_object_agg가 없어 불가,
// 2026-10-22 확인). 파손된 기존 daily JSON은 0에서 재기작한다.
// 기록 upsert는 병렬 세션과 40001 충돌이 가능해 재시도로 회복하며, 소진
// 시 조용히 넘긴다 — 호출부(write·read·hot)가 이미 오류를 폐기하는 계측
// 원칙(사용 로그 유실이 호출 실패보다 낫다)을 RecordUsage도 따른다.
func (r *Repo) RecordUsage(ctx context.Context, kind, source string) error {
	switch kind {
	case "write", "hot", "read", "dup":
	default:
		return fmt.Errorf("record usage: invalid kind %q", kind)
	}
	delta := map[string]int{kind: 1}
	if kind == "write" && source != "" {
		delta[source] = 1
	}
	day := time.Now().UTC().Format("2006-01-02")
	_ = Retry(ctx, func() error {
		if kind != "dup" {
			now := time.Now().UTC().Format(time.RFC3339)
			if _, err := r.pool.Exec(ctx,
				`INSERT INTO meta (key, value) VALUES ($1, $2)
				 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
				"last_"+kind+"_at", now); err != nil {
				return fmt.Errorf("record usage: last_%s_at: %w", kind, err)
			}
		}
		if err := r.bumpDaily(ctx, day, delta); err != nil {
			return fmt.Errorf("record usage: daily: %w", err)
		}
		return nil
	}, usageRetryBackoff) // 재시도 소진 조용히(계측 원칙).
	_ = r.PruneDaily(ctx) // 14일 정리 실패는 조용히(계약).
	return nil
}

// bumpDaily — daily:<date> JSON 카운터 증분. 파손된 값은 0에서 재기작.
func (r *Repo) bumpDaily(ctx context.Context, day string, delta map[string]int) error {
	key := "daily:" + day
	cur := map[string]int{}
	var val string
	err := r.pool.QueryRow(ctx, `SELECT value FROM meta WHERE key = $1`, key).Scan(&val)
	switch {
	case err == nil:
		// 파손(부분 디코드 포함)이거나 JSON null이면 cur를 공란으로 되돌려
		// 0에서 재기작한다(null은 cur=nil map이 되어 증분에서 panic).
		if json.Unmarshal([]byte(val), &cur) != nil || cur == nil {
			cur = map[string]int{}
		}
	case errors.Is(err, pgx.ErrNoRows):
		// 첫 기록 — cur 공란.
	default:
		return fmt.Errorf("bump daily: read: %w", err)
	}
	for k, n := range delta {
		cur[k] += n
	}
	out, err := json.Marshal(cur)
	if err != nil {
		return fmt.Errorf("bump daily: marshal: %w", err)
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, string(out))
	if err != nil {
		return fmt.Errorf("bump daily: upsert: %w", err)
	}
	return nil
}

// PruneDaily — 14일 보존(오늘+직전 13일)을 넘는 daily 키 삭제. 키 날짜는
// UTC(RecordUsage 기록 기준)이므로 기준일도 UTC로 잡는다(서버 TZ 영향 배제).
// 날짜 형식이 아닌 키(daily:*)는 건드리지 않는다(형식 검사 후 cast).
func (r *Repo) PruneDaily(ctx context.Context) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM meta WHERE key ~ '^daily:[0-9]{4}-[0-9]{2}-[0-9]{2}$'
		   AND substring(key from 7)::date <= (now() AT TIME ZONE 'UTC')::date - 14`)
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
