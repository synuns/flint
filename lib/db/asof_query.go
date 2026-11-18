// asof_query.go — AS OF 시점 질의(wire.AsOfQuery 계약의 Doltgres 구현).
// 호출측(lib/knowledge read)이 원시 AS OF 절 조립을 더 이상 하지 않는다 —
// 인라인·해시 문자셋 재검증·뷰 비호환 폴백이 이 계층의 단일 소스다.
// 조립형: "SELECT {Columns} FROM {Table} AS OF '{hash}' {Where} LIMIT {Limit}"
// (Limit은 int 리터럴, 0 이하면 LIMIT 절 생략).
package db

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/synuns/flint/lib/wire"
)

// asOfTables — AS OF 질의 허용 테이블. base(knowledge)와 뷰
// (active_knowledge)만 — 그 외 참조는 조립 전 거부.
var asOfTables = map[string]bool{
	"knowledge":        true,
	"active_knowledge": true,
}

// asOfHashReDB — AS OF 인라인 해시의 엄격 문자셋(소문자 base32계,
// lib/knowledge asOfHashRe와 동등 규칙 — 원본 수정 금지라 db에 동등
// 정의 신규 작성). 엔진 반환값 신뢰의 이중 확인: 위반은 인라인 대신 오류.
var asOfHashReDB = regexp.MustCompile(`^[0-9a-z]{6,64}$`)

// asOfColumnRe — Columns 식별자 검증. 소문자·숫자·밑줄·괄호·별표·공백·
// 쉼표만(함수·표현식 허용 — count(*)·산술). 대문자·문장 구분자·대시 거부.
var asOfColumnRe = regexp.MustCompile(`^[a-z_0-9)(* ,]+$`)

// QueryAsOf — q를 조립해 시점 질의를 실행한다. Where는 테이블 참조 뒤
// raw 꼬리 절("WHERE ... ORDER BY ..." — $N은 Args에 순서 바인딩)로
// repo 내 질의 조립기 산출물만 받는 신뢰 경계(외부 입력은 Columns·Hash·
// Table 검증으로 차단). 뷰(active_knowledge) 비호환 — "column ... does
// not exist"(현재 정의 + 과거 행, C3) — 이면 FallbackTable(base)로
// 재시도하고, 재시도도 실패하거나 폴백 미지정이면 안내 문구 랩 오류.
func (r *Repo) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	rows, err := r.queryAsOfTable(ctx, q, q.Table)
	if err == nil {
		return rows, nil
	}
	if isViewColumnMismatch(err) {
		if q.FallbackTable != "" && q.FallbackTable != q.Table {
			if frows, ferr := r.queryAsOfTable(ctx, q, q.FallbackTable); ferr == nil {
				return frows, nil
			}
		}
		// 원문 보존(read.go wrapViewColumnMismatch 이관) — 안내 문구 유지.
		return nil, fmt.Errorf("%w — the active_knowledge view definition is incompatible with rows at that point in time; try an AS OF query on the base table (knowledge)", err)
	}
	return nil, err
}

// queryAsOfTable — 검증 후 단일 테이블 AS OF 조립 실행.
func (r *Repo) queryAsOfTable(ctx context.Context, q wire.AsOfQuery, table string) ([][]any, error) {
	if !asOfTables[table] {
		return nil, fmt.Errorf("query as of: table %q not allowed (knowledge, active_knowledge)", table)
	}
	if !asOfHashReDB.MatchString(q.Hash) {
		// 원문 정합(read.go) — "as-of hash charset violation".
		return nil, fmt.Errorf("as-of hash charset violation: %q", q.Hash)
	}
	for _, c := range q.Columns {
		if !asOfColumnRe.MatchString(c) {
			return nil, fmt.Errorf("query as of: invalid column expression %q", c)
		}
	}
	var b strings.Builder
	b.WriteString("SELECT ")
	b.WriteString(strings.Join(q.Columns, ", "))
	b.WriteString(" FROM ")
	b.WriteString(table)
	b.WriteString(" AS OF '")
	b.WriteString(q.Hash)
	b.WriteString("'")
	if q.Where != "" {
		b.WriteString(" ")
		b.WriteString(q.Where)
	}
	if q.Limit > 0 {
		b.WriteString(fmt.Sprintf(" LIMIT %d", q.Limit))
	}
	return r.queryRows(ctx, b.String(), q.Args...)
}

// isViewColumnMismatch — 뷰-컬럼 미호환(과거 행에 없는 컬럼) 판정.
// 원문 read.go wrapViewColumnMismatch의 휴리스틱 승계 + Doltgres 뷰 AS OF
// 실측 형태("references invalid table(s) or column(s)") 확장 — 원문
// 휴리스틱만으로는 이 형태를 놓쳐 폴백이 발동하지 않았다(실측).
func isViewColumnMismatch(err error) bool {
	msg := err.Error()
	if strings.Contains(msg, "column") && strings.Contains(msg, "does not exist") {
		return true
	}
	return strings.Contains(msg, "references invalid table(s) or column(s)")
}

// queryRows — pgx 질의를 [][]any로(pool Query 공용 패턴 — rows.Values).
// 질의 실패는 재시도 1회(spec §7).
func (r *Repo) queryRows(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := r.queryRetry(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, verr := rows.Values()
		if verr != nil {
			return nil, verr
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

// queryRetry — pool.Query를 재시도 1회로 실행(모든 db 히스토리 읽기 공용).
func (r *Repo) queryRetry(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return retryOnce(func() (pgx.Rows, error) {
		return r.pool.Query(ctx, sql, args...)
	})
}

// retryOnce — 읽기 재시도 1회(spec §7 — wire.QueryOnce 동등 계약의 db
// 구현 내 사본). 첫 실패 → 1회 재실행 → 최종 결과 전파.
func retryOnce[T any](fn func() (T, error)) (T, error) {
	v, err := fn()
	if err != nil {
		v, err = fn()
	}
	return v, err
}
