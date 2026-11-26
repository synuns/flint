package doclint

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	_ "github.com/synuns/flint/lib/cli"
	"github.com/synuns/flint/lib/mcp"
	"github.com/synuns/flint/lib/wire"
)

var tableCmdRe = regexp.MustCompile("(?m)^\\| `flint ([a-z][a-z-]*)")

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

var skillToolRe = regexp.MustCompile("flint_[a-z_]+")

func TestSkillToolListMatchesMCP(t *testing.T) {
	want := map[string]bool{}
	for _, td := range mcp.ToolsManifest() {
		want[td.Name] = true
	}
	if len(want) != 6 {
		t.Fatalf("mcp 도구 %d종 — 6종 계약 위반(도구맵 확장 시 가드 갱신)", len(want))
	}
	got := map[string]bool{}
	for _, name := range skillToolRe.FindAllString(docStr(t, "../flint-plugin/skills/flint/SKILL.md"), -1) {
		got[name] = true
	}
	diffNames(t, "SKILL.md 도구", got, want)
}
