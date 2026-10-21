package store

import (
	"fmt"
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
			warnings = append(warnings, fmt.Sprintf("unknown tag: %s", n))
		}
	}
	if d := normalizeVocab(domain); d != "" && !knownDomains[d] {
		warnings = append(warnings, fmt.Sprintf("unknown domain: %s", d))
	}
	return warnings
}
