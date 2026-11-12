// diff.go — flint diff <from> <to> (T84): 두 as-of 지점 간 지식 변경 비교.
// 행 상세는 dolt_commit_diff_knowledge(from_commit·to_commit WHERE 각 1개
// 필수 — 실측 2026-10-24), 요약 커밋 목록은 dolt_log 범위×dolt_diff 조인.
// 출력은 한 줄 JSON, 무효 as-of 스펙은 exit 3.
// v0.40.0 T1: lib/cli에서 이동(패키지문·내보내기만 변경 — 동작 불변).
// as-of 정규화는 cli 소유 asof.go(T4 wire 이동 예정) — ParseAsOfSpecFn
// seam으로 주입받는다.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// ParseAsOfSpecFn — as-of 스펙 정규화 seam. 실구현 cli.ParseAsOfSpec을
// cli/ops_alias_1.go init이 주입한다(T4 wire 이동 후 직접 호출 수렴).
var ParseAsOfSpecFn func(ctx context.Context, st wire.Store, spec string) (string, error)

// ErrInvalidAsOf — 무효 as-of 스펙(형식 오류·접두어 모호/미발견·첫 커밋
// 이전). 커맨드 계층에서 exit 3으로 매핑한다. cli.ErrInvalidAsOf와 동일
// 값(cli init 주입) — errors.Is 판정 불변.
var ErrInvalidAsOf error

// diffCommitsMax — 요약 커밋 목록 상한(계약: 기본 20).
const diffCommitsMax = 20

// diffRow — 행 상세 1건. id·topic은 added·modified는 to_*, removed는
// from_* 값(실측: removed 행은 to_* NULL → COALESCE).
type diffRow struct {
	DiffType string `json:"diff_type"`
	ID       any    `json:"id"`
	Topic    any    `json:"topic"`
}

// diffCommit — 요약 커밋 1건. data_change·schema_change은 해당 커밋의
// 테이블 전체 OR(dolt_diff 테이블별 플래그 집계).
type diffCommit struct {
	Hash         string    `json:"hash"`
	Date         time.Time `json:"date"`
	Message      string    `json:"message"`
	DataChange   bool      `json:"data_change"`
	SchemaChange bool      `json:"schema_change"`
}

// diffCounts — 요약 수치(--summary 출력 그대로).
type diffCounts struct {
	From     string       `json:"from"`
	To       string       `json:"to"`
	Added    int          `json:"added"`
	Modified int          `json:"modified"`
	Removed  int          `json:"removed"`
	Commits  []diffCommit `json:"commits"`
}

// diffDetail — 기본 모드 출력(수치 + 행 상세).
type diffDetail struct {
	From     string       `json:"from"`
	To       string       `json:"to"`
	Added    int          `json:"added"`
	Modified int          `json:"modified"`
	Removed  int          `json:"removed"`
	Rows     []diffRow    `json:"rows"`
	Commits  []diffCommit `json:"commits"`
}

// diffCommitsSQL — from(초과)~to(이하) 커밋 범위의 dolt_log×dolt_diff 요약.
// from/to 해시는 $1·$2 파라미터화(주입 방지, §3).
var diffCommitsSQL = fmt.Sprintf(`SELECT l.commit_hash, l.date, l.message,
  COALESCE(bool_or(d.data_change), false),
  COALESCE(bool_or(d.schema_change), false)
FROM dolt_log l
LEFT JOIN dolt_diff d ON d.commit_hash = l.commit_hash
WHERE l.commit_order > (SELECT commit_order FROM dolt_log WHERE commit_hash = $1)
  AND l.commit_order <= (SELECT commit_order FROM dolt_log WHERE commit_hash = $2)
GROUP BY l.commit_hash, l.commit_order, l.date, l.message
ORDER BY l.commit_order DESC
LIMIT %d`, diffCommitsMax)

// RunDiff — `flint diff <from> <to> [--summary] [--data|--schema]`.
// from·to는 as-of 스펙(각각 ParseAsOfSpec으로 커밋 해시 정규화). --data·
// --schema는 커밋 요약을 해당 변경 커밋으로 한정하는 필터.
func RunDiff(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	summary, dataOnly, schemaOnly := false, false, false
	var pos []string
	for _, a := range args {
		switch a {
		case "--summary":
			summary = true
		case "--data":
			dataOnly = true
		case "--schema":
			schemaOnly = true
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) != 2 {
		fmt.Fprintln(stdout, `{"error":"usage: flint diff <from> <to> [--summary] [--data|--schema]"}`)
		return 3
	}
	from, err := ParseAsOfSpecFn(ctx, st, pos[0])
	if err != nil {
		return diffFail(stdout, err)
	}
	to, err := ParseAsOfSpecFn(ctx, st, pos[1])
	if err != nil {
		return diffFail(stdout, err)
	}

	// 행 상세 — dolt_commit_diff_knowledge은 from·to 커밋 필터 각 1개가
	// 쿼리 계획 요구사항이다(실측). COALESCE로 added/removed 공통 표현.
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT diff_type, COALESCE(to_id, from_id), COALESCE(to_topic, from_topic)
FROM dolt_commit_diff_knowledge WHERE to_commit = $1 AND from_commit = $2`,
		[]any{to, from})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	counts := diffCounts{From: from, To: to, Commits: []diffCommit{}}
	detail := make([]diffRow, 0, len(rows))
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		dt := fmt.Sprint(r[0])
		switch dt {
		case "added":
			counts.Added++
		case "modified":
			counts.Modified++
		case "removed":
			counts.Removed++
		}
		row := diffRow{DiffType: dt}
		if len(r) > 1 {
			row.ID = r[1]
		}
		if len(r) > 2 {
			row.Topic = render.Text(fmt.Sprint(r[2])) // v0.37.0: render.Text 경유(출력 재살균 승계)
		}
		detail = append(detail, row)
	}

	// 요약 커밋 목록 — 최신순 상한 20. --data·--schema는 여기서 한정.
	commits, err := wire.QueryOnce(ctx, st, diffCommitsSQL, []any{from, to})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	for _, r := range commits {
		if len(r) < 5 {
			continue
		}
		c := diffCommit{Hash: fmt.Sprint(r[0]), Message: render.Text(fmt.Sprint(r[2]))} // v0.37.0: render.Text 경유
		if t, ok := parseAuditDate(r[1]); ok {
			c.Date = t
		}
		if b, ok := r[3].(bool); ok {
			c.DataChange = b
		}
		if b, ok := r[4].(bool); ok {
			c.SchemaChange = b
		}
		if dataOnly && !c.DataChange {
			continue
		}
		if schemaOnly && !c.SchemaChange {
			continue
		}
		counts.Commits = append(counts.Commits, c)
	}

	var out any = counts
	if !summary {
		out = diffDetail{
			From: counts.From, To: counts.To,
			Added: counts.Added, Modified: counts.Modified, Removed: counts.Removed,
			Rows: detail, Commits: counts.Commits,
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// diffFail — as-of 정규화 실패 출력. ErrInvalidAsOf는 invalid_as_of 한 줄
// JSON + exit 3, 그 외 질의 오류는 기존 errLine·MapExit 경로.
func diffFail(stdout io.Writer, err error) int {
	if errors.Is(err, ErrInvalidAsOf) {
		b, _ := json.Marshal(map[string]string{"error": "invalid_as_of", "message": err.Error()})
		fmt.Fprintln(stdout, string(b))
		return 3
	}
	fmt.Fprintln(stdout, wire.ErrLine(err))
	return store.MapExit(err)
}
