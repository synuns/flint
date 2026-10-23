// read.go — flint read [query]: active_knowledge 뷰 강제 조회 + 토큰 예산.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

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
	jsonMode, fields, asOfSpec := false, "", ""
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
		case "--as-of":
			asOfSpec, i = v, i+1
		case "--json":
			jsonMode = true
		case "--fields":
			fields, i = v, i+1
		default:
			pos = append(pos, a)
		}
	}
	sql, qargs := query.BuildRead(strings.Join(pos, " "), f)
	asOfHash, asOfDate := "", ""
	if asOfSpec != "" {
		hash, err := ParseAsOfSpec(ctx, st, asOfSpec)
		if err != nil {
			if errors.Is(err, ErrInvalidAsOf) {
				fmt.Fprintln(stdout, asOfErrLine("invalid_as_of", err.Error()))
				return 3
			}
			fmt.Fprintln(stdout, errLine(err))
			return store.MapExit(err)
		}
		// 접합 안전성 — 해시는 16진 소문자로 확정된 경우만 인라인(§3 주입 방지).
		if !isHexPrefix(hash) {
			err := fmt.Errorf("정규화 결과가 16진 해시가 아님 %q: %w", hash, ErrInvalidAsOf)
			fmt.Fprintln(stdout, asOfErrLine("invalid_as_of", err.Error()))
			return 3
		}
		// AS OF 절은 테이블 참조 바로 뒤(뷰+AS OF "현재 정의+과거 행"). 기존
		// RankOrder·LIMIT 절은 그대로 둔다.
		sql = strings.Replace(sql, " FROM active_knowledge",
			" FROM active_knowledge AS OF '"+hash+"'", 1)
		asOfHash = hash
		// 커밋 시각 첨부 — 메타데이터 조회 실패는 조용히(빈 값, §13).
		if dr, err := queryOnce(ctx, st,
			`SELECT date FROM dolt_log WHERE commit_hash = $1`, []any{hash}); err == nil &&
			len(dr) == 1 && len(dr[0]) > 0 {
			asOfDate = fmtAsOfDate(dr[0][0])
		}
	}
	rows, err := queryOnce(ctx, st, sql, qargs)
	if err != nil {
		// 뷰-컬럼 미호환(과거 행에 없는 컬럼)은 기본 테이블 조회 안내로 랩(C3).
		if asOfSpec != "" {
			err = wrapViewColumnMismatch(err)
		}
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
	// --as-of 지정 시 결과 뒤에 as_of{hash,commit_date} 한 줄(JSON) 추가.
	// 미지정이면 기존 필드만 그대로.
	if asOfHash != "" {
		b, _ := json.Marshal(asOfLine{AsOf: asOfInfo{Hash: asOfHash, CommitDate: asOfDate}})
		fmt.Fprintln(stdout, string(b))
	}
	// 계측(T56): zero 히트 여부를 meta read_stats에 누적. 실패는 조용히(§13).
	bumpReadStatsToMeta(ctx, st, len(rows) == 0)
	// 계측(T62): 결과 항목 id 전수 참조 증분 + read 유입 1회. 조용히(§13).
	instrumentReads(ctx, st, "read", rowIDs(rows))
	return 0
}

// asOfInfo / asOfLine — --as-of 결과 첨부 한 줄 JSON {as_of:{hash,commit_date}}.
type asOfInfo struct {
	Hash       string `json:"hash"`
	CommitDate string `json:"commit_date"`
}

type asOfLine struct {
	AsOf asOfInfo `json:"as_of"`
}

// asOfErrLine — {error,message} 한 줄 JSON 조립(errLine의 cli 국소 변형 —
// root.go 공용 errLine은 store 계층 에러만 분류한다).
func asOfErrLine(code, msg string) string {
	b, _ := json.Marshal(map[string]string{"error": code, "message": msg})
	return string(b)
}

// fmtAsOfDate — dolt_log date 셀을 RFC3339 문자열로. time.Time이 아니면
// 그대로 문자열화한다.
func fmtAsOfDate(v any) string {
	if t, ok := v.(time.Time); ok {
		return t.Format(time.RFC3339)
	}
	return fmt.Sprint(v)
}

// wrapViewColumnMismatch — 뷰-컬럼 미호환(column ... does not exist) 오류에
// 기본 테이블 조회 안내를 붙인다. 뷰+AS OF는 "현재 정의 + 과거 행" 규칙
// (C3)이라 과거 커밋에 없는 컬럼에서 이 오류가 난다. 그 외 오류는 불변.
func wrapViewColumnMismatch(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "column") && strings.Contains(msg, "does not exist") {
		return fmt.Errorf("%w — active_knowledge 뷰 정의가 해당 시점 행과 호환되지 않습니다. 기본 테이블(knowledge) AS OF 조회를 시도하세요", err)
	}
	return err
}
