// Package query — flint read/hot 조회 조립. 읽기는 항상 active_knowledge 뷰로 강제한다.
package query

import (
	"encoding/json"
	"fmt"
	"strings"
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

// likeEscape — LIKE 패턴에서 \, %, _ 를 리터럴로 이스케이프.
// 작은따옴표는 placeholder 파라미터로 전달되므로 별도 이스케이프 불필요.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

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

	sql := "SELECT id, topic, summary FROM active_knowledge"
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += " ORDER BY ingested_at DESC"
	args = append(args, ClampLimit(f.Limit))
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	return sql, args
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
func FormatRows(rows [][]any, fields string, jsonMode bool) string {
	if fields == "" {
		fields = "id,topic,summary"
	}
	names := strings.Split(fields, ",")
	var lines []string
	for _, row := range rows {
		vals := make([]string, 0, len(names))
		for i, name := range names {
			v := ""
			if i < len(row) && row[i] != nil {
				v = fmt.Sprint(row[i])
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
