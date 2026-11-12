package ops

// maskExtraJSON — runBackup --mask 경로의 extra 필드 조립을 재현(계약 검증).

import (
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// TestMaskedDumpWarnsAllowlistEntries — v1.0.2(R2-C4): --mask 출력에
// allowlist 등록 수 경고가 병기된다(공유 덤프의 평문 잔류 고지 계약).
func TestMaskedDumpWarnsAllowlistEntries(t *testing.T) {
	store.ResetSecretsAllowlistForTest([]string{"demo-token-123"})
	defer store.ResetSecretsAllowlistForTest(nil)
	out := maskExtraJSON(t) // --mask 경로의 extra 조립 결과
	if !strings.Contains(out, `"allowlist_entries":1`) {
		t.Fatalf("allowlist 경고 부재: %q", out)
	}
	if !strings.Contains(out, "마스킹되지 않음") {
		t.Fatalf("경고 문구 부재: %q", out)
	}
	store.ResetSecretsAllowlistForTest(nil)
	if out2 := maskExtraJSON(t); strings.Contains(out2, "allowlist_entries") {
		t.Fatalf("미등록 시 경고 없어야 함: %q", out2)
	}
}

func maskExtraJSON(t *testing.T) string {
	t.Helper()
	return buildMaskExtra(store.SecretsAllowlistCount())
}
