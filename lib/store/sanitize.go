package store

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// SanitizeText는 메모리 포이즈닝 대응(ADR-0006) 텍스트 중립화를 수행한다.
// ①제어 문자(ANSI 이스케이프·NUL 등)·형식 문자(U+200B ZWSP·U+202E RLO·
//
//	U+FEFF — v0.28.0: IsControl이 Latin-1만 검사해 통과하던 위조 보이지
//	않는 문자 차단) 제거 — 유니코드 본문은 보존
//
// ②콘텐츠 내 "[flint]" 마커를 공백으로 치환 — 대소문자 무시(v0.28.0:
//
//	"[FLINT]"·"[Flint]" 위조 변형 차단)
//
// 저장 거부가 아닌 경고+살균이며, 멱등이다(재적용 시 동일 결과).
// e.g. SanitizeText("a\x00b[flint]c") -> "ab c"
func SanitizeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	return replaceMarkerFold(b.String())
}

// replaceMarkerFold — "[flint]" 마커의 대소문자 무시 치환(ASCII 마커라
// 룬 스캔으로 길이 불일치 없이 원문을 보존하며 치환한다).
func replaceMarkerFold(s string) string {
	const marker = "[flint]"
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(runes); {
		if markerFoldAt(runes, i) {
			b.WriteString(" ")
			i += len(marker)
			continue
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String()
}

// markerFoldAt — runes[i:]가 대소문자 무시 마커로 시작하는지.
func markerFoldAt(runes []rune, i int) bool {
	const marker = "[flint]"
	for j := 0; j < len(marker); j++ {
		if i+j >= len(runes) || unicode.ToLower(runes[i+j]) != rune(marker[j]) {
			return false
		}
	}
	return true
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
	// v0.28.0(H⑦): AI 도구생태계 상위 토큰 갱신(gitleaks 추적).
	// openrouter는 sk-or-가 sk-(openai)에 포함 — specific 마스킹 우선.
	// github-app은 gh[osr](OAuth·server·refresh) — p(ghp_)는 기존
	// github-token이 이미 커버해 제외(리뷰 2: 이중 보고 방지).
	{"openrouter-key", regexp.MustCompile(`sk-or-[0-9A-Za-z-]{20,}`), true},
	{"github-app-token", regexp.MustCompile(`gh[osr]_[0-9A-Za-z]{36,}`), false},
	{"groq-key", regexp.MustCompile(`gsk_[0-9A-Za-z]{30,}`), false},
	{"xai-key", regexp.MustCompile(`xai-[0-9A-Za-z]{30,}`), false},
}

// ScanSecrets는 비밀 후보 패턴명을 반환한다(빈 슬라이스=통과).
// v0.26.0 리뷰 ③: 특수 패턴(anthropic sk-ant-)에 포함되는 일반
// 패턴(openai sk-)의 이중 보고 방지 — 특수 패턴 매칭 구간을 마스킹
// 후 일반 스캔(gitleaks 우선순위 관례, RE2 lookahead 불가 대응).
// 동일 패턴 반복은 1건만 — 경고 배열 절제. 저장 거부 아님(경고용).
// e.g. ScanSecrets("ghp_...") -> ["github-token"]
func ScanSecrets(s string) []string {
	secretsAllowlistOnce.Do(LoadSecretsAllowlist)
	var found []string
	seen := map[string]bool{}
	// v0.26.1: 사용자 allowlist 항목 우선 마스킹(오탐 완화 — 문서용
	// 예시 토큰 등) 후 특수 패턴 마스킹.
	s = maskAllowlisted(s)
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

// MaskSecrets — 비밀 패턴 매칭 구간을 [REDACTED:패턴명]으로 치환한 사본
// 반환(v0.28.0 백업 마스킹 — 진단·공유용 덤프). allowlist 항목은 원문
// 그대로 유지(사용자가 등록한 문서용 예시는 치환하지 않는다 — 치환
// 전 sentinel로 보호·치환 후 복원. maskAllowlisted의 [ALLOWED] 개조와
// 달리 내용을 보존한다). 특수→일반 순서는 ScanSecrets와 동일(포함
// 관계 이중 치환 방지).
func MaskSecrets(s string) string {
	type keep struct{ sentinel, orig string }
	var keeps []keep
	for i, a := range secretsAllowlist {
		if a == "" || !strings.Contains(s, a) {
			continue
		}
		sent := "\x00FLINTMASKKEEP" + strconv.Itoa(i) + "\x00" // 패턴 형태와 무충돌
		s = strings.ReplaceAll(s, a, sent)
		keeps = append(keeps, keep{sent, a})
	}
	for _, p := range secretPatterns {
		if p.specific {
			s = p.re.ReplaceAllString(s, "[REDACTED:"+p.name+"]")
		}
	}
	for _, p := range secretPatterns {
		if !p.specific {
			s = p.re.ReplaceAllString(s, "[REDACTED:"+p.name+"]")
		}
	}
	for _, k := range keeps {
		s = strings.ReplaceAll(s, k.sentinel, k.orig)
	}
	return s
}

// ── v0.26.1(패치 루프 1차): 비밀 스캔 allowlist — 오탐 완화(F 리스크 #1).
// repo 밖 사용자 파일(~/.config/flint/secrets-allowlist.txt, 한 줄 하나)로
// 지식·필드에 안전하게 나타나는 문자열(문서용 예시 토큰·프로젝트 고유
// 접두사 등)을 등록하면 해당 부분 문자열을 마스킹 후 스캔(personal-markers
// 관례 승계 — repo에 개인 기준을 두지 않는다). FLINT_SECRETS_ALLOWLIST
// env로 경로 오버라이드(테스트 격리).
var (
	secretsAllowlistOnce sync.Once
	secretsAllowlist     []string
)

// LoadSecretsAllowlist — allowlist 파일을 읽어 저장(단발 프로세스 캐시).
func LoadSecretsAllowlist() {
	path := os.Getenv("FLINT_SECRETS_ALLOWLIST")
	if path == "" {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, ".config", "flint", "secrets-allowlist.txt")
		}
	}
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return // 부재·읽기 실패는 빈 목록 — 조용히(계측 아님, 스캔 계약 유지)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
			secretsAllowlist = append(secretsAllowlist, t)
		}
	}
}

// ResetSecretsAllowlistForTest — 테스트 격리(프로덕션 미사용).
func ResetSecretsAllowlistForTest(entries []string) {
	secretsAllowlistOnce = sync.Once{}
	secretsAllowlist = entries
}

// MaskSecretsStrict — 기기 밖(브리지·공유)용 마스킹: 패턴 비밀과
// allowlist 등록값을 모두 마스킹한다(v1.0.10 감사 B2 — dump --mask는
// 진단용 원문 보존, 기기 밖은 등록값도 평문으로 나가지 않는다).
// v1.0.11(R12-B1): sentinel 보호 없이 패턴 마스킹 먼저 — 등록값이 더
// 긴 실제 비밀의 접두사인 경우 매칭이 놓여 접미가 평문 유출되던 조합
// 결함(실증) 폐지. 등록값이 비밀의 일부면 패턴 마스킹이 이긴다.
func MaskSecretsStrict(s string) string {
	for _, p := range secretPatterns {
		if p.specific {
			s = p.re.ReplaceAllString(s, "[REDACTED:"+p.name+"]")
		}
	}
	for _, p := range secretPatterns {
		if !p.specific {
			s = p.re.ReplaceAllString(s, "[REDACTED:"+p.name+"]")
		}
	}
	return maskAllowlisted(s)
}

// maskAllowlisted — allowlist 항목을 마스킹한 사본 반환.
// v1.0.11(R12-B2): [REDACTED:…]·[ALLOWED] 마커는 sentinel으로 먼저
// 보호해 치환 대상에서 제외한다 — 패턴명 부분문자열("token")이 등록
// 돼도 감사 흔적 마커가 개조되지 않는다(MaskSecrets sentinel 관례 승계).
func maskAllowlisted(s string) string {
	type keep struct{ sentinel, orig string }
	var keeps []keep
	for i, m := range markerRes.FindAllString(s, -1) {
		if m == "" {
			continue
		}
		sent := "\x01FLINTMARKKEEP" + strconv.Itoa(i) + "\x01"
		s = strings.Replace(s, m, sent, 1)
		keeps = append(keeps, keep{sent, m})
	}
	// v1.0.11(R13-2): 긴 등록값 우선 — {"doc","doc-token-abc"} 순회 시
	// 짧은 항목이 먼저 치환되어 "[ALLOWED]-token-abc" 잔여 평문이 남는다
	// (실증). 내림차순 정렬로 긴 항목이 먼저 매칭된다.
	sortedAllow := append([]string(nil), secretsAllowlist...)
	sort.Slice(sortedAllow, func(i, j int) bool { return len(sortedAllow[i]) > len(sortedAllow[j]) })
	for _, a := range sortedAllow {
		if a != "" {
			s = strings.ReplaceAll(s, a, "[ALLOWED]")
		}
	}
	for _, k := range keeps {
		s = strings.Replace(s, k.sentinel, k.orig, 1)
	}
	return s
}

// markerRes — 보호 대상 감사 마커(REDACTED 패턴명·ALLOWED).
var markerRes = regexp.MustCompile(`\[(?:REDACTED:[^\]]*|ALLOWED)\]`)

// SecretsAllowlistCount — 현재 로드된 allowlist 항목 수(doctor 가시성 —
// v0.26.2 재검수 발견3: 오타·무단 등록 감지).
func SecretsAllowlistCount() int {
	return len(secretsAllowlist)
}
