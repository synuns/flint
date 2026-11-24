package doclint

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	ruleTestDeletion    = "R-C-101"
	ruleSkipIncrease    = "R-C-102"
	ruleRunFilterCommit = "R-C-103"
	rulePairRegistry    = "R-C-104"
	ruleFindingOutput   = "R-C-105"
	ruleCommentBan      = "R-C-201"
	rulePersonalData    = "R-C-202"
	ruleMetricsRegistry = "R-C-203"
	ruleCommandsTable   = "R-C-204"
	ruleDocsSyncExit    = "R-C-205"
)

var canonicalRuleIDs = []string{
	ruleTestDeletion,
	ruleSkipIncrease,
	ruleRunFilterCommit,
	rulePairRegistry,
	ruleFindingOutput,
	ruleCommentBan,
	rulePersonalData,
	ruleMetricsRegistry,
	ruleCommandsTable,
	ruleDocsSyncExit,
}

type pairRow struct {
	rule   string
	device string
}

var ruleEnforcementPairs = []pairRow{
	{ruleTestDeletion, "internal/doclint:TestNoTestDeletion"},
	{ruleSkipIncrease, "internal/doclint:TestNoSkipIncrease"},
	{ruleRunFilterCommit, "internal/doclint:TestNoSkipIncrease"},
	{rulePairRegistry, "internal/doclint:TestRuleEnforcementPairs"},
	{ruleFindingOutput, "internal/doclint:TestFindingOutputContract"},
	{ruleCommentBan, "scripts/check_comments.py"},
	{rulePersonalData, "scripts/check_personal.py"},
	{ruleMetricsRegistry, "internal/doclint:TestMetricsRegistrySync"},
	{ruleCommandsTable, "internal/doclint:TestCommandTableMatchesRegistry"},
	{ruleDocsSyncExit, "internal/doclint:TestMakefileDocsSyncPropagatesGoTestExit"},
}

var ruleIDRe = regexp.MustCompile(`^R-C-\d{3}$`)

var testFuncRe = regexp.MustCompile(`^func (Test[A-Za-z0-9_]+)\(`)

func collectTestFuncNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if m := testFuncRe.FindStringSubmatch(sc.Text()); m != nil {
				names[m[1]] = true
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestRuleEnforcementPairs(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range ruleEnforcementPairs {
		if seen[p.rule] {
			t.Errorf("쌍 표 규칙 ID 중복 — %s", p.rule)
		}
		seen[p.rule] = true
		if !ruleIDRe.MatchString(p.rule) {
			t.Errorf("규칙 ID 체계(불변 형식 R-C-XXX) 위반 — %s", p.rule)
		}
	}
	for _, id := range canonicalRuleIDs {
		if !seen[id] {
			t.Errorf("쌍 표에 불변 규칙 ID 누락 — %s (삭제·재번호 금지)", id)
		}
	}
	if len(ruleEnforcementPairs) != len(canonicalRuleIDs) {
		t.Errorf("쌍 표 드리프트 — 표 %d행 vs 불변 규칙 %d건 — 등록표 편집 없는 과잉 등록 금지",
			len(ruleEnforcementPairs), len(canonicalRuleIDs))
	}

	root := docRepoRoot(t)
	tests := collectTestFuncNames(t, root)
	for _, p := range ruleEnforcementPairs {
		if i := strings.LastIndex(p.device, ":"); i >= 0 {
			if !tests[p.device[i+1:]] {
				t.Errorf("쌍 표 집행 장치 부재 — 규칙 %s의 %s 테스트 함수가 repo에 없음", p.rule, p.device)
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(root, p.device)); err != nil {
			t.Errorf("쌍 표 집행 장치 부재 — 규칙 %s의 %s 파일이 repo에 없음", p.rule, p.device)
		}
	}
}
