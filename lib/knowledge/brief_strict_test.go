package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// TestBriefMasksAllowlistedEntries — v1.0.10(감사 B2): brief는 기기 밖 —
// allowlist 등록값도 마스킹된다(진단용 dump --mask의 원문 보존과 구분).
func TestBriefMasksAllowlistedEntries(t *testing.T) {
	store.ResetSecretsAllowlistForTest([]string{"doc-token-abc123"})
	defer store.ResetSecretsAllowlistForTest(nil)
	ctx := context.Background()
	rows := briefRows(1, "시연 토큰 doc-token-abc123 사용")
	st := &fakeStore{queryFn: briefQueryFn(rows)}
	var out strings.Builder
	if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit=%d: %s", code, out.String())
	}
	got := out.String()
	if strings.Contains(got, "doc-token-abc123") {
		t.Fatalf("allowlist 등록값이 brief에 평문 유출: %q", got)
	}
	if !strings.Contains(got, "[ALLOWED]") {
		t.Fatalf("등록값 마스킹 마커 없음: %q", got)
	}
}
