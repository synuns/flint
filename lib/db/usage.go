// usage.go — T37 플레이북 사용 로그·성공률. 신규 테이블 금지 제약(schema v2
// 유지)이라 meta 키 pb_<id>_uses/pb_<id>_ok 증분으로 기록한다. 사용 로그는
// 의도적으로 비멱등이다 — 같은 호출이 두 번 세지는 게 결함이 아니라 로그다.
package db

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

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
