// export.go — v1.3.0 T2: flint export 원천 조회(ExportRows·ExportRow·
// ExportFilter). 기존 read 조회 원천 승계:
//   - 활성 단일 정의 status='active' AND invalid_at IS NULL(activeKnowledgeCount·
//     BuildReadStatus 공통 규칙). pending은 Status:"all"에만 포함된다.
//   - tag 필터 ':t:' strpos 정확 매칭(appendReadFilters 관례).
//   - base 테이블 knowledge 직접 조회 — active_knowledge 뷰는 superseded_by·
//     invalid_at을 노출하지 않는다(BuildReadStatus와 동일 사유).
//   - LIMIT 없음(전수 export — read의 토큰 예산 clamp는 검색 경로 전용).
//
// 스키마 실측 매핑(스키마 변경 금지 — 이번 패치 불변):
//   - content ← summary, entities ← entity(단일 열), created_at ← ingested_at.
//   - updated_at 열 부재 → COALESCE(invalid_at, ingested_at) 파생(행은 불변
//     커밋이라 invalid_at이 마지막 갱신 시각). Since 컷오프도 동일 정의
//     (PAM §16 증분 차용, 0값=전체).
//   - Kind는 항상 "knowledge"(원천 테이블 판별자 — DB 컬럼 아님).
//   - Source는 origin 열(agent|human|import:pam:* — done 훅·PAM 수입이
//     남기는 기록) 그대로. 왕복 재이식(T9) 해시가 origin 키를 포함하므로
//     빈 값으로 내보내면 origin≠"agent" 행이 재이식에서 dedup 불일치로
//     중복 적립된다(원천 열 보존이 왕복 계약).
package knowledge

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// ExportRow — export 1행(원천 매핑은 상단 패키지 주석 참조).
type ExportRow struct {
	ID           int
	Topic        string
	Content      string
	Kind         string
	Status       string
	Confidence   float64
	Tags         []string
	Entities     []string
	Domain       string
	Source       string
	ContentHash  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	SupersededBy *int
}

// ExportFilter — ExportRows 필터. Status "active"(기본·0값)|"all"
// (invalidated·pending 포함 전체). Tag는 ':t:' 정확 매칭. Since는
// updated_at(COALESCE(invalid_at, ingested_at)) 컷오프 — 0값은 전체.
type ExportFilter struct {
	Status string
	Tag    string
	Since  time.Time
}

// buildExportQuery — export 조회 SQL 조립. 조건 순서 = placeholder 순서
// (tag → since — 테스트 seam이 이 순서를 계약으로 소비한다).
func buildExportQuery(f ExportFilter) (string, []any) {
	var where []string
	var args []any
	if f.Status != "all" { // active 기본(0값 포함) — 활성 단일 정의
		where = append(where, "status = 'active'", "invalid_at IS NULL")
	}
	if f.Tag != "" {
		args = append(args, f.Tag)
		where = append(where, fmt.Sprintf("strpos(tags, ':' || $%d || ':') > 0", len(args)))
	}
	if !f.Since.IsZero() {
		// time.Time 직접 바인드 폐지 — RFC3339Nano 문자열(import_jsonl
		// valid_at 관례와 동일 형식. Doltgres/PG 파라미터 이종 회피 —
		// 컷오프 결과는 문자열화 전후 불변).
		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
		where = append(where, fmt.Sprintf("COALESCE(invalid_at, ingested_at) >= $%d", len(args)))
	}
	sql := "SELECT id, topic, summary, tags, entity, domain, confidence, origin, status, " +
		"content_hash, superseded_by, ingested_at, COALESCE(invalid_at, ingested_at) FROM knowledge"
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	return sql + " ORDER BY id", args
}

// ExportRows — export 원천 조회. 무효 Status는 *store.SchemaError(호출부
// exit 3 매핑), 조회 실패는 그대로 전파(MapExit exit 2 매핑).
func ExportRows(ctx context.Context, st wire.Store, f ExportFilter) ([]ExportRow, error) {
	switch f.Status {
	case "", "active", "all":
	default:
		return nil, &store.SchemaError{Field: "status", Expect: "active|all"}
	}
	sql, args := buildExportQuery(f)
	rows, err := wire.QueryOnce(ctx, st, sql, args)
	if err != nil {
		return nil, err
	}
	out := make([]ExportRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapExportRow(r))
	}
	return out, nil
}

// mapExportRow — SELECT 열 순서 셀 튜플 → ExportRow. pgx 실측형
// (int32/int64·float64·time.Time)과 fake 문자열 셀을 모두 수용한다.
func mapExportRow(r []any) ExportRow {
	cell := func(i int) any {
		if i < len(r) {
			return r[i]
		}
		return nil
	}
	row := ExportRow{Kind: "knowledge"} // 원천 테이블 판별자(상단 주석)
	if id, ok := render.CellToInt(cell(0)); ok {
		row.ID = id
	}
	row.Topic = cellString(cell(1))
	row.Content = cellString(cell(2))
	row.Tags = parseExportTags(cellString(cell(3)))
	if e := cellString(cell(4)); e != "" {
		row.Entities = []string{e}
	}
	row.Domain = cellString(cell(5))
	row.Confidence = cellFloat(cell(6))
	row.Source = cellString(cell(7)) // origin 열 — 왕복 해시 대칭(상단 주석)
	row.Status = cellString(cell(8))
	row.ContentHash = cellString(cell(9))
	if cell(10) != nil {
		if id, ok := render.CellToInt(cell(10)); ok {
			row.SupersededBy = &id
		}
	}
	row.CreatedAt = cellTime(cell(11))
	row.UpdatedAt = cellTime(cell(12))
	return row
}

// parseExportTags — ':a:b:' 정규화 형식 → ["a","b"]. 빈 값·빈 조각은 nil.
func parseExportTags(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ":")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// cellString — nil → "", string은 그대로, 그 외는 fmt.Sprint.
func cellString(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	default:
		return fmt.Sprint(s)
	}
}

// cellFloat — 수치 셀 관용 변환(pgx float64·정수형·문자열 파싱).
func cellFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}

// cellTime — 시각 셀 관용 변환(time.Time 우선, 문자열은 RFC3339 계열
// 파싱 — 파싱 불가는 0값).
func cellTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339,
			"2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999",
		} {
			if pt, err := time.Parse(layout, t); err == nil {
				return pt
			}
		}
	}
	return time.Time{}
}
