// tuning_test.go — TUNING.md의 메타 키 이름이 코드에서 실제 사용하는
// 키와 일치하는지 grep 정합 검증한다(T30 — 문서 임계값 절차의 계약).
// thresholds 4키는 lib/store/thresholds.go, last_backup은 lib/backup/auto.go,
// hot_budget은 lib/cli/hot.go, hot_stats는 lib/cli/metrics.go와 각각 대조한다.
package docs

import (
	"os"
	"strings"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("읽기 실패 %s: %v", path, err)
	}
	return string(b)
}

// TestTuningMetaKeysMatch — TUNING.md에 언급된 임계값 키가 thresholds.go
// case 문(큰따옴표 키)과 일치하고, last_backup은 auto.go, hot_budget은
// hot.go, hot_stats는 metrics.go의 실제 키와 각각 일치하는지 검증.
func TestTuningMetaKeysMatch(t *testing.T) {
	doc := read(t, "TUNING.md")

	for _, key := range []string{"pending_max", "dup_susp_max", "conf_floor", "stale_sessions"} {
		if !strings.Contains(read(t, "../lib/store/thresholds.go"), `"`+key+`"`) {
			t.Errorf("코드에 임계값 키 %s 없음 (thresholds.go)", key)
		}
		if !strings.Contains(doc, key) {
			t.Errorf("TUNING.md에 %s 절차 없음", key)
		}
	}

	if strings.Contains(doc, "last_backup") {
		if !strings.Contains(read(t, "../lib/backup/auto.go"), `"last_backup"`) {
			t.Error("last_backup 키 auto.go와 불일치")
		}
	}

	if !strings.Contains(read(t, "../lib/cli/hot.go"), `"hot_budget"`) {
		t.Error("hot_budget 키 hot.go와 불일치")
	}
	if !strings.Contains(doc, "hot_budget") {
		t.Error("TUNING.md에 hot_budget 절차 없음")
	}
}

// TestTuningHotStatsKeyMatch — meta hot_stats 키가 metrics.go 실구현과
// 일치하고 TUNING.md에도 언급되는지 검증.
func TestTuningHotStatsKeyMatch(t *testing.T) {
	if !strings.Contains(read(t, "../lib/cli/metrics.go"), `"hot_stats"`) {
		t.Error("hot_stats 키 metrics.go와 불일치")
	}
	if !strings.Contains(read(t, "TUNING.md"), "hot_stats") {
		t.Error("TUNING.md에 hot_stats 언급 없음")
	}
}
