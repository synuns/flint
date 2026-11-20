package store

import (
	"strings"
	"testing"
)

// TestStrictMaskPrefixResidue — even when a registered value is a prefix of a
// real secret, strict does not leak the suffix in plaintext (pattern masking first).
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

// TestStrictMaskAllowlistedEntry — a standalone registered value becomes [ALLOWED] (keeping the B2 contract).
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

// TestMarkerNotMutatedByAllowlist — registering a pattern-name substring
// does not mutate the REDACTED markers.
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

// TestMaskSecretsPreservesAllowlist — the diagnostic MaskSecrets' original-text preservation unchanged.
func TestMaskSecretsPreservesAllowlist(t *testing.T) {
	ResetSecretsAllowlistForTest([]string{"doc-token-abc"})
	defer ResetSecretsAllowlistForTest(nil)
	got := MaskSecrets("예시 doc-token-abc 사용")
	if !strings.Contains(got, "doc-token-abc") {
		t.Fatalf("진단용 원문 보존 위반: %q", got)
	}
}

// TestNestedAllowlistLongestFirst — nested registrations prefer the longer
// entry — {"doc", "doc-token-abc"} becomes all [ALLOWED] with no residual plaintext.
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
