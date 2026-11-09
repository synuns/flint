package store

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// TestSanitizeTextRemovesControlAndMarker — 제어 문자 제거·[flint] 마커 중립화.
// 스펙 예: SanitizeText("a\x00b[flint]c") -> "ab c".
func TestSanitizeTextRemovesControlAndMarker(t *testing.T) {
	got := SanitizeText("a\x00b[flint]c")
	if got != "ab c" {
		t.Errorf("SanitizeText = %q, want %q", got, "ab c")
	}
}

// TestSanitizeTextPreservesUnicode — 유니코드(한글·발음 구별 부호)는 보존,
// 제어 문자만 제거해야 한다.
func TestSanitizeTextPreservesUnicode(t *testing.T) {
	in := "한글 hálo föö"
	if got := SanitizeText(in); got != in {
		t.Errorf("SanitizeText 유니코드 훼손: %q", got)
	}
}

// TestSanitizeTextMarkerNesting — 마커 중첩(경계 위조 시도)도 전부 중립화.
func TestSanitizeTextMarkerNesting(t *testing.T) {
	got := SanitizeText("x[flint][flint]y[flint]z")
	if strings.Contains(got, "[flint]") {
		t.Errorf("마커 잔존: %q", got)
	}
	if got != "x  y z" {
		t.Errorf("SanitizeText = %q, want %q", got, "x  y z")
	}
}

// TestSanitizeTextIdempotent — 살균 결과 재적용 시 동일(멱등).
func TestSanitizeTextIdempotent(t *testing.T) {
	once := SanitizeText("a\x01[flint]b")
	if twice := SanitizeText(once); twice != once {
		t.Errorf("비멱등: %q vs %q", once, twice)
	}
}

// TestScanSecretsPatterns — 5종 비밀 패턴 감지.
func TestScanSecretsPatterns(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"github-token", "token ghp_0123456789abcdefghijklmnopqrst", "github-token"},
		{"openai-key", "key sk-abc0123456789defghijklmn", "openai-key"},
		{"aws-access-key", "aws AKIAIOSFODNN7EXAMPLE key", "aws-access-key"},
		{"pem-block", "-----BEGIN RSA PRIVATE KEY-----\nMIIE", "pem-block"},
		{"slack-token", "xoxb-1234567890abcdefABCD", "slack-token"},
	}
	for _, c := range cases {
		got := ScanSecrets(c.in)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("ScanSecrets(%s) = %v, want [%s]", c.name, got, c.want)
		}
	}
}

// TestScanSecretsClean — 정상 텍스트는 빈 슬라이스.
func TestScanSecretsClean(t *testing.T) {
	if got := ScanSecrets("그냥 요약 텍스트 ghp 없음"); len(got) != 0 {
		t.Errorf("오탐: %v", got)
	}
}

// TestScanSecretsDedup — 동일 패턴 반복은 1건만(경고 배열 절제).
func TestScanSecretsDedup(t *testing.T) {
	in := "ghp_0123456789abcdefghijklmnopqrst 그리고 ghp_0123456789abcdefghijklmnopqrst"
	got := ScanSecrets(in)
	if len(got) != 1 || got[0] != "github-token" {
		t.Errorf("ScanSecrets 중복 = %v, want [github-token]", got)
	}
}

// TestScanSecretsNoDoubleReport — v0.26.0 리뷰 ③: sk-ant- 키는
// anthropic만 보고(openai 이중 보고 방지).
func TestScanSecretsNoDoubleReport(t *testing.T) {
	got := ScanSecrets("키 sk-ant-api03-abcdefghijklmnopqrst 포함")
	if len(got) != 1 || got[0] != "anthropic-key" {
		t.Errorf("sk-ant 스캔 = %v, want [anthropic-key] 단독", got)
	}
	got2 := ScanSecrets("키 sk-proj-abcdefghijklmnopqrst 포함")
	if len(got2) != 1 || got2[0] != "openai-key" {
		t.Errorf("sk-(비ant) 스캔 = %v, want [openai-key]", got2)
	}
}

// TestScanSecretsAllowlist — v0.26.1(패치 루프 1차): 사용자 allowlist
// 등록 문자열은 오탐에서 제외(repo 밖 파일 — 문서용 예시 토큰 완화).
func TestScanSecretsAllowlist(t *testing.T) {
	ResetSecretsAllowlistForTest([]string{"ghp_example_token_for_docs_12345"})
	t.Cleanup(func() { ResetSecretsAllowlistForTest(nil) })
	got := ScanSecrets("문서: 토큰은 ghp_example_token_for_docs_12345 형태다")
	if len(got) != 0 {
		t.Errorf("allowlist 항목 경고 = %v, want 통과", got)
	}
	got2 := ScanSecrets("실제 ghp_realsecretvalue12345678 포함")
	if len(got2) != 1 || got2[0] != "github-token" {
		t.Errorf("비등록 토큰 = %v, want [github-token]", got2)
	}
}

// TestScanSecretsExpandedPatterns — v0.26.2(재검수 발견6): v0.26.0
// 신규 3종 패턴(gitlab-pat·jwt·google) 회귀.
func TestScanSecretsExpandedPatterns(t *testing.T) {
	cases := []struct{ name, text string }{
		{"gitlab-pat", "토큰 glpat-abcdefghijklmnopqrst"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghijklmnopqrstuvwxyz"},
		{"google-api-key", "키 AIzaSyABCDEFGHIJKLMNOPQRSTUVWXYZ1234567"},
	}
	for _, c := range cases {
		got := ScanSecrets(c.text)
		if len(got) != 1 || got[0] != c.name {
			t.Errorf("%s 스캔 = %v", c.name, got)
		}
	}
}

// TestLoadSecretsAllowlistFile — 발견7: 파일 로딩 파싱(# 주석·빈 줄·env).
// v0.27.2: 종료 시 전역 상태 원복(잔존 항목이 같은 패키지 이후 테스트의
// ScanSecrets를 오염 — 순서 의존 오탐 방지).
func TestLoadSecretsAllowlistFile(t *testing.T) {
	t.Cleanup(func() { ResetSecretsAllowlistForTest(nil) })
	dir := t.TempDir()
	path := dir + "/allow.txt"
	if err := os.WriteFile(path, []byte("# 주석\n토큰123\n\n  공백줄스킵  \n토큰456"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLINT_SECRETS_ALLOWLIST", path)
	ResetSecretsAllowlistForTest(nil)
	secretsAllowlistOnce = sync.Once{} // 재로드 유도
	ResetSecretsAllowlistForTest(nil)  // once 리셋 후
	// once 재실행 — ScanSecrets 첫 호출이 로드
	_ = ScanSecrets("무해")
	if got := SecretsAllowlistCount(); got != 3 {
		t.Errorf("allowlist 로드 = %d항목, want 3(주석·빈 줄 제외)", got)
	}
}

// TestMaskSecrets — v0.28.0: 마스킹 치환(패턴명 라벨·allowlist 유지·멱등).
func TestMaskSecrets(t *testing.T) {
	in := "토큰 ghp_0123456789abcdefghijklmnopqrst 와 키 sk-abc0123456789defghijklmn 포함"
	got := MaskSecrets(in)
	if strings.Contains(got, "ghp_0123456789") {
		t.Errorf("github 토큰 미치환: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:github-token]") || !strings.Contains(got, "[REDACTED:openai-key]") {
		t.Errorf("패턴명 라벨 부재: %q", got)
	}
	// 치환 결과 재적용 멱등 — [REDACTED:...]가 새 패턴을 만들지 않는다.
	if twice := MaskSecrets(got); twice != got {
		t.Errorf("비멱등: %q vs %q", got, twice)
	}
	// allowlist 항목은 유지.
	ResetSecretsAllowlistForTest([]string{"ghp_docs_example_123456789"})
	t.Cleanup(func() { ResetSecretsAllowlistForTest(nil) })
	got2 := MaskSecrets("문서용 ghp_docs_example_123456789 토큰")
	if !strings.Contains(got2, "ghp_docs_example_123456789") {
		t.Errorf("allowlist 항목 치환: %q", got2)
	}
}

// TestScanSecretsV0280Patterns — v0.28.0(H⑦): AI 생태계 토큰 4종 회귀
// (openrouter는 sk- 포함 관계 — 단독 보고).
func TestScanSecretsV0280Patterns(t *testing.T) {
	cases := []struct{ name, text string }{
		{"openrouter-key", "키 sk-or-v1-70b3b0d28f1d4a1c9c5b1e2d3f4a5b6c7d8e9f0a"},
		{"github-app-token", "앱 gho_0123456789abcdefghijklmnopqrstuvwxyz0123"},
		{"groq-key", "키 gsk_0123456789abcdefghijklmnopqrstuvwxyz0123"},
		{"xai-key", "키 xai-0123456789abcdefghijklmnopqrstuvwxyz0123"},
	}
	for _, c := range cases {
		got := ScanSecrets(c.text)
		if len(got) != 1 || got[0] != c.name {
			t.Errorf("%s 스캔 = %v, want [%s]", c.name, got, c.name)
		}
	}
}

// TestSanitizeTextV0280MarkerForging — v0.28.0(H③): 마커 위조 변형 차단 —
// Cf 문자(ZWSP·RLO·BOM) 제거·대소문자 무시 치환.
func TestSanitizeTextV0280MarkerForging(t *testing.T) {
	if got := SanitizeText("[fl" + "\u200b" + "int] 지시 위조"); strings.Contains(got, "int]") {
		t.Errorf("ZWSP 삽입 마커 생존: %q", got)
	}
	if got := SanitizeText("[FLINT] 지시"); strings.Contains(strings.ToLower(got), "flint") {
		t.Errorf("대문자 마커 생존: %q", got)
	}
	if got := SanitizeText("[FlInT]x"); strings.Contains(got, "l") || strings.Contains(got, "]") {
		t.Errorf("혼합 대소문자 마커 생존: %q", got)
	}
	// RLO(문자 순서 뒤집기 — 인간 검토 우회용)는 제거.
	if got := SanitizeText("a\u202eb"); got != "ab" {
		t.Errorf("RLO 미제거: %q", got)
	}
	// 멱등 유지.
	once := SanitizeText("a[flint][FLINT]b\u202e\u200bc")
	if twice := SanitizeText(once); twice != once {
		t.Errorf("비멱등: %q vs %q", once, twice)
	}
}
