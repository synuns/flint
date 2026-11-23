package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

type usageTypeProbe struct {
	cmd      string
	args     []string
	want     int
	usageSub string
}

var usageTypeProbes = []usageTypeProbe{

	{"maintain", []string{"--check", "--bogus"}, 3, "usage: flint maintain"},
	{"maintain", []string{"--limit"}, 3, "usage: flint maintain"},
	{"maintain", []string{}, 3, "usage: flint maintain"},

	{"doctor", []string{"--bogus"}, 3, "usage: flint doctor"},
	{"doctor", []string{"--check"}, 3, "usage: flint doctor"},
	{"doctor", []string{"--check", "nope"}, 3, "usage: flint doctor"},

	{"report", []string{"--bogus"}, 3, "usage: flint report"},
	{"report", []string{"--days"}, 3, "usage: flint report"},
	{"report", []string{"--days", "abc"}, 3, "usage: flint report"},

	{"audit", []string{"--bogus"}, 3, "usage: flint audit"},
	{"audit", []string{"--limit"}, 3, "usage: flint audit"},
	{"audit", []string{"--limit", "abc"}, 3, "usage: flint audit"},

	{"backup", []string{"--wat"}, 3, "usage: flint backup"},
	{"backup", []string{"--out"}, 3, "usage: flint backup"},
	{"backup", []string{"--encrypt"}, 3, "usage: flint backup"},

	{"restore", []string{"--wat"}, 3, "usage: flint restore"},
	{"restore", []string{"--file"}, 3, "usage: flint restore"},

	{"import", []string{"--verbose"}, 3, "usage: flint import"},
	{"import", []string{"--format", "jsonl", "--wat"}, 3, "usage: flint import"},

	{"update", []string{"--bogus"}, 3, "usage: flint update"},

	{"pending", []string{"--bogus"}, 3, "usage: flint pending"},
	{"pending", []string{"--tui", "--json"}, 3, "usage: flint pending"},
	{"export", []string{"--bogus"}, 3, "usage: flint export"},
	{"profile", []string{"--bogus"}, 3, "usage: flint profile"},
	{"setup", []string{"--bogus"}, 3, "usage: flint setup"},
	{"teardown", []string{"--bogus"}, 3, "usage: flint teardown"},
	{"report", []string{"--tui", "--html"}, 3, "usage: flint report"},

	{"hot", []string{"--bogus"}, 3, "usage: flint hot"},
	{"brief", []string{"--bogus"}, 3, "usage: flint brief"},
	{"compact", []string{"--bogus"}, 3, "usage: flint compact"},
	{"nudge", []string{"--bogus"}, 3, "usage: flint nudge"},

	{"write", []string{"--help"}, 0, "usage: flint write"},
	{"write", []string{"-h"}, 0, "usage: flint write"},
}

func TestUsageTypeProbes(t *testing.T) {
	ctx := context.Background()
	for _, p := range usageTypeProbes {
		h, ok := wire.Lookup(p.cmd)
		if !ok {
			t.Fatalf("%s: registry 미등록 커맨드", p.cmd)
		}
		var st wire.Store = &fakeStore{}
		if p.cmd == "maintain" {
			st = &fakeMaint{fakeStore: &fakeStore{}}
		}
		var b strings.Builder
		code := h(ctx, st, p.args, strings.NewReader(""), &b)
		if code != p.want {
			t.Errorf("%s %v: exit=%d, want %d — 출력 %q", p.cmd, p.args, code, p.want, b.String())
		}

		if p.want != 0 && !strings.Contains(b.String(), `"error":"usage"`) {
			t.Errorf("%s %v: error:usage 아님 — %q", p.cmd, p.args, b.String())
		}
		if !strings.Contains(b.String(), p.usageSub) {
			t.Errorf("%s %v: 허용 플래그 안내(%q) 미포함 — %q", p.cmd, p.args, p.usageSub, b.String())
		}
	}
}
