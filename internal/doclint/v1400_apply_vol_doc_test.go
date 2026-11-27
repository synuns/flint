package doclint

import (
	"strings"
	"testing"
)

func TestDocsSyncContracts(t *testing.T) {
	cli := docStr(t, "../docs/contracts/cli.md")
	if !strings.Contains(cli, "--age-audit --apply-vol") {
		t.Errorf("cli.md: maintain --age-audit --apply-vol contract bullet missing")
	}
	if !strings.Contains(cli, "dry-run") || !strings.Contains(cli, "--yes") {
		t.Errorf("cli.md: apply-vol approval gate (dry-run, --yes 승인) missing")
	}
	claimRows := []struct{ claim, vol string }{
		{"decision", "vol:immutable"},
		{"procedure", "vol:conditional"},
		{"lesson", "vol:conditional"},
		{"event", "vol:immutable"},
	}
	for _, r := range claimRows {
		if !strings.Contains(cli, "| `"+r.claim+"` | `"+r.vol+"` |") {
			t.Errorf("cli.md: claim→vol mapping row %s→%s missing", r.claim, r.vol)
		}
	}
	sourceRows := []struct{ src, vol string }{
		{"human", "vol:immutable"},
		{"correction", "vol:immutable"},
		{"import", "vol:conditional"},
		{"vault", "vol:conditional"},
	}
	for _, r := range sourceRows {
		if !strings.Contains(cli, "| `"+r.src+"` | `"+r.vol+"` |") {
			t.Errorf("cli.md: source default row %s→%s missing", r.src, r.vol)
		}
	}
	if !strings.Contains(cli, "never automatic") {
		t.Errorf("cli.md: proposal-only (no automatic decay exemption) wording missing")
	}
	skill := docStr(t, "../flint-plugin/skills/flint/SKILL.md")
	if !strings.Contains(skill, "--apply-vol") {
		t.Errorf("SKILL.md: --apply-vol line missing")
	}
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStr(t, p), "--apply-vol") {
			t.Errorf("%s: maintain table row --apply-vol mention missing", p)
		}
	}
}
