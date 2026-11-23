package ops

import (
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

func TestMaskedDumpWarnsAllowlistEntries(t *testing.T) {
	store.ResetSecretsAllowlistForTest([]string{"demo-token-123"})
	defer store.ResetSecretsAllowlistForTest(nil)
	out := maskExtraJSON(t)
	if !strings.Contains(out, `"allowlist_entries":1`) {
		t.Fatalf("allowlist 경고 부재: %q", out)
	}
	if !strings.Contains(out, "are not masked") {
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
