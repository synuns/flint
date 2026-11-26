package doclint

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const baselineCommit = "cefe28d"

const reproDeletionFmt = "rm %s && go test ./internal/doclint -run TestNoTestDeletion"
const reproSkipIncrease = "go test ./internal/doclint -run TestNoSkipIncrease"

type testBaselineRow struct {
	dir        string
	files      int
	skips      int
	runFilters int
}

var testBaseline = []testBaselineRow{
	{"data", 2, 1, 0},
	{"flint-plugin", 8, 0, 0},
	{"internal/commentlint", 1, 0, 0},
	{"internal/doclint", 29, 4, 7},
	{"internal/testengine", 4, 4, 0},
	{"lib/backup", 10, 2, 0},
	{"lib/cli", 90, 47, 11},
	{"lib/db", 59, 4, 0},
	{"lib/engine", 5, 0, 0},
	{"lib/i18n", 1, 0, 0},
	{"lib/knowledge", 56, 61, 5},
	{"lib/mcp", 10, 10, 0},
	{"lib/ops", 62, 14, 9},
	{"lib/query", 16, 1, 0},
	{"lib/render", 2, 0, 0},
	{"lib/sqlfs", 3, 0, 0},
	{"lib/store", 14, 0, 0},
	{"lib/tui", 9, 2, 0},
	{"lib/vault", 4, 5, 0},
	{"lib/wire", 19, 4, 0},
	{"tests/e2e", 10, 18, 1},
	{"tests/regression", 7, 2, 0},
}

type site struct {
	file string
	line int
}

type finding struct {
	rule    string
	file    string
	line    int
	what    string
	fix     string
	repro   string
	summary string
}

func (f finding) render() string {
	loc := f.file
	if f.line > 0 {
		loc = fmt.Sprintf("%s:%d", f.file, f.line)
	}
	return strings.Join([]string{
		fmt.Sprintf("[%s] %s", f.rule, f.what),
		fmt.Sprintf("파일: %s", loc),
		fmt.Sprintf("수정처: %s", f.fix),
		fmt.Sprintf("재현: %s", f.repro),
		f.summary,
	}, "\n")
}

type testTreeState struct {
	root                 string
	filesByDir           map[string][]string
	skipSites            []site
	runSites             []site
	deletedSinceBaseline map[string][]string
	gitEvidenceErr       string
}

var docSkipRe = regexp.MustCompile(`t\.Skip(f)?\(`)
var docRunRe = regexp.MustCompile(`(^|[^-])-run[= ]`)

func docRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func gitDeletedTestFiles(t *testing.T, root string) ([]string, string) {
	t.Helper()
	cmd := exec.Command("git", "diff", "--name-only", baselineCommit, "--diff-filter=D", "--", "*_test.go")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Sprintf("git diff --name-only %s --diff-filter=D: %v — %s", baselineCommit, err, detail)
	}
	var names []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			names = append(names, l)
		}
	}
	return names, ""
}

func collectTestTreeState(t *testing.T) *testTreeState {
	t.Helper()
	root := docRepoRoot(t)
	st := &testTreeState{
		root:                 root,
		filesByDir:           map[string][]string{},
		deletedSinceBaseline: map[string][]string{},
	}
	var all []string
	for _, row := range testBaseline {
		matches, err := filepath.Glob(filepath.Join(root, row.dir, "*_test.go"))
		if err != nil {
			t.Fatal(err)
		}
		rel := make([]string, 0, len(matches))
		for _, m := range matches {
			r, err := filepath.Rel(root, m)
			if err != nil {
				t.Fatal(err)
			}
			rel = append(rel, r)
		}
		sort.Strings(rel)
		st.filesByDir[row.dir] = rel
		all = append(all, rel...)
	}
	sort.Strings(all)
	for _, f := range all {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if docSkipRe.MatchString(line) {
				st.skipSites = append(st.skipSites, site{f, i + 1})
			}
			if docRunRe.MatchString(line) {
				st.runSites = append(st.runSites, site{f, i + 1})
			}
		}
	}
	names, gitErr := gitDeletedTestFiles(t, root)
	st.gitEvidenceErr = gitErr
	for _, n := range names {
		d := filepath.Dir(n)
		st.deletedSinceBaseline[d] = append(st.deletedSinceBaseline[d], n)
	}
	return st
}

func sitesInDir(sites []site, dir string) []site {
	var out []site
	for _, s := range sites {
		if filepath.Dir(s.file) == dir {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].line < out[j].line
	})
	return out
}

func checkTestDeletion(baseline []testBaselineRow, state *testTreeState) []finding {
	var out []finding
	for _, row := range baseline {
		cur := len(state.filesByDir[row.dir])
		if cur >= row.files {
			continue
		}
		names := append([]string(nil), state.deletedSinceBaseline[row.dir]...)
		sort.Strings(names)
		file := row.dir + "/"
		fix := fmt.Sprintf("git checkout %s -- %s/", baselineCommit, row.dir)
		what := fmt.Sprintf("테스트 파일 삭제 감지 — %s 등록표 %d건 중 현재 %d건", row.dir, row.files, cur)
		summary := fmt.Sprintf("등록표(testBaseline) 단일 소스 대비 소실 %d건 — 복구 또는 등록표 편집(사유 필수)", row.files-cur)
		if len(names) > 0 {
			file = names[0]
			fix = fmt.Sprintf("git checkout %s -- %s", baselineCommit, strings.Join(names, " "))
		} else if state.gitEvidenceErr != "" {
			what += " — git 삭제 증거 저하(파일명 미식별)"
			fix = fmt.Sprintf("원격 완전 동기화(git fetch --unshallow origin — baseline %s 확보) 후 git checkout %s -- %s/", baselineCommit, baselineCommit, row.dir)
			summary = fmt.Sprintf("git 삭제 증거 수집 실패(%s) — 소실 %d건의 파일명 미표기, 완전 체크아웃 후 재판정 필요", state.gitEvidenceErr, row.files-cur)
		}
		out = append(out, finding{
			rule:    ruleTestDeletion,
			file:    file,
			what:    what,
			fix:     fix,
			repro:   fmt.Sprintf(reproDeletionFmt, file),
			summary: summary,
		})
	}
	return out
}

func checkSiteIncrease(baseline []testBaselineRow, state *testTreeState, sites []site, rule string, label string, fix string, baselineOf func(testBaselineRow) int) []finding {
	var out []finding
	for _, row := range baseline {
		base := baselineOf(row)
		inDir := sitesInDir(sites, row.dir)
		if len(inDir) <= base {
			continue
		}
		extras := inDir[base:]
		var tokens []string
		for _, s := range extras {
			tokens = append(tokens, fmt.Sprintf("%s:%d", s.file, s.line))
		}
		out = append(out, finding{
			rule:    rule,
			file:    extras[0].file,
			line:    extras[0].line,
			what:    fmt.Sprintf("%s 수 증가 — %s 등록표 %d → 현재 %d", label, row.dir, base, len(inDir)),
			fix:     fix,
			repro:   reproSkipIncrease,
			summary: "증가분 후보: " + strings.Join(tokens, ", "),
		})
	}
	return out
}

func checkSkipIncrease(baseline []testBaselineRow, state *testTreeState) []finding {
	return checkSiteIncrease(baseline, state, state.skipSites, ruleSkipIncrease, "t.Skip",
		"신규 t.Skip 제거(테스트 복구) — 부득하하면 등록표(testBaseline) 편집+사유 기재",
		func(r testBaselineRow) int { return r.skips })
}

func checkRunFilterIncrease(baseline []testBaselineRow, state *testTreeState) []finding {
	return checkSiteIncrease(baseline, state, state.runSites, ruleRunFilterCommit, "-run 필터",
		"신규 -run 필터 제거(전수 실행 복구) — 부득하하면 등록표(testBaseline) 편집+사유 기재",
		func(r testBaselineRow) int { return r.runFilters })
}

func renderFindings(fs []finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.render())
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func failWithFindings(t *testing.T, fs []finding) {
	t.Helper()
	if len(fs) > 0 {
		t.Fatalf("\n%s", renderFindings(fs))
	}
}

func TestNoTestDeletionDegradedGitEvidence(t *testing.T) {
	_, gitErr := gitDeletedTestFiles(t, t.TempDir())
	if gitErr == "" {
		t.Fatal("git 저장소 밖 디렉터리의 증거 수집 실패가 표면화되지 않음 — git 오류 삼킴 재발")
	}
	state := &testTreeState{
		filesByDir:     map[string][]string{"pkg/x": {"pkg/x/a_test.go"}},
		gitEvidenceErr: gitErr,
	}
	fs := checkTestDeletion([]testBaselineRow{{dir: "pkg/x", files: 2}}, state)
	if len(fs) != 1 {
		t.Fatalf("증거 저하 상태에서도 소실 finding은 유지되어야 함 — %v", fs)
	}
	out := fs[0].render()
	if !strings.Contains(out, gitErr) {
		t.Errorf("finding 출력에 git 오류 사유 미포함:\n%s", out)
	}
	if !strings.Contains(out, "unshallow") {
		t.Errorf("finding 수정처에 완전 체크아웃 처방 미포함:\n%s", out)
	}
	assertFiveLineBlock(t, fs[0])

	healthy := &testTreeState{
		filesByDir: map[string][]string{"pkg/x": {"pkg/x/a_test.go", "pkg/x/b_test.go"}},
	}
	if fs := checkTestDeletion([]testBaselineRow{{dir: "pkg/x", files: 2}}, healthy); len(fs) != 0 {
		t.Errorf("정합 상태에서 git 증거 저하 finding 오탐 — %v", fs)
	}
}

func TestNoTestDeletion(t *testing.T) {
	failWithFindings(t, checkTestDeletion(testBaseline, collectTestTreeState(t)))
}

func TestNoSkipIncrease(t *testing.T) {
	state := collectTestTreeState(t)
	var fs []finding
	fs = append(fs, checkSkipIncrease(testBaseline, state)...)
	fs = append(fs, checkRunFilterIncrease(testBaseline, state)...)
	failWithFindings(t, fs)
}

func TestFindingOutputContract(t *testing.T) {
	fakeBaseline := []testBaselineRow{
		{dir: "pkg/x", files: 2, skips: 1, runFilters: 1},
	}

	delState := &testTreeState{
		filesByDir:           map[string][]string{"pkg/x": {"pkg/x/a_test.go"}},
		deletedSinceBaseline: map[string][]string{"pkg/x": {"pkg/x/b_test.go"}},
	}
	del := checkTestDeletion(fakeBaseline, delState)
	if len(del) != 1 {
		t.Fatalf("삭제 1건 시나리오에서 finding 수 = %d (기대 1)", len(del))
	}
	if del[0].rule != ruleTestDeletion {
		t.Errorf("삭제 finding 규칙 ID = %s (기대 %s)", del[0].rule, ruleTestDeletion)
	}
	if !strings.Contains(del[0].file, "b_test.go") {
		t.Errorf("삭제 finding 파일 미표기 — %s", del[0].file)
	}
	if !strings.Contains(del[0].fix, "git checkout") {
		t.Errorf("삭제 finding 수정처(git checkout) 미표기 — %s", del[0].fix)
	}
	assertFiveLineBlock(t, del[0])

	skipState := &testTreeState{filesByDir: map[string][]string{"pkg/x": {"a_test.go", "b_test.go"}}}
	skipState.skipSites = []site{
		{"pkg/x/a_test.go", 10},
		{"pkg/x/b_test.go", 20},
		{"pkg/x/b_test.go", 30},
	}
	skip := checkSkipIncrease(fakeBaseline, skipState)
	if len(skip) != 1 {
		t.Fatalf("Skip 증가 시나리오에서 finding 수 = %d (기대 1)", len(skip))
	}
	if skip[0].rule != ruleSkipIncrease || skip[0].line <= 0 {
		t.Errorf("Skip finding 규칙/행 부정합 — rule=%s line=%d", skip[0].rule, skip[0].line)
	}
	if !strings.Contains(skip[0].summary, "b_test.go:30") {
		t.Errorf("Skip finding 증가분 후보 미표기 — %s", skip[0].summary)
	}
	assertFiveLineBlock(t, skip[0])

	runState := &testTreeState{filesByDir: map[string][]string{"pkg/x": {"a_test.go", "b_test.go"}}}
	runState.runSites = []site{{"pkg/x/a_test.go", 5}, {"pkg/x/b_test.go", 7}}
	run := checkRunFilterIncrease(fakeBaseline, runState)
	if len(run) != 1 {
		t.Fatalf("-run 필터 증가 시나리오에서 finding 수 = %d (기대 1)", len(run))
	}
	if run[0].rule != ruleRunFilterCommit {
		t.Errorf("-run finding 규칙 ID = %s (기대 %s)", run[0].rule, ruleRunFilterCommit)
	}
	assertFiveLineBlock(t, run[0])

	cleanState := &testTreeState{
		filesByDir: map[string][]string{"pkg/x": {"a_test.go", "b_test.go"}},
	}
	cleanState.skipSites = []site{{"pkg/x/a_test.go", 10}}
	cleanState.runSites = []site{{"pkg/x/a_test.go", 5}}
	if fs := checkTestDeletion(fakeBaseline, cleanState); len(fs) != 0 {
		t.Errorf("정합 시나리오에서 삭제 오탐 — %v", fs)
	}
	if fs := checkSkipIncrease(fakeBaseline, cleanState); len(fs) != 0 {
		t.Errorf("정합 시나리오에서 Skip 오탐 — %v", fs)
	}
	if fs := checkRunFilterIncrease(fakeBaseline, cleanState); len(fs) != 0 {
		t.Errorf("정합 시나리오에서 -run 오탐 — %v", fs)
	}
}

func assertFiveLineBlock(t *testing.T, f finding) {
	t.Helper()
	out := f.render()
	if got := strings.Count(out, "\n") + 1; got != 5 {
		t.Errorf("구조화 실패 출력 5줄 계약 위반 — 실제 %d줄\n%s", got, out)
	}
	for _, want := range []string{"파일: ", "수정처: ", "재현: "} {
		if !strings.Contains(out, want) {
			t.Errorf("구조화 실패 출력 계약 미비 — %q 없음\n%s", want, out)
		}
	}
}
