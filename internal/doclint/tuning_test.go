package doclint

import (
	"os"
	"strings"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(docPath(path))
	if err != nil {
		t.Fatalf("읽기 실패 %s: %v", path, err)
	}
	return string(b)
}

func TestTuningMetaKeysMatch(t *testing.T) {
	doc := read(t, "TUNING.md")

	for _, key := range []string{"pending_max", "dup_susp_max", "conf_floor", "stale_sessions", "pending_promote_days", "pending_promote_min_runes",

		"diag_hot_budget_high", "diag_hot_budget_low", "diag_trend_pct", "diag_recall_led_goal", "diag_dead_warn", "knowledge_reads_retention_days", "zero_owned_floor"} {
		src := read(t, "../lib/store/thresholds.go")

		if !strings.Contains(src, `"`+key+`"`) {
			src2 := read(t, "../lib/knowledge/read.go")
			if !strings.Contains(src2, `"`+key+`"`) {
				t.Errorf("코드에 임계값 키 %s 없음 (thresholds.go·read.go)", key)
			}
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

	if !strings.Contains(read(t, "../lib/knowledge/hot.go"), `"hot_budget"`) {
		t.Error("hot_budget 키 hot.go와 불일치")
	}
	if !strings.Contains(doc, "hot_budget") {
		t.Error("TUNING.md에 hot_budget 절차 없음")
	}
}

func TestTuningHotStatsKeyMatch(t *testing.T) {
	if !strings.Contains(read(t, "../lib/ops/metrics.go"), `"hot_stats"`) {
		t.Error("hot_stats 키 metrics.go(ops)와 불일치")
	}
	if !strings.Contains(read(t, "TUNING.md"), "hot_stats") {
		t.Error("TUNING.md에 hot_stats 언급 없음")
	}
}
