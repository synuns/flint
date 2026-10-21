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
	name string
	re   *regexp.Regexp
}{
	{"github-token", regexp.MustCompile(`ghp_[0-9A-Za-z]{20,}`)},
	{"openai-key", regexp.MustCompile(`sk-[0-9A-Za-z_-]{20,}`)},
	{"aws-access-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"pem-block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"slack-token", regexp.MustCompile(`xoxb-[0-9A-Za-z-]{10,}`)},
}

// ScanSecrets는 비밀 후보 패턴명을 반환한다(빈 슬라이스=통과).
// 동일 패턴 반복은 1건만 — 경고 배열 절제. 저장 거부 아님(경고용).
// e.g. ScanSecrets("ghp_...") -> ["github-token"]
func ScanSecrets(s string) []string {
	var found []string
	seen := map[string]bool{}
	for _, p := range secretPatterns {
		if p.re.MatchString(s) && !seen[p.name] {
			found = append(found, p.name)
			seen[p.name] = true
		}
	}
	return found
}
