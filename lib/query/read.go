// Package query — flint read/hot 조회 조립. 읽기는 항상 active_knowledge 뷰로 강제한다.
package query

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Filters — flint read 필터(--tag --entity --domain --limit).
type Filters struct {
	Tag    string
	Entity string
	Domain string
	Limit  int
}

const (
	defaultLimit = 10
	maxLimit     = 50
	summaryMax   = 80

	// ReadFallbackCountKey — 제로히트 폴백 시도 횟수 meta 계측 키(v0.9.6).
	ReadFallbackCountKey = "read_fallback_calls"
	// maxQueryTokens — 폴백 OR 스캔 비용 가드(룰링): 초과 토큰은 버린다.
	maxQueryTokens = 8
)

// ClampLimit — limit 기본 10, 상한 50 (토큰 예산 박제).
func ClampLimit(n int) int {
	if n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

// readColumns — BuildRead이 SELECT하는 컬럼(고정 순서). FormatRows의
// fields 이름→위치 매핑 기준이 된다.
var readColumns = []string{
	"id", "topic", "summary", "tags", "entity", "domain", "confidence", "status",
}

// tokenizeQuery — 무료 텍스트 q를 검색 토큰으로 쪼갠다(v0.9.6 폴백용).
// strings.Fields 분리 후 각 토큰의 선행/후행 구두점(unicode.IsPunct)을
// 제거하고 빈 토큰은 버린다. 상한 8 — 초과 토큰은 조용히 버림(에러 아님).
func tokenizeQuery(q string) []string {
	fields := strings.Fields(q)
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		f = trimPunct(f)
		if f != "" {
			tokens = append(tokens, f)
		}
		if len(tokens) == maxQueryTokens {
			break
		}
	}
	return tokens
}

// TokenizeQuery — tokenizeQuery 내보내기(폴백 재랭킹 호출부·lib/cli용).
func TokenizeQuery(q string) []string { return tokenizeQuery(q) }

// trimPunct — 토큰 양끝 구두점 제거(내부 구두점은 유지 — 'a-b' 등).
func trimPunct(s string) string {
	r := []rune(s)
	for len(r) > 0 && unicode.IsPunct(r[0]) {
		r = r[1:]
	}
	for len(r) > 0 && unicode.IsPunct(r[len(r)-1]) {
		r = r[:len(r)-1]
	}
	return string(r)
}

// fallbackMatchCount — 토큰이 topic·summary에 포함되는 개수(재랭킹 점수).
// 대소문자 무시 lower 부분문자열 매칭.
func fallbackMatchCount(tokens []string, topic string, summary string) int {
	t, s := strings.ToLower(topic), strings.ToLower(summary)
	n := 0
	for _, tok := range tokens {
		tok = strings.ToLower(tok)
		if strings.Contains(t, tok) || strings.Contains(s, tok) {
			n++
		}
	}
	return n
}

// likeEscape — LIKE 패턴에서 \, %, _ 를 리터럴로 이스케이프.
// 작은따옴표는 placeholder 파라미터로 전달되므로 별도 이스케이프 불필요.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// quoteEscaper — SQL 리터럴 인라인 시 작은따옴표 이스케이프(' → '').
var quoteEscaper = strings.NewReplacer(`'`, `''`)

// RankOrder — 검색 랭킹 정렬 절(T56): 주제어 정확 일치 우선 → confidence →
// 최신 수집 순. q는 작은따옴표를 ''로 이스케이프해 인라인한다(ORDER BY 식
// 조립용 문자열 절 계약).
// v0.6(T68): 동률에서만 최근 참조 우선 — 말미에 last_read_at DESC NULLS LAST
// 추가(조회시 파생·저장값 불변). NULLS LAST로 미참조 항목은 동률 꼴찌.
func RankOrder(q string) string {
	return fmt.Sprintf("ORDER BY (lower(topic)=lower('%s')) DESC, confidence DESC, ingested_at DESC, last_read_at DESC NULLS LAST",
		quoteEscaper.Replace(q))
}

// BuildRead — active_knowledge 뷰 조회 SQL과 placeholder 인자를 조립한다.
// 무료 텍스트 q는 lower(topic||' '||summary) LIKE lower('%q%') 매칭(ILIKE 대체).
func BuildRead(q string, f Filters) (string, []any) {
	var where []string
	var args []any

	if q != "" {
		args = append(args, "%"+likeEscaper.Replace(q)+"%")
		where = append(where, fmt.Sprintf("lower(topic || ' ' || summary) LIKE lower($%d)", len(args)))
	}
	if f.Tag != "" {
		args = append(args, f.Tag)
		// tags 저장 형태 ':a:b:' — 부분 문자열 오탐 방지를 위해 ':tag:' 정확 매칭.
		where = append(where, fmt.Sprintf("strpos(tags, ':' || $%d || ':') > 0", len(args)))
	}
	if f.Entity != "" {
		args = append(args, f.Entity)
		where = append(where, fmt.Sprintf("entity = $%d", len(args)))
	}
	if f.Domain != "" {
		args = append(args, f.Domain)
		where = append(where, fmt.Sprintf("domain = $%d", len(args)))
	}

	sql := "SELECT " + strings.Join(readColumns, ", ") + " FROM active_knowledge"
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += " " + RankOrder(q)
	args = append(args, ClampLimit(f.Limit))
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	return sql, args
}

// BuildReadFallback — 제로히트 폴백 조회 SQL 조립(v0.9.6). 토큰별
// OR LIKE + 기존 필터(tag·entity·domain)·active_knowledge 뷰·likeEscape·
// RankOrder·LIMIT 승계. 토큰 2개 미만은 구문 매칭과 동질이라 ok=false
// (폴백 생략 — 룰링).
func BuildReadFallback(q string, f Filters) (string, []any, bool) {
	tokens := tokenizeQuery(q)
	if len(tokens) < 2 {
		return "", nil, false
	}
	var conds []string
	var args []any
	for _, tok := range tokens {
		args = append(args, "%"+likeEscaper.Replace(tok)+"%")
		conds = append(conds, fmt.Sprintf("lower(topic || ' ' || summary) LIKE lower($%d)", len(args)))
	}
	where := []string{"(" + strings.Join(conds, " OR ") + ")"}
	if f.Tag != "" {
		args = append(args, f.Tag)
		where = append(where, fmt.Sprintf("strpos(tags, ':' || $%d || ':') > 0", len(args)))
	}
	if f.Entity != "" {
		args = append(args, f.Entity)
		where = append(where, fmt.Sprintf("entity = $%d", len(args)))
	}
	if f.Domain != "" {
		args = append(args, f.Domain)
		where = append(where, fmt.Sprintf("domain = $%d", len(args)))
	}
	sql := "SELECT " + strings.Join(readColumns, ", ") + " FROM active_knowledge WHERE " +
		strings.Join(where, " AND ") + " " + RankOrder(q)
	args = append(args, ClampLimit(f.Limit))
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	return sql, args, true
}

// ReorderByFallbackCount — 폴백 결과 앱 재랭킹(v0.9.6): fallbackMatchCount
// DESC. sort.SliceStable — 동률은 SQL RankOrder 순(조회 순)을 유지한다.
func ReorderByFallbackCount(rows [][]any, tokens []string) {
	rowCount := func(row []any) int {
		topic, summary := "", ""
		if i := columnIndexOf("topic"); i >= 0 && i < len(row) {
			topic, _ = row[i].(string)
		}
		if i := columnIndexOf("summary"); i >= 0 && i < len(row) {
			summary, _ = row[i].(string)
		}
		return fallbackMatchCount(tokens, topic, summary)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rowCount(rows[i]) > rowCount(rows[j])
	})
}

// columnIndexOf — fields 이름 → readColumns 인덱스. 없으면 -1.
func columnIndexOf(name string) int {
	for i, c := range readColumns {
		if c == name {
			return i
		}
	}
	return -1
}

// truncateRunes — rune 기반 절단(한글 등 멀티바이트 안전).
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// FormatRows — 조회 결과 출력 포맷.
// 기본: fields 순서의 컬럼을 '|'로 조인, summary는 80자 rune 절단.
// jsonMode: 행별 한 줄 JSON, 선택 fields 키만 포함.
// fields 이름은 readColumns 기준으로 매핑하고, 매핑에 없는 이름(hot_view의
// kind·ts 등)은 행 내 위치순으로 해석한다.
func FormatRows(rows [][]any, fields string, jsonMode bool) string {
	if fields == "" {
		fields = "id,topic,summary"
	}
	names := strings.Split(fields, ",")
	var lines []string
	for _, row := range rows {
		vals := make([]string, 0, len(names))
		for i, name := range names {
			var raw any
			if idx := columnIndexOf(name); idx >= 0 {
				if idx < len(row) {
					raw = row[idx]
				}
			} else if i < len(row) {
				raw = row[i] // 매핑 밖 필드는 위치 fallback
			}
			v := ""
			if raw != nil {
				v = fmt.Sprint(raw)
			}
			if name == "summary" {
				v = truncateRunes(v, summaryMax)
			}
			vals = append(vals, v)
		}
		if jsonMode {
			m := make(map[string]string, len(names))
			for i, name := range names {
				m[name] = vals[i]
			}
			// encoding/json은 map 키를 정렬해 직렬화하므로 출력이 안정적이다.
			b, err := json.Marshal(m)
			if err != nil {
				continue
			}
			lines = append(lines, string(b))
		} else {
			lines = append(lines, strings.Join(vals, "|"))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
