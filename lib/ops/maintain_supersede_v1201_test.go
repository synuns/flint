// maintain_supersede_v1201_test.go — v1.20.1 (r87) supersede precheck
// regressions: the SupersedeCycleSQL second column (depth-cap
// truncation) makes a beyond-cap lineage a conservative refusal (exit
// 1) — the old contract answered a capped walk with the "safe
// no-cycle" verdict and let the write proceed.
package ops

import (
	"strings"
	"testing"
	"time"
)

// TestMaintainSupersedeDepthCapRefusedV1201 — the walk hitting the
// depth cap refuses the supersede (exit 1) with the depth-cap
// diagnostic; no Supersede delegation·no Commit.
func TestMaintainSupersedeDepthCapRefusedV1201(t *testing.T) {
	shortRetry(t)
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT (SELECT status FROM knowledge") {
			return [][]any{{"active", time.Now().Add(-time.Hour)}}, nil
		}
		if strings.Contains(sql, "WITH RECURSIVE") {
			return [][]any{{false, true}}, nil // no cycle within the cap, but the walk truncated
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--supersede", "3", "--by", "7")
	if code != 1 {
		t.Fatalf("exit=%d want 1 (conservative depth-cap refusal), out=%s", code, out)
	}
	if !strings.Contains(out, "depth cap") {
		t.Fatalf("diagnostic=%q want the depth-cap message", out)
	}
	if len(f.superseded) != 0 {
		t.Fatalf("Supersede ran on a truncated verdict: %v", f.superseded)
	}
}
