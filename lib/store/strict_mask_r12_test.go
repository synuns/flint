package store

import (
	"strings"
	"testing"
)

// TestStrictMaskPrefixResidue — v1.0.11(R12-B1): 등록값이 실제 비밀의
// 접두사여도 strict은 접미를 평문 유출하지 않는다(패턴 마스킹 우선).
func TestStrictMaskPrefixResidue(t *testing.T) {
	ResetSecretsAllowlistForTest([]string{"ghp_" + strings.Repeat("a", 20)})
	defer ResetSecretsAllowlistForTest(nil)
	real := "ghp_" + strings.Repeat("a", 20) + strings.Repeat("Z", 20)
	got := MaskSecretsStrict(real)
	if strings.Contains(got, "ZZZZ") {
		t.Fatalf("접미 평문 유출: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:github-token]") {
		t.Fatalf("패턴 마스킹 흔적 없음: %q", got)
	}
}

// TestStrictMaskAllowlistedEntry — 등록값 단독은 [ALLOWED](B2 계약 유지).
func TestStrictMaskAllowlistedEntry(t *testing.T) {
	ResetSecretsAllowlistForTest([]string{"doc-token-abc"})
	defer ResetSecretsAllowlistForTest(nil)
	got := MaskSecretsStrict("예시 doc-token-abc 사용")
	if strings.Contains(got, "doc-token-abc") {
		t.Fatalf("등록값 평문: %q", got)
	}
	if !strings.Contains(got, "[ALLOWED]") {
		t.Fatalf("[ALLOWED] 없음: %q", got)
	}
}

// TestMarkerNotMutatedByAllowlist — v1.0.11(R12-B2): 패턴명 부분문자열
// 등록으로 REDACTED 마커가 개조되지 않는다.
func TestMarkerNotMutatedByAllowlist(t *testing.T) {
	ResetSecretsAllowlistForTest([]string{"token"})
	defer ResetSecretsAllowlistForTest(nil)
	masked := MaskSecrets("ghp_" + strings.Repeat("b", 24)) // [REDACTED:github-token]
	got := maskAllowlisted(masked)
	if strings.Contains(got, "[ALLOWED]]") || strings.Contains(got, "github-[ALLOWED]") {
		t.Fatalf("마커 개조: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:github-token]") {
		t.Fatalf("마커 비보존: %q", got)
	}
}

// TestMaskSecretsPreservesAllowlist — 진단용 MaskSecrets 원문 보존 불변.
func TestMaskSecretsPreservesAllowlist(t *testing.T) {
	ResetSecretsAllowlistForTest([]string{"doc-token-abc"})
	defer ResetSecretsAllowlistForTest(nil)
	got := MaskSecrets("예시 doc-token-abc 사용")
	if !strings.Contains(got, "doc-token-abc") {
		t.Fatalf("진단용 원문 보존 위반: %q", got)
	}
}

// TestNestedAllowlistLongestFirst — v1.0.11(R13-2): 중첩 등록은 긴 항목
// 우선 — {"doc","doc-token-abc"}에서 잔여 평문 없이 전부 [ALLOWED].
func TestNestedAllowlistLongestFirst(t *testing.T) {
	ResetSecretsAllowlistForTest([]string{"doc", "doc-token-abc"})
	defer ResetSecretsAllowlistForTest(nil)
	got := MaskSecretsStrict("예시 doc-token-abc 사용")
	if strings.Contains(got, "token-abc") || strings.Contains(got, "-[ALLOWED]") {
		t.Fatalf("중첩 등록 잔여: %q", got)
	}
	if !strings.Contains(got, "[ALLOWED]") {
		t.Fatalf("[ALLOWED] 없음: %q", got)
	}
}
