package store

import (
	"fmt"
	"sort"
	"strings"
)

// CONTENT.md §4 어휘표 — tags 7종·domain 3종(필요시 표 먼저 확장).
var (
	knownTags = map[string]bool{
		"adr":        true,
		"correction": true,
		"failure":    true,
		"env":        true,
		"process":    true,
		"contract":   true,
		"playbook":   true,
	}
	knownDomains = map[string]bool{
		"main":       true,
		"infra":      true,
		"preference": true,
	}
)

// normalizeVocab은 어휘 값을 소문자+공백 제거로 정규화한다(대소문자 경계 흡수).
func normalizeVocab(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// CheckVocabulary는 tags·domain이 CONTENT.md §4 어휘표 안에 있는지 검증해
// 표 밖 값마다 경고 문자열 1개를 반환한다(빈 슬라이스=통과, 저장 거부 아님).
// domain이 빈 값이면 검사하지 않는다(선택 인자 규약).
// e.g. CheckVocabulary(["playbook","xxx"],"main") -> ["unknown tag: xxx"]
func CheckVocabulary(tags []string, domain string) []string {
	var warnings []string
	for _, tag := range tags {
		n := normalizeVocab(tag)
		if !knownTags[n] {
			w := fmt.Sprintf("unknown tag: %s", n)
			if s := SuggestTag(n); len(s) > 0 {
				w += " (유사: " + strings.Join(s, ", ") + ")"
			}
			warnings = append(warnings, w)
		}
	}
	if d := normalizeVocab(domain); d != "" && !knownDomains[d] {
		w := fmt.Sprintf("unknown domain: %s", d)
		if s := SuggestDomain(d); len(s) > 0 {
			w += " (유사: " + strings.Join(s, ", ") + ")"
		}
		warnings = append(warnings, w)
	}
	return warnings
}

// suggestFrom — 어휘 집합에서 입력과 가장 가까운 후보 최대 2개
// (접두 일치 우선, 다음 편집거리 ≤2 — v0.23.0 D②: 어휘 밖 값에 대한
// 오타 수정 제안).
func suggestFrom(in string, known map[string]bool) []string {
	n := normalizeVocab(in)
	if n == "" {
		return nil
	}
	var out []string
	// ① 접두 일치(의도 확실) — 대소 정렬로 결정적 순서.
	keys := make([]string, 0, len(known))
	for k := range known {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.HasPrefix(k, n) || strings.HasPrefix(n, k) {
			out = append(out, k)
			if len(out) == 2 {
				return out
			}
		}
	}
	// ② 편집거리 ≤2 근사.
	for _, k := range keys {
		if editDistance(n, k) <= 2 {
			out = append(out, k)
			if len(out) == 2 {
				return out
			}
		}
	}
	return out
}

// editDistance — 레벤슈타인(소문자·짧은 어휘 규모 — 전형적 DP).
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = minInt(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func minInt(vs ...int) int {
	m := vs[0]
	for _, v := range vs[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// SuggestTag·SuggestDomain — 어휘 밖 값의 수정 후보.
func SuggestTag(in string) []string    { return suggestFrom(in, knownTags) }
func SuggestDomain(in string) []string { return suggestFrom(in, knownDomains) }
