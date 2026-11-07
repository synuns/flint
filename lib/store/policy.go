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
		// v0.23.2(기획 리뷰 결함 1): hot_view '최근 결정' 팔이 domain='decision'을
		// 요구(views.sql)하는데 어휘표에 없어 unknown domain 경고·팔 공회전 —
		// 어휘 4종으로 정합(확정 결정은 CONTENT §1 유형 1 최상위).
		"decision": true,
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
		if !knownTag(n) {
			w := fmt.Sprintf("unknown tag: %s", n)
			if s := SuggestTag(n); len(s) > 0 {
				w += " (유사: " + strings.Join(s, ", ") + ")"
			}
			warnings = append(warnings, w)
		}
	}
	if d := normalizeVocab(domain); d != "" && !knownDomain(d) {
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
	seen := map[string]bool{}
	add := func(k string) bool {
		if seen[k] {
			return false
		}
		seen[k] = true
		out = append(out, k)
		return len(out) == 2
	}
	keys := make([]string, 0, len(known))
	for k := range known {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// ① 접두 일치(의도 확실).
	for _, k := range keys {
		if (strings.HasPrefix(k, n) || strings.HasPrefix(n, k)) && add(k) {
			return out
		}
	}
	// ② 편집거리 ≤2 근사.
	for _, k := range keys {
		if editDistance(n, k) <= 2 && add(k) {
			return out
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
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// SuggestTag·SuggestDomain — 어휘 밖 값의 수정 후보.
func SuggestTag(in string) []string {
	return suggestFrom(in, unionVocab(knownTags, vocabExtraTags))
}

func SuggestDomain(in string) []string {
	return suggestFrom(in, unionVocab(knownDomains, vocabExtraDomains))
}

// unionVocab — 기본 ∪ 개인(제안도 개인 어휘에서).
func unionVocab(base, extra map[string]bool) map[string]bool {
	out := make(map[string]bool, len(base)+len(extra))
	for k := range base {
		out[k] = true
	}
	for k := range extra {
		out[k] = true
	}
	return out
}

// VocabExtras — meta 오버라이드 어휘(v0.24.0 R4a: 개인 어휘를 repo가
// 아닌 운영 DB meta에 — 합집합. 어휘는 늘리는 게 일반 케이스라 기본
// 집합을 대체하지 않는다). SetVocabExtras는 write 경로 시작 시 1회
// 주입(단발 CLI 프로세스 — 갱신 경합 없음).
var (
	vocabExtraTags    = map[string]bool{}
	vocabExtraDomains = map[string]bool{}
)

// SetVocabExtras — 개인 어휘 주입(정규화 후 합집합 반영). 호출 시
// 기존 extras를 대체한다(v0.24.0 리뷰 ⑤: flint mcp 장기 프로세스에서
// reset·변경이 반영되게 — 추가만 하면 영구 잔존).
func SetVocabExtras(tags, domains []string) {
	vocabExtraTags = map[string]bool{}
	vocabExtraDomains = map[string]bool{}
	for _, t := range tags {
		if n := normalizeVocab(t); n != "" {
			vocabExtraTags[n] = true
		}
	}
	for _, d := range domains {
		if n := normalizeVocab(d); n != "" {
			vocabExtraDomains[n] = true
		}
	}
}

// knownTag — 기본 ∪ 개인.
func knownTag(s string) bool { return knownTags[s] || vocabExtraTags[s] }

// knownDomain — 기본 ∪ 개인.
func knownDomain(s string) bool { return knownDomains[s] || vocabExtraDomains[s] }
