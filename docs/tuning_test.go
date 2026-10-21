// tuning_test.go — TUNING.md의 메타 키 이름이 코드에서 실제 사용하는
// 키와 일치하는지 grep 정합 검증한다(T30 — 문서 임계값 절차의 계약).
// thresholds 4키는 lib/store/thresholds.go, last_backup은 lib/backup/auto.go,
// hot_budget은 v0.3 예약 키(코드 미반영)로 문서 표기까지만 검증한다.
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
// case 문(큰따옴표 키)과 일치하고, last_backup은 auto.go 키와 일치하며,
// hot_budget에는 '예약' 표기가 붙어 있는지 검증.
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

	if strings.Contains(doc, "hot_budget") && !strings.Contains(doc, "예약") {
		t.Error("hot_budget은 코드 미반영 — '예약' 표기 필요")
	}
}
