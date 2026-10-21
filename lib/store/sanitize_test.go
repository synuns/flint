package store

import (
	"strings"
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
