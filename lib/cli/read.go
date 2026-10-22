// read.go — flint read [query]: active_knowledge 뷰 강제 조회 + 토큰 예산.
package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"flint/lib/query"
	"flint/lib/store"
)

// readBumper — v0.6 항목 참조 계측 seam(T62 BumpReads).
type readBumper interface {
	BumpReads(ctx context.Context, ids []int) error
}

// realStore 위임 — reads.go(T62) 실구현 연결. 메서드 정의만 여기 둔다.
func (s *realStore) BumpReads(ctx context.Context, ids []int) error {
	return s.repo.BumpReads(ctx, ids)
}

// cellToInt — 셀(any)→int 단일 강제 변환 유틸. read rowIDs·maintain stale·
// report 집계가 공유한다(구현 3중 복제 방지). pgx 숫자형(int·int32·int64)
// ·float64·10진 문자열을 수용하고 nil·비수는 (0, false).
func cellToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	default:
		return 0, false
	}
}

// rowIDs — 조회 결과 행에서 id 컬럼(위치 0)을 int로 모은다. 매핑 불가 칸은
// 조용히 건너뛴다(계측 누락은 조회를 깨지 않는다).
func rowIDs(rows [][]any) []int {
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if id, ok := cellToInt(row[0]); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// instrumentReads — 참조 계측(T62): 결과 항목 id 전수 증분 + 유입 1회 기록.
// 유입 기록은 UsageStore 단일 seam(write.go recordUsage)을 경유한다.
// 둘 다 계측이라 실패는 조용히 무시된다.
func instrumentReads(ctx context.Context, st Store, kind string, ids []int) {
	if rb, ok := st.(readBumper); ok {
		_ = rb.BumpReads(ctx, ids)
	}
	recordUsage(ctx, st, kind, "")
}

// runRead — 무료 텍스트 query와 --tag --entity --domain --limit 필터.
// --kind는 active_knowledge가 knowledge 단일 kind 뷰라 의미가 없어 받지 않는다.
func runRead(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	f := query.Filters{}
	jsonMode, fields := false, ""
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		var v string
		if i+1 < len(args) {
			v = args[i+1]
		}
		switch a {
		case "--tag":
			f.Tag, i = v, i+1
		case "--entity":
			f.Entity, i = v, i+1
		case "--domain":
			f.Domain, i = v, i+1
		case "--limit":
			n, err := strconv.Atoi(v)
			if err != nil {
				fmt.Fprintln(stdout, errLine(&store.SchemaError{Field: "limit", Expect: "integer"}))
				return 3
			}
			f.Limit, i = n, i+1
		case "--json":
			jsonMode = true
		case "--fields":
			fields, i = v, i+1
		default:
			pos = append(pos, a)
		}
	}
	sql, qargs := query.BuildRead(strings.Join(pos, " "), f)
	rows, err := queryOnce(ctx, st, sql, qargs)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	// 출력 중립화(ADR-0006): 저장 단계 살균을 읽기에도 재적용 — 저장 이후
	// 혹은 경로상 유입된 제어 문자·[flint] 마커가 출력 경계를 위조하지 않게 한다.
	for _, row := range rows {
		for i, cell := range row {
			if s, ok := cell.(string); ok {
				row[i] = store.SanitizeText(s)
			}
		}
	}
	fmt.Fprint(stdout, query.FormatRows(rows, fields, jsonMode))
	// 계측(T56): zero 히트 여부를 meta read_stats에 누적. 실패는 조용히(§13).
	bumpReadStatsToMeta(ctx, st, len(rows) == 0)
	// 계측(T62): 결과 항목 id 전수 참조 증분 + read 유입 1회. 조용히(§13).
	instrumentReads(ctx, st, "read", rowIDs(rows))
	return 0
}
