// audit_test.go — the audit command: the most recent N commit log (limit boundaries, session filter).
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// auditRows — 2 dolt_log fixture rows (different session tags).
func auditRows() [][]any {
	return [][]any{
		{"hash2", "committer-b", "2026-10-22 10:00:00", "knowledge: s [session:S2]"},
		{"hash1", "committer-a", "2026-10-22 09:00:00", "knowledge: t [session:S1]"},
	}
}

// auditFake — a queryFn mimicking SQL LIMIT and LIKE. Records the passed limit argument.
func auditFake(t *testing.T, rows [][]any) (*fakeStore, *[]int) {
	t.Helper()
	limits := &[]int{}
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		limit, _ := args[0].(int)
		if limit <= 0 {
			limit = len(rows)
		}
		*limits = append(*limits, limit)
		out := rows
		if len(out) > limit {
			out = out[:limit]
		}
		// v1.7.2 T2: the default query excludes NOT LIKE 'telemetry: %' + --session LIKE.
		if strings.Contains(sql, "message NOT LIKE ") && len(args) > 1 {
			pat, _ := args[1].(string)
			pat = strings.Trim(pat, "%")
			var filtered [][]any
			for _, r := range out {
				if !strings.Contains(r[3].(string), pat) {
					filtered = append(filtered, r)
				}
			}
			out = filtered
		}
		if strings.Contains(sql, "message LIKE ") && len(args) > 1 {
			pat, _ := args[len(args)-1].(string)
			pat = strings.Trim(pat, "%")
			var filtered [][]any
			for _, r := range out {
				if strings.Contains(r[3].(string), pat) {
					filtered = append(filtered, r)
				}
			}
			out = filtered
		}
		return out, nil
	}
	return f, limits
}

// TestAuditLimitAndSessionFilter — limit boundaries and the session filter.
func TestAuditLimitAndSessionFilter(t *testing.T) {
	f, _ := auditFake(t, auditRows())
	withFake(t, f)

	// default: 2 rows -> a 2-row log, exit 0.
	out, code := mustRun(t, []string{"audit"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if lg, ok := m["log"].([]any); !ok || len(lg) != 2 {
		t.Fatalf("log=%v, want 2행", m["log"])
	}
	first := m["log"].([]any)[0].(map[string]any)
	// date follows the LogEntry.Date (time.Time) format (the implementation's fmt.Sprint — timezone included).
	if first["commit_hash"] != "hash2" || first["committer"] != "committer-b" ||
		first["date"] != "2026-10-22 10:00:00 +0000 UTC" || first["message"] != "knowledge: s [session:S2]" {
		t.Fatalf("컬럼 4종 불일치: %v", first)
	}

	// --limit 1 -> 1 row.
	out, code = mustRun(t, []string{"audit", "--limit", "1"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if lg := decode(t, out)["log"].([]any); len(lg) != 1 {
		t.Fatalf("log=%d행, want 1", len(lg))
	}

	// --session S1 -> 1 matching row.
	out, code = mustRun(t, []string{"audit", "--session", "S1"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	lg := decode(t, out)["log"].([]any)
	if len(lg) != 1 || !strings.Contains(lg[0].(map[string]any)["message"].(string), "[session:S1]") {
		t.Fatalf("session 필터 결과=%v, want 1행(S1)", lg)
	}

	// invalid session -> 0 rows, exit 0.
	out, code = mustRun(t, []string{"audit", "--session", "nope"}, "")
	if code != 0 {
		t.Fatalf("무효 session exit=%d, want 0", code)
	}
	if got := strings.TrimSpace(out); got != `{"log":[]}` {
		t.Fatalf("무효 session 출력=%q, want empty log", got)
	}
}

// TestAuditLimitBoundary — 0 or negative -> default 20; capped at 100.
func TestAuditLimitBoundary(t *testing.T) {
	cases := []struct {
		arg  string
		want int
	}{
		{"0", 20}, {"-5", 20}, {"999", 100}, {"150", 100}, {"1", 1}, {"42", 42},
	}
	for _, c := range cases {
		f, limits := auditFake(t, auditRows())
		withFake(t, f)
		if _, code := mustRun(t, []string{"audit", "--limit", c.arg}, ""); code != 0 {
			t.Fatalf("--limit %s exit≠0", c.arg)
		}
		if len(*limits) == 0 || (*limits)[0] != c.want {
			t.Fatalf("--limit %s → 전달 limit=%v, want %d", c.arg, *limits, c.want)
		}
	}
}

// TestAuditServerDownExit2 — a query failure (server down) is silent exit 2.
func TestAuditServerDownExit2(t *testing.T) {
	f := &fakeStore{queryErr: store.ErrServerDown}
	withFake(t, f)
	out, code := mustRun(t, []string{"audit"}, "")
	if code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
	if strings.TrimSpace(out) != `{"error":"server_down"}` {
		t.Fatalf("출력=%q, want server_down 한 줄", out)
	}
}

// TestAuditBadFlagExit3 — an unknown flag is invalid-arg exit 3 one-line JSON.
func TestAuditBadFlagExit3(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"audit", "--bogus"}, "")
	if code != 3 {
		t.Fatalf("exit=%d, want 3", code)
	}
	decode(t, out)
}

// TestAuditSanitizesStringColumns — the string columns of the commit log (LogEntries) and the
// per-table change summary (DiffStat) (commit_hash, committer, table) pass through the render.Text
// re-sanitization — unsanitized, the [flint] marker and control characters would leak verbatim.
func TestAuditSanitizesStringColumns(t *testing.T) {
	f := &auditSanitizeFake{fakeStore: &fakeStore{}}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		return [][]any{
			{"ha\x01sh1", "committer-[flint]-b", "2026-10-22 09:00:00", "m"},
		}, nil
	}
	withFakeStore(t, f)
	out, code := mustRun(t, []string{"audit", "--stat"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	e := decode(t, out)["log"].([]any)[0].(map[string]any)
	if e["commit_hash"] != "hash1" {
		t.Errorf("commit_hash 미살균: %q", e["commit_hash"])
	}
	if e["committer"] != "committer- -b" {
		t.Errorf("committer 미살균: %q", e["committer"])
	}
	stat, ok := e["stat"].(map[string]any)
	if !ok {
		t.Fatalf("stat 부록 부재: %v", e["stat"])
	}
	if _, ok := stat["table_ _x"]; !ok {
		t.Errorf("table_name 미살균: %v", stat)
	}
}

// auditSanitizeFake — for the sanitization regression: the log via queryFn (raw polluted rows); the
// table summary via DiffStat canned (polluted raw values — sanitization is the implementation's render.Text).
type auditSanitizeFake struct {
	*fakeStore
}

func (f *auditSanitizeFake) DiffStat(ctx context.Context, hashes []string) ([]wire.TableDiffStat, error) {
	return []wire.TableDiffStat{
		{CommitHash: "ha\x01sh1", Table: "table_[flint]_x", DataChange: true},
	}, nil
}
