// suggest.go — v0.10.0 T1: 제로히트 did-you-mean 제안(스펙 §3 R1·R2).
// 유사도·정렬은 전부 앱 사이드 순수 함수 — 구문 LIKE SQL·출력 불변.
package query

import (
	"sort"
	"strconv"

	"golang.org/x/text/unicode/norm"
)

// Suggestion — 제안 한 건(active_knowledge 행의 id·topic).
type Suggestion struct {
	ID    int    `json:"id"`
	Topic string `json:"topic"`
}

// nfkc — 매칭 비교 문자열 Unicode NFKC 정규화(R2). NFC/NFD·조합형 표기
// 차이로 인한 미스를 차단한다. 비교 문자열에만 적용 — 원문·SQL·출력 불변.
func nfkc(s string) string { return norm.NFKC.String(s) }

// bigramDice — rune bigram 집합의 Dice 계수(0~1). 양변 모두 bigram이
// 없는 1-rune 문자열은 동등성 판정(1/0). 빈 문자열·공집합은 0.
func bigramDice(a string, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 2 && len(rb) < 2 {
		if len(ra) == 1 && len(rb) == 1 && ra[0] == rb[0] {
			return 1
		}
		return 0
	}
	sa, sb := bigramSet(ra), bigramSet(rb)
	if len(sa)+len(sb) == 0 {
		return 0
	}
	inter := 0
	for k := range sa {
		if _, ok := sb[k]; ok {
			inter++
		}
	}
	return 2 * float64(inter) / float64(len(sa)+len(sb))
}

// bigramSet — rune 슬라이스의 인접 bigram 집합(중복 제거).
func bigramSet(r []rune) map[string]struct{} {
	set := make(map[string]struct{}, len(r))
	for i := 0; i+1 < len(r); i++ {
		set[string(r[i:i+2])] = struct{}{}
	}
	return set
}

// BigramDice — rune bigram 집합 Dice 계수 공개 래퍼(집합판 bigramDice).
// suggest 제안 점수와 같은 측정을 패키지 밖(cli 등)에서 재사용할 때 쓴다.
func BigramDice(a string, b string) float64 { return bigramDice(a, b) }

// BigramDiceMultiset — rune bigram 다중집합 Dice 계수(maintain 모순 분류
// 측정). 반복 bigram을 집합처럼 접지 않고 출현 수로 세아려, 같은 bigram이
// 반복되는 문장에서 집합판보다 유사도를 보수적으로 낸다 —
// cli.conflictDupThreshold(0.6) 룰링은 이 측정 기준이므로 집합판으로
// 통합하지 않는다. bigram이 없는 짧은 문자열(2 미만 rune)은 둘 다 비면
// 1(동일), 아니면 0(비교 불가=이질)으로 처리한다.
func BigramDiceMultiset(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 2 || len(rb) < 2 {
		if len(ra) == 0 && len(rb) == 0 {
			return 1
		}
		return 0
	}
	ma := map[string]int{}
	for i := 0; i+1 < len(ra); i++ {
		ma[string(ra[i:i+2])]++
	}
	mb := map[string]int{}
	for i := 0; i+1 < len(rb); i++ {
		mb[string(rb[i:i+2])]++
	}
	inter := 0
	for k, n := range ma {
		if m, ok := mb[k]; ok {
			if m < n {
				inter += m
			} else {
				inter += n
			}
		}
	}
	return 2 * float64(inter) / float64(len(ra)-1+len(rb)-1)
}

// suggestCellInt — 스캔 id 셀(any)→int. pgx 숫자형·float64·10진 문자열
// 수용, 그 외 (0, false) — cli.cellToInt와 같은 계약의 query 국소 복제.
func suggestCellInt(v any) (int, bool) {
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

// suggestMinScore — 제안 채택 하한(v0.10.1 R1). 이 점수 미만 행은
// 무관 후보로 간주해 제외한다. 메타 오버라이드는 데이터 부족으로 이연.
const suggestMinScore = 0.15

// SuggestSimilar — 질의 q와 스캔 행([0]=id·[1]=topic·[2]=summary)의
// NFKC 정규화 bigram Dice 유사도로 상위 topN 제안. suggestMinScore
// 미만(0 포함)은 제외, 내림차순·동률은 id 오름차순. topN 이하는 그대로 반환.
func SuggestSimilar(q string, rows [][]any, topN int) []Suggestion {
	nq := nfkc(q)
	type scored struct {
		sug   Suggestion
		score float64
	}
	var cs []scored
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		id, ok := suggestCellInt(row[0])
		if !ok {
			continue
		}
		topic, _ := row[1].(string)
		summary, _ := row[2].(string)
		score := bigramDice(nq, nfkc(topic+summary))
		if score < suggestMinScore {
			continue
		}
		cs = append(cs, scored{Suggestion{ID: id, Topic: topic}, score})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].score != cs[j].score {
			return cs[i].score > cs[j].score
		}
		return cs[i].sug.ID < cs[j].sug.ID
	})
	if topN > 0 && len(cs) > topN {
		cs = cs[:topN]
	}
	if len(cs) == 0 {
		return nil
	}
	out := make([]Suggestion, len(cs))
	for i, c := range cs {
		out[i] = c.sug
	}
	return out
}

// suggestScanCap — 제안 스캔 행 상한(N13). 스펙 §3 R1의 '스캔 상한 없음'을
// 정정한다: 개인 지식관리 규모(수천 행)는 전부 수용하면서, 대량 유입 등
// 이상 규모에서 무상한 스캔이 제로히트 경로를 지연시키는 것을 방어한다.
// 상한은 정렬 없는 방어선이다 — 제안은 상위 topN 재랭킹이므로 어느 5000행이
// 걸리든 근접 후보 제공이라는 목적은 유지된다.
const suggestScanCap = 5000

// BuildSuggestScan — 제안용 active_knowledge 스캔 SQL. 필터 없음(제안은
// 필터 무관 전체 active 대상 — 스펙 §3 R1), 상한은 suggestScanCap.
func BuildSuggestScan() (string, []any) {
	return "SELECT id, topic, summary FROM active_knowledge LIMIT " + strconv.Itoa(suggestScanCap), nil
}
