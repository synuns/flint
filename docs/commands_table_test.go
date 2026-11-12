// commands_table_test.go — v0.44.0 T3 커맨드 표 동기 가드. README EN/KO
// 커맨드 표와 SKILL 도구 목록이 코드 원천(wire.Registered · mcp
// ToolsManifest)과 드리프트 없는지 검증한다. 신규 커맨드·도구는 Register와
// 함께 README 양어 표에 행을 추가해야 통과한다(코드가 원천 — 문서가 아님).
package docs

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	_ "github.com/synuns/flint/lib/cli" // init Register 부작용(실등록 표면)
	"github.com/synuns/flint/lib/mcp"
	"github.com/synuns/flint/lib/wire"
)

// tableCmdRe — 커맨드 표 행 첫 열 패턴 `| `flint <name>`. 행 시작 앵커로
// 본문 인라인 언급(`flint hot` 산문 등)은 제외한다.
var tableCmdRe = regexp.MustCompile("(?m)^\\| `flint ([a-z][a-z-]*)")

// readmeTableCommands — README 커맨드 표에서 뽑은 커맨드명 집합.
func readmeTableCommands(t *testing.T, path string) map[string]bool {
	t.Helper()
	rows := tableCmdRe.FindAllStringSubmatch(docStr(t, path), -1)
	if len(rows) == 0 {
		t.Fatalf("%s 커맨드 표 행 없음 — 표 파서·문서 구조 확인", path)
	}
	set := map[string]bool{}
	for _, m := range rows {
		if set[m[1]] {
			t.Errorf("%s 커맨드 표에 %q 중복 행", path, m[1])
		}
		set[m[1]] = true
	}
	return set
}

// diffNames — 양방향 집합 차를 정렬된 메시지로 보고(불일치 전량 노출).
func diffNames(t *testing.T, label string, got, want map[string]bool) {
	t.Helper()
	var missing, extra []string
	for name := range want {
		if !got[name] {
			missing = append(missing, name)
		}
	}
	for name := range got {
		if !want[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("%s 표에 없는 등록 커맨드: %s", label, strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Errorf("%s 표의 미등록(또는 훅 전용) 커맨드: %s", label, strings.Join(extra, ", "))
	}
}

// TestCommandTableMatchesRegistry — Review Focus: README EN/KO 커맨드 표 ==
// wire.Registered 집합(HiddenFromUsage 제외 — 훅 전용 진입점은 사람 대상
// 표에 없다). 신규 커맨드를 Register만 하면 FAIL — 영구 드리프트 가드.
func TestCommandTableMatchesRegistry(t *testing.T) {
	want := map[string]bool{}
	for _, name := range wire.Registered() {
		if wire.HiddenFromUsage(name) {
			continue
		}
		want[name] = true
	}
	if len(want) < 10 {
		t.Fatalf("등록 커맨드 %d종 — cli init 부작용 임포트 깨짐", len(want))
	}
	diffNames(t, "README.md", readmeTableCommands(t, "../README.md"), want)
	diffNames(t, "README.ko.md", readmeTableCommands(t, "../README.ko.md"), want)
}

// skillToolRe — SKILL.md에 등장하는 flint_* 도구명 패턴.
var skillToolRe = regexp.MustCompile("flint_[a-z_]+")

// TestSkillToolListMatchesMCP — SKILL MCP 세션 도구 나열 == mcp
// ToolsManifest 5종. 도구 추가·개명 시 SKILL.md 갱신 없으면 FAIL.
func TestSkillToolListMatchesMCP(t *testing.T) {
	want := map[string]bool{}
	for _, td := range mcp.ToolsManifest() {
		want[td.Name] = true
	}
	if len(want) != 5 {
		t.Fatalf("mcp 도구 %d종 — 5종 계약 위반(도구맵 확장 시 가드 갱신)", len(want))
	}
	got := map[string]bool{}
	for _, name := range skillToolRe.FindAllString(docStr(t, "../flint-plugin/skills/flint/SKILL.md"), -1) {
		got[name] = true
	}
	diffNames(t, "SKILL.md 도구", got, want)
}
