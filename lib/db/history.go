// history.go — dolt_log 히스토리 조회(wire.LogQuery 계약의 Doltgres
// 구현). 커밋 로그 접근의 단일 소스 — asof(lib/wire/asof.go)·audit
// (lib/ops/audit.go)가 이식 대상이다. 포팅 쿼리는 원문 그대로(바이트
// 동치 원칙): 컬럼 충원 범위도 원문 승계 — Hash·LatestN은 4열
// (commit_hash·committer·date·message), Prefix·BeforeOrAt은 commit_hash만,
// Earliest는 date만.
package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// LogEntries — q 모드별 dolt_log 조회. 모드 우선순위: Earliest > Prefix >
// Hash > BeforeOrAt(비제로) > LatestN. 어느 것도 지정 없으면 오류.
// 질의 실패는 재시도 1회(spec §7 — wire.QueryOnce 동등, queryRetry).
// Prefix는 LIKE 일치 전체 목록을 돌려주고 유일성 판정은 호출측(asof 계약
// 승계), BeforeOrAt은 시각 직전(포함) 최신 1행, Earliest는 첫 커밋 시각
// 1행(date만 충원), LatestN은 DESC 최근 N에 MessageNotLike NOT LIKE 필터
// (audit 기본 telemetry 제외 필터 표현).
func (r *Repo) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	switch {
	case q.Earliest:
		return r.logEarliest(ctx)
	case q.Prefix != "":
		// 원문: asof.go 접두어 확정 질의(전체 일치 목록).
		return r.logHashRows(ctx,
			`SELECT commit_hash FROM dolt_log WHERE commit_hash LIKE $1||'%' ORDER BY commit_order DESC`,
			q.Prefix)
	case q.Hash != "":
		// Hash 모드는 4열(commit_hash·committer·date·message) 신규 조립 —
		// 구 read/status의 date 1열 질의를 대체한다(값은 동치, 조립 상이).
		// "원문 승계"가 아님에 유의.
		return r.logFullRows(ctx, `SELECT commit_hash, committer, date, message FROM dolt_log WHERE commit_hash = $1`, q.Hash)
	case !q.BeforeOrAt.IsZero():
		// 원문: asof.go 시각 직전 채택 질의. date 비교는 Go 계산 time.Time을
		// timestamptz 파라미터로 전달(SQL interval 금지 원칙 승계).
		return r.logHashRows(ctx,
			`SELECT commit_hash FROM dolt_log WHERE date <= $1 ORDER BY commit_order DESC LIMIT 1`,
			q.BeforeOrAt)
	case q.LatestN > 0:
		return r.logLatestN(ctx, q)
	}
	return nil, fmt.Errorf("log entries: empty query")
}

// logHashRows — commit_hash 단일 열 질의(Prefix·BeforeOrAt 원문 승계).
func (r *Repo) logHashRows(ctx context.Context, sql string, arg any) ([]wire.LogEntry, error) {
	rows, err := r.queryRetry(ctx, sql, arg)
	if err != nil {
		return nil, fmt.Errorf("log entries: %w", err)
	}
	defer rows.Close()
	var out []wire.LogEntry
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("log entries: scan: %w", err)
		}
		out = append(out, wire.LogEntry{Hash: h})
	}
	return out, rows.Err()
}

// logFullRows — 4열(commit_hash·committer·date·message) 질의(Hash 모드).
// date 셀은 parseLogDate로 파싱(time.Time 아닐 때 문자열 3레이아웃).
func (r *Repo) logFullRows(ctx context.Context, sql string, arg ...any) ([]wire.LogEntry, error) {
	rows, err := r.queryRetry(ctx, sql, arg...)
	if err != nil {
		return nil, fmt.Errorf("log entries: %w", err)
	}
	defer rows.Close()
	var out []wire.LogEntry
	for rows.Next() {
		var e wire.LogEntry
		var d any
		if err := rows.Scan(&e.Hash, &e.Committer, &d, &e.Message); err != nil {
			return nil, fmt.Errorf("log entries: scan: %w", err)
		}
		t, ok := parseLogDate(d)
		if !ok {
			return nil, fmt.Errorf("log entries: unparseable date %v", d)
		}
		e.Date = t
		out = append(out, e)
	}
	return out, rows.Err()
}

// logEarliest — 첫 커밋 시각 1행. 원문: asof.go Earliest 질의(date만
// 선택 — commit_hash 열 미충원은 원문 쿼리 승계).
func (r *Repo) logEarliest(ctx context.Context) ([]wire.LogEntry, error) {
	rows, err := r.queryRetry(ctx,
		`SELECT date FROM dolt_log ORDER BY commit_order ASC LIMIT 1`)
	if err != nil {
		return nil, fmt.Errorf("log entries: %w", err)
	}
	defer rows.Close()
	var out []wire.LogEntry
	for rows.Next() {
		var d any
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("log entries: scan: %w", err)
		}
		t, ok := parseLogDate(d)
		if !ok {
			return nil, fmt.Errorf("log entries: unparseable date %v", d)
		}
		out = append(out, wire.LogEntry{Date: t})
	}
	return out, rows.Err()
}

// logLatestN — 최근 N 조회(audit.go 이식). 원문 조립 순서 보존: 조건은
// NOT LIKE(기본 telemetry 제외)→LIKE(--session 포함) 순, placeholder는
// $2부터 순차 부여, AND 결합, LIMIT $1은 문미 불변.
func (r *Repo) logLatestN(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	sql := `SELECT commit_hash, committer, date, message FROM dolt_log`
	qargs := []any{q.LatestN}
	var conds []string
	ph := 2
	if q.MessageNotLike != "" {
		conds = append(conds, fmt.Sprintf(`message NOT LIKE $%d`, ph))
		qargs = append(qargs, q.MessageNotLike)
		ph++
	}
	if q.MessageLike != "" {
		conds = append(conds, fmt.Sprintf(`message LIKE $%d`, ph))
		qargs = append(qargs, q.MessageLike)
	}
	if len(conds) > 0 {
		sql += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	sql += ` ORDER BY commit_order DESC LIMIT $1`
	return r.logFullRows(ctx, sql, qargs...)
}

// parseLogDate — dolt_log date 값을 time.Time으로(실구현은 time.Time,
// fake/문자열은 레이아웃 3종 파싱). audit.go parseAuditDate 로직 이관
// (원 로직 보존).
func parseLogDate(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, lay := range []string{
			"2006-01-02 15:04:05.999999999 -0700 MST", // time.Time 기본 Sprint
			"2006-01-02 15:04:05.999999999",
			time.RFC3339,
		} {
			if ts, err := time.Parse(lay, x); err == nil {
				return ts, true
			}
		}
	}
	return time.Time{}, false
}
