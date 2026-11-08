// Package query — flint read/hot 조회 조립. 읽기는 기본 active_knowledge 뷰,
// --status 지정 시에만 base 테이블 knowledge를 직접 조회한다(v0.12.0 R1).
package query

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Filters — flint read 필터(--tag --entity --domain --limit --status).
type Filters struct {
	Tag    string
	Entity string
	Domain string
	Limit  int
	// Status — --status pending|active|invalidated(v0.12.0 R1). 지정 시
	// BuildRead 대신 BuildReadStatus이 base 테이블을 조회한다. BuildRead의
	// 미지정 경로는 이 값을 무시한다(뷰 경로 완전 불변 — 룰링).
	Status string
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

// readColumnIndex — readColumns 내 컬럼 위치(-1=부재). 재랭킹 신호 추출용.
func readColumnIndex(name string) int {
	for i, c := range readColumns {
		if c == name {
			return i
		}
	}
	return -1
}

var readColumns = []string{
	"id", "topic", "summary", "tags", "entity", "domain", "confidence", "status",
	"read_count",
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
// 대소문자 무시 lower 부분문자열 매칭. 비교 문자열 양측에 NFKC 정규화
// 적용(v0.10.0 R2 — NFC/NFD 표기 차이 미스 차단. 원문·SQL 불변).
func fallbackMatchCount(tokens []string, topic string, summary string) int {
	t, s := nfkc(strings.ToLower(topic)), nfkc(strings.ToLower(summary))
	n := 0
	for _, tok := range tokens {
		tok = nfkc(strings.ToLower(tok))
		if strings.Contains(t, tok) || strings.Contains(s, tok) {
			n++
		}
	}
	return n
}

// likeEscape — LIKE 패턴에서 \, %, _ 를 리터럴로 이스케이프.
// 작은따옴표는 placeholder 파라미터로 전달되므로 별도 이스케이프 불필요.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// quoteEscaper — SQL 리터럴 인라인 시 작은따옴표 이스케이프(' → ”).
var quoteEscaper = strings.NewReplacer(`'`, `''`)

// RankOrder — 검색 랭킹 정렬 절(T56): 주제어 정확 일치 우선 → confidence →
// 최신 수집 순. q는 작은따옴표를 ”로 이스케이프해 인라인한다(ORDER BY 식
// 조립용 문자열 절 계약).
// v0.6(T68): 동률에서만 최근 참조 우선 — 말미에 last_read_at DESC NULLS LAST
// 추가(조회시 파생·저장값 불변). NULLS LAST로 미참조 항목은 동률 꼴찌.
// v0.13.0(R1): 채택 재랭킹 — last_read_at 뒤 최종 타이브레이크로 read_count
// DESC 추가(많이 읽힌 항목 우선). 상위 순위 요소는 불변.
func RankOrder(q string) string {
	return fmt.Sprintf("ORDER BY (lower(topic)=lower('%s')) DESC, confidence DESC, ingested_at DESC, last_read_at DESC NULLS LAST, read_count DESC",
		quoteEscaper.Replace(q))
}

// appendReadFilters — q·tag·entity·domain 필터의 where절·placeholder 인자
// 조립 공유체: BuildRead·BuildReadStatus 양쪽이 호출해 두 조립기의 필터
// 해석이 항상 동일하다(복붙 분열 방지). append 순서 = where 절 순서 =
// placeholder 번호 — 호출부의 선행 조건(status 등) 뒤에 이어 붙인다.
func appendReadFilters(where []string, args []any, q string, f Filters) ([]string, []any) {
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
	return where, args
}

// BuildRead — active_knowledge 뷰 조회 SQL과 placeholder 인자를 조립한다.
// 무료 텍스트 q는 lower(topic||' '||summary) LIKE lower('%q%') 매칭(ILIKE 대체).
func BuildRead(q string, f Filters) (string, []any) {
	var where []string
	var args []any

	where, args = appendReadFilters(where, args, q, f)

	sql := "SELECT " + strings.Join(readColumns, ", ") + " FROM active_knowledge"
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += " " + RankOrder(q)
	args = append(args, ClampLimit(f.Limit))
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	return sql, args
}

// BuildReadStatus — --status 지정 경로 조회 SQL 조립(v0.12.0 R1). 뷰가
// invalid_at IS NULL 행만 노출하므로 pending·invalidated 행을 보려면 base
// 테이블 knowledge에서 직접 걸러야 한다. status 조건을 첫 파라미터로 두고
// q·tag·entity·domain 필터는 appendReadFilters 공유 조립, RankOrder·LIMIT·
// likeEscape는 BuildRead 조립을 승계한다. 컬럼·순서는 readColumns 그대로
// (뷰와 동일 원본 테이블).
// status='active'는 뷰의 노출 규칙(invalid_at IS NULL)과 동치로 걸러
// 뷰에 없는 행이 --status active에만 보이는 정의 분열을 막는다.
// pending·invalidated에는 이 조건을 붙이지 않는다 — invalidated는
// invalid_at 설정이 그 상태의 본질(부착 시 영(零)히트), pending은 승격 전
// 상태로 invalid_at 규약이 다르다(룰링).
func BuildReadStatus(q string, f Filters) (string, []any) {
	var where []string
	var args []any

	args = append(args, f.Status)
	where = append(where, fmt.Sprintf("status = $%d", len(args)))
	if f.Status == "active" {
		where = append(where, "invalid_at IS NULL")
	}
	where, args = appendReadFilters(where, args, q, f)

	sql := "SELECT " + strings.Join(readColumns, ", ") + " FROM knowledge WHERE " +
		strings.Join(where, " AND ") + " " + RankOrder(q)
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
		// v0.18.0 R5: 폴백 접합면에 tags 포함 — 태그로만 분류된 지식도
		// 단어 폴백에서 회수된다(tags 저장형 ':a:b:'의 부분 문자열 매칭).
		// 구문(phrase) 쿼리의 접합면은 불변(tags는 --tag 필터 영역).
		conds = append(conds, fmt.Sprintf("lower(topic || ' ' || summary || ' ' || tags) LIKE lower($%d)", len(args)))
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

// rrfK — RRF 표준 상수(순위 완화 계수, 원 논문 k=60).
const rrfK = 60.0

// ReorderByFallbackCount — 폴백 결과 앱 재랭킹(v0.9.6): fallbackMatchCount
// DESC. v0.13.0(R1): 정합수 동률 시 read_count DESC 2차 타이브레이크 —
// SQL RankOrder의 채택 재랭킹과 같은 기준이다. sort.SliceStable — 동률은
// SQL RankOrder 순(조회 순)을 유지한다.
func ReorderByFallbackCount(rows [][]any, tokens []string) {
	if len(rows) < 2 {
		return
	}
	values := make([][]int, len(rows))
	for i, row := range rows {
		topic, summary := "", ""
		if idx := readColumnIndex("topic"); idx >= 0 && idx < len(row) {
			topic, _ = row[idx].(string)
		}
		if idx := readColumnIndex("summary"); idx >= 0 && idx < len(row) {
			summary, _ = row[idx].(string)
		}
		values[i] = []int{
			fallbackMatchCount(tokens, topic, summary),
			readCountOf(row),
			jamoOverlap(tokens, topic, summary),
		}
	}
	scores := rrfFuse(values)
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if scores[idx[a]] != scores[idx[b]] {
			return scores[idx[a]] > scores[idx[b]]
		}
		return idx[a] < idx[b] // 동률은 기존 순서(SQL RankOrder) 유지
	})
	out := make([][]any, len(rows))
	for pos, src := range idx {
		out[pos] = rows[src]
	}
	copy(rows, out)
}

// rrfFuse — 행별 신호값 배열을 받아 RRF 스코어 벡터를 반환: 각 신호
// 내림 순위(1=최상, 동률 동순위)에서 1/(k+rank)를 신호별 합산. 값 자체를
// rank에 대입하면 "높을수록 좋은 값"의 부호가 뒤집혀 최앙값이 이긴다
// (v0.25.0 패치 — 구현 첫 버전의 결함, 회귀 테스트로 봉쇄).
func rrfFuse(values [][]int) []float64 {
	n := len(values)
	if n == 0 {
		return nil
	}
	scores := make([]float64, n)
	for sig := range values[0] {
		// 신호 내림 순위 — 동률은 최고 순위 공유.
		order := make([]int, n)
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			return values[order[a]][sig] > values[order[b]][sig]
		})
		rank := 1
		for pos, row := range order {
			if pos > 0 && values[row][sig] != values[order[pos-1]][sig] {
				rank = pos + 1
			}
			if values[row][sig] > 0 { // 0값은 신호 미기여
				scores[row] += 1.0 / (rrfK + float64(rank))
			}
		}
	}
	return scores
}

// jamoOverlap — v0.25.0 R2(자모 bigram): 한글 토큰을 자모 분해해
// bigram 중첩 수로 근사 유사도를 잰다(오타·어미 변형 흡수 — "백업"과
// "백업용"이 1오타/확장 관계에서 같은 bigram 다수 공유). NFKC 정규화
// 승계. 한글 없는 토큰은 기존 부분 문자열 매칭(fallbackMatchCount)이
// 담당하므로 여기선 0(신호 미기여).
func jamoOverlap(tokens []string, topic, summary string) int {
	n := 0
	hay := jamoBigrams(nfkc(strings.ToLower(topic + " " + summary)))
	for _, tok := range tokens {
		for bg := range jamoBigrams(nfkc(strings.ToLower(tok))) {
			if hay[bg] {
				n++
				break // 토큰당 1점 — 긴 토큰이 지배하지 않게
			}
		}
	}
	return n
}

// jamoBigrams — 문자열의 자모 bigram 집합. 한글 음절은 초·중·종성으로
// 분해(종성 없으면 생략), 비한글은 rune bigram 그대로.
func jamoBigrams(s string) map[string]bool {
	// 한글 음절 분해: 유니코드 계산 (U+AC00..U+D7A3).
	const (
		syllableBase = 0xAC00
		leadBase     = 0x1100 // ㄱ
		vowelBase    = 0x1161 // ㅏ
		trailBase    = 0x11A7 // 없음+1
	)
	var jamo []rune
	for _, r := range s {
		if r >= syllableBase && r <= 0xD7A3 {
			off := r - syllableBase
			jamo = append(jamo, leadBase+off/588, vowelBase+(off%588)/28)
			if t := off % 28; t > 0 {
				jamo = append(jamo, trailBase+t)
			}
		} else {
			jamo = append(jamo, r)
		}
	}
	out := map[string]bool{}
	for i := 0; i+1 < len(jamo); i++ {
		out[string(jamo[i:i+2])] = true
	}
	return out
}

// readCountOf — 행에서 read_count 값(readColumns 인덱스 기준). pgx 숫자형을
// 수용하고 칼럼 부재·비수·nil은 0(동률 취급 — 정렬 불변). v0.25.0: query
// 패키지로 이관(cli.columnIndexOf 의존 제거 — readColumnIndex 사용).
func readCountOf(row []any) int {
	i := readColumnIndex("read_count")
	if i < 0 || i >= len(row) {
		return 0
	}
	switch n := row[i].(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

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
			if idx := readColumnIndex(name); idx >= 0 {
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

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func ReadFieldKnown(name string) bool {
	for _, c := range readColumns {
		if c == name {
			return true
		}
	}
	return false
}

func ReadColumnsHint() string {
	return strings.Join(readColumns, ",")
}
