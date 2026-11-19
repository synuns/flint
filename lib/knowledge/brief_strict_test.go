package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// TestBriefMasksAllowlistedEntries — brief is off-device — even
// allowlist-registered values are masked (unlike the raw-value
// preservation of the diagnostic dump --mask).
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
