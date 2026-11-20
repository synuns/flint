// usage_type_v154_test.go — regression: invalid args (unknown flag, missing value,
// invalid value, invalid check name) for the 7 commands (maintain, doctor,
// report, audit, backup, import, update) produce error:"usage" + allowed-flag guidance in the message
// (extending the read/approve convention). Exit codes unchanged (invalid arg 3).
// Genuine internal faults (network failure etc.) stay internal — not the target here.
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

// usageTypeProbes — the table driving invalid-arg paths: (command, args, expected exit,
// usage-guidance prefix that must appear in the message).
type usageTypeProbe struct {
	cmd      string
	args     []string
	want     int
	usageSub string
}

var usageTypeProbes = []usageTypeProbe{
	// maintain — unknown flag, missing value, bare usage
	{"maintain", []string{"--check", "--bogus"}, 3, "usage: flint maintain"},
	{"maintain", []string{"--limit"}, 3, "usage: flint maintain"},
	{"maintain", []string{}, 3, "usage: flint maintain"},
	// doctor — unknown flag, missing --check value, invalid check name
	{"doctor", []string{"--bogus"}, 3, "usage: flint doctor"},
	{"doctor", []string{"--check"}, 3, "usage: flint doctor"},
	{"doctor", []string{"--check", "nope"}, 3, "usage: flint doctor"},
	// report — unknown flag, missing --days value, invalid number
	{"report", []string{"--bogus"}, 3, "usage: flint report"},
	{"report", []string{"--days"}, 3, "usage: flint report"},
	{"report", []string{"--days", "abc"}, 3, "usage: flint report"},
	// audit — unknown flag, missing --limit value, invalid number
	{"audit", []string{"--bogus"}, 3, "usage: flint audit"},
	{"audit", []string{"--limit"}, 3, "usage: flint audit"},
	{"audit", []string{"--limit", "abc"}, 3, "usage: flint audit"},
	// backup — unknown flag, missing --out value, --encrypt alone
	{"backup", []string{"--wat"}, 3, "usage: flint backup"},
	{"backup", []string{"--out"}, 3, "usage: flint backup"},
	{"backup", []string{"--encrypt"}, 3, "usage: flint backup"},
	// restore (owned by backup.go — same contract) — unknown flag, missing --file value
	{"restore", []string{"--wat"}, 3, "usage: flint restore"},
	{"restore", []string{"--file"}, 3, "usage: flint restore"},
	// import — unknown flag on the rules-conversion path and the migration path
	{"import", []string{"--verbose"}, 3, "usage: flint import"},
	{"import", []string{"--format", "jsonl", "--wat"}, 3, "usage: flint import"},
	// update — unknown flag (validated before network access)
	{"update", []string{"--bogus"}, 3, "usage: flint update"},
	// the remaining 5 sites' unknown flags share the same contract:
	// pending (default path + --tui combination), export, profile, setup/teardown,
	// and the report --tui x view-flag combination.
	{"pending", []string{"--bogus"}, 3, "usage: flint pending"},
	{"pending", []string{"--tui", "--json"}, 3, "usage: flint pending"},
	{"export", []string{"--bogus"}, 3, "usage: flint export"},
	{"profile", []string{"--bogus"}, 3, "usage: flint profile"},
	{"setup", []string{"--bogus"}, 3, "usage: flint setup"},
	{"teardown", []string{"--bogus"}, 3, "usage: flint teardown"},
	{"report", []string{"--tui", "--html"}, 3, "usage: flint report"},
	// hot, brief, compact, nudge: unknown `-`-prefixed args exit 3
	// (abolishing the old silent ignore + exit 0 — no silent success).
	{"hot", []string{"--bogus"}, 3, "usage: flint hot"},
	{"brief", []string{"--bogus"}, 3, "usage: flint brief"},
	{"compact", []string{"--bogus"}, 3, "usage: flint compact"},
	{"nudge", []string{"--bogus"}, 3, "usage: flint nudge"},
	// write --help/-h: usage guidance with exit 0 before waiting on stdin
	// (not an error, so the error:"usage" assertion applies only to want!=0 rows).
	{"write", []string{"--help"}, 0, "usage: flint write"},
	{"write", []string{"-h"}, 0, "usage: flint write"},
}

// TestUsageTypeProbes — each row: exit 3 unchanged + error:"usage" + allowed-flag
// guidance included. Red against the pre-fix expectation (current error:"internal").
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
		// guidance rows like write --help (exit 0) are excluded since they are not error JSON.
		if p.want != 0 && !strings.Contains(b.String(), `"error":"usage"`) {
			t.Errorf("%s %v: error:usage 아님 — %q", p.cmd, p.args, b.String())
		}
		if !strings.Contains(b.String(), p.usageSub) {
			t.Errorf("%s %v: 허용 플래그 안내(%q) 미포함 — %q", p.cmd, p.args, p.usageSub, b.String())
		}
	}
}
