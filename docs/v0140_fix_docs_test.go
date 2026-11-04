// v0140_fix_docs_test.go — v0.14.0 리뷰 수정 회귀(docs): P5 §8 스테일 지침
// 제거·P6 exit 1 구분 문구 EN/KO 대칭.
package docs

import (
	"strings"
	"testing"
)

// TestTuningSuggestAdjustmentObservable — §8 suggestMinScore 조정 지침은
// 폐지된 제안 출력 재확인 대신 zero_near_pct(`flint report`) 관측을 안내한다.
func TestTuningSuggestAdjustmentObservable(t *testing.T) {
	s := docStrV0140(t, "TUNING.md")
	section := s
	if i := strings.Index(s, "## 8."); i >= 0 {
		section = s[i:]
	}
	if end := strings.Index(section, "## 9."); end >= 0 {
		section = section[:end]
	}
	if strings.Contains(section, "top 제안을 재확인") {
		t.Error("§8 제안 출력 재확인 지침 잔존 — 제안 출력은 폐지돼 실행 불가")
	}
	if !strings.Contains(section, "zero_near_pct") {
		t.Error("§8 zero_near_pct 관측 안내 누락(§9 정합)")
	}
}

// TestExit1DisambiguationDocs — exit 1 구분(무출력=침묵 정상 · 오류 JSON 한
// 줄 동반=실패)이 README EN/KO·TUNING에 문서화됐다.
func TestExit1DisambiguationDocs(t *testing.T) {
	if !strings.Contains(docStrV0140(t, "../README.md"), "exit 1 with no output") {
		t.Error("README.md exit 1 구분 문구 누락")
	}
	if !strings.Contains(docStrV0140(t, "../README.ko.md"), "출력 없는 exit 1") {
		t.Error("README.ko.md exit 1 구분 문구 누락")
	}
	if !strings.Contains(docStrV0140(t, "TUNING.md"), "무출력") {
		t.Error("TUNING.md exit 1 구분 문구 누락")
	}
}
