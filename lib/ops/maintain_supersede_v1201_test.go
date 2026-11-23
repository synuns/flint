package ops

import (
	"strings"
	"testing"
	"time"
)

func TestMaintainSupersedeDepthCapRefusedV1201(t *testing.T) {
	shortRetry(t)
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT (SELECT status FROM knowledge") {
			return [][]any{{"active", time.Now().Add(-time.Hour)}}, nil
		}
		if strings.Contains(sql, "WITH RECURSIVE") {
			return [][]any{{false, true}}, nil
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
