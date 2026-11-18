// diff.go — flint diff <from> <to>: 두 as-of 지점 간 지식 변경 비교.
// 행 상세·요약 커밋 목록은 wire.Store DiffRows·DiffCommits(db 구현측
// 소유)에서 typed 값으로 받는다. 출력은 한 줄 JSON, 무효 as-of 스펙은
// exit 3. as-of 정규화는 cli 소유 asof.go — ParseAsOfSpecFn
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

// ParseAsOfSpecFn — as-of 스펙 정규화 seam. 실구현 wire.ParseAsOfSpec을
// cli/root.go composition root init이 주입한다.
var ParseAsOfSpecFn func(ctx context.Context, st wire.Store, spec string) (string, error)

// ErrInvalidAsOf — 무효 as-of 스펙(형식 오류·접두어 모호/미발견·첫 커밋
// 이전). 커맨드 계층에서 exit 3으로 매핑한다. wire.ErrInvalidAsOf와 동일
// 값(root.go composition root 주입) — errors.Is 판정 불변.
var ErrInvalidAsOf error

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
		// 원시 문자열 JSON이 아니라 wire.UsageErrorf → ErrLine
		// 경로(error:"usage").
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
			"usage: flint diff <from> <to> [--summary] [--data|--schema]")))
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

	// 행 상세 — DiffRows가 added(to_*)·removed(from_*) COALESCE 표현을
	// typed 값으로 반환(db 구현측 소유).
	rows, err := st.DiffRows(ctx, from, to)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	counts := diffCounts{From: from, To: to, Commits: []diffCommit{}}
	detail := make([]diffRow, 0, len(rows))
	for _, r := range rows {
		switch r.DiffType {
		case "added":
			counts.Added++
		case "modified":
			counts.Modified++
		case "removed":
			counts.Removed++
		}
		detail = append(detail, diffRow{
			DiffType: r.DiffType,
			ID:       r.ID,
			Topic:    render.Text(r.Topic), // render.Text 경유(출력 재살균 승계)
		})
	}

	// 요약 커밋 목록 — 최신순(상한은 구현측). --data·--schema는 여기서 한정.
	commits, err := st.DiffCommits(ctx, from, to)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	for _, s := range commits {
		c := diffCommit{
			Hash:         s.Hash,
			Date:         s.Date,
			Message:      render.Text(s.Message), // render.Text 경유
			DataChange:   s.DataChange,
			SchemaChange: s.SchemaChange,
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
