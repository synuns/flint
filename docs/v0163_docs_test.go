// v0163_docs_test.go — v0.16.3 튜닝값 레지스트리화 문서 회귀.
package docs

import (
	"regexp"
	"strings"
	"testing"
)

func TestPlanRecordsV0163(t *testing.T) {
	if !strings.Contains(docStr(t, "PLAN.md"), "v0.16.3") {
		t.Error("PLAN.md v0.16.3 마일스톤 누락")
	}
}

// TestBadgeIsV0163 — v0.16.4 전환으로 삭제(선례·MatchesBadge가 동기 가드).

// TUNING.md §2 표에 신규 튜닝 키 7종이 모두 문서화됐는지(코드 키와 대조는
// TestTuningMetaKeysMatch 계약 승계 — 여기는 행 존재 단얱).
func TestTuningKeysV0163Documented(t *testing.T) {
	doc := docStr(t, "TUNING.md")
	for _, key := range []string{
		"suggest_min_score", "suggest_scan_cap", "conflict_dup_threshold",
		"backup_age_warn_h", "zero_near_min_read", "zero_near_warn_pct",
		"pending_age_days",
	} {
		if !strings.Contains(doc, key) {
			t.Errorf("TUNING.md에 %s 행 없음", key)
		}
	}
}

// SKILL.md 근거 문구가 개인 관측 수치 인용 없이 중성 서술로 기술되는지.
func TestSkillRationaleNeutral(t *testing.T) {
	b := docStr(t, "../flint-plugin/skills/flint/SKILL.md")
	if !strings.Contains(b, "중복 재제출") {
		t.Error("SKILL.md 쓰기 프로토콜 근거 문구(중립 서술) 없음")
	}
}

// plugin.json 버전이 README 배지와 동기되는지 — v0.16.3 범프 누락 회귀
// (역대 릴리스는 항상 범프됐으나 v0.16.3에서 누락됐었다).
func TestPluginVersionMatchesBadge(t *testing.T) {
	m := regexp.MustCompile(`release-v(\d+\.\d+\.\d+)-blue`).FindStringSubmatch(docStr(t, "../README.md"))
	if m == nil {
		t.Fatal("README 배지 버전 파싱 실패")
	}
	manifest := docStr(t, "../flint-plugin/.claude-plugin/plugin.json")
	v := regexp.MustCompile(`"version":\s*"(\d+\.\d+\.\d+)"`).FindStringSubmatch(manifest)
	if v == nil || v[1] != m[1] {
		t.Errorf("plugin.json 버전 %q != README 배지 %q", v, m[1])
	}
}
