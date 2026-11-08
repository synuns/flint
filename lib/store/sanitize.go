package store

import (
	"regexp"
	"strings"
	"unicode"
)

// SanitizeText는 메모리 포이즈닝 대응(ADR-0006) 텍스트 중립화를 수행한다.
// ①제어 문자(ANSI 이스케이프·NUL 등) 제거 — 유니코드 본문은 보존
// ②콘텐츠 내 "[flint]" 마커를 공백으로 치환 — 주입 경계 위조 방지
// 저장 거부가 아닌 경고+살균이며, 멱등이다(재적용 시 동일 결과).
// e.g. SanitizeText("a\x00b[flint]c") -> "ab c"
func SanitizeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.ReplaceAll(b.String(), "[flint]", " ")
}

// secretPatterns — 비밀 패턴 카탈로그. 감지 순서=배열 순서(결정적).
var secretPatterns = []struct {
	name     string
	re       *regexp.Regexp
	specific bool // 포함 관계 상위 패턴(마스킹 우선) — v0.26.0 리뷰 ③
}{
	{"github-token", regexp.MustCompile(`ghp_[0-9A-Za-z]{20,}`), false},
	{"openai-key", regexp.MustCompile(`sk-[0-9A-Za-z_-]{20,}`), false},
	{"aws-access-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`), false},
	{"pem-block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), false},
	{"slack-token", regexp.MustCompile(`xoxb-[0-9A-Za-z-]{10,}`), false},
	// v0.26.0 R1: gitleaks 상위 룰 이식(저장 시점+doctor 사후 스캔 공용).
	{"anthropic-key", regexp.MustCompile(`sk-ant-[0-9A-Za-z_-]{20,}`), true},
	{"gitlab-pat", regexp.MustCompile(`glpat-[0-9A-Za-z_\-]{20,}`), false},
	{"jwt", regexp.MustCompile(`eyJ[0-9A-Za-z_-]{10,}\.[0-9A-Za-z_-]{10,}\.[0-9A-Za-z_-]{10,}`), false},
	{"google-api-key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`), false},
}

// ScanSecrets는 비밀 후보 패턴명을 반환한다(빈 슬라이스=통과).
// v0.26.0 리뷰 ③: 특수 패턴(anthropic sk-ant-)에 포함되는 일반
// 패턴(openai sk-)의 이중 보고 방지 — 특수 패턴 매칭 구간을 마스킹
// 후 일반 스캔(gitleaks 우선순위 관례, RE2 lookahead 불가 대응).
// 동일 패턴 반복은 1건만 — 경고 배열 절제. 저장 거부 아님(경고용).
// e.g. ScanSecrets("ghp_...") -> ["github-token"]
func ScanSecrets(s string) []string {
	var found []string
	seen := map[string]bool{}
	// v0.26.0 리뷰 ③: 특수 패턴 매칭 구간을 먼저 마스킹 — 포함 관계
	// (sk-ant- ⊂ sk-)의 이중 보고 방지(gitleaks 우선순위 관례).
	masked := s
	for _, p := range secretPatterns {
		if !p.specific {
			continue
		}
		masked = p.re.ReplaceAllString(masked, "[REDACTED]")
	}
	for _, p := range secretPatterns {
		target := masked
		if p.specific {
			target = s // 특수 패턴은 원문에서 자기 매칭을 확인
		}
		if p.re.MatchString(target) && !seen[p.name] {
			found = append(found, p.name)
			seen[p.name] = true
		}
	}
	return found
}
