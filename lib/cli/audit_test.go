// audit_test.go — T52 감사 커맨드: dolt_log 최근 N 조회(limit 경계·session 필터).
package cli

import (
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// auditRows — dolt_log 2행 픽스처(session 태그 서로 다름).
func auditRows() [][]any {
	return [][]any{
		{"hash2", "committer-b", "2026-10-22 10:00:00", "knowledge: s [session:S2]"},
		{"hash1", "committer-a", "2026-10-22 09:00:00", "knowledge: t [session:S1]"},
	}
}

// auditFake — SQL LIMIT·LIKE를 흉내 내는 queryFn. 전달된 limit 인자를 기록.
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
		if strings.Contains(sql, "LIKE") && len(args) > 1 {
			pat, _ := args[1].(string)
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

// TestAuditLimitAndSessionFilter — Review Focus: limit 경계·session 필터.
func TestAuditLimitAndSessionFilter(t *testing.T) {
	f, _ := auditFake(t, auditRows())
	withFake(t, f)

	// 기본: 2행 → log 2행 exit 0.
	out, code := mustRun(t, []string{"audit"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if lg, ok := m["log"].([]any); !ok || len(lg) != 2 {
		t.Fatalf("log=%v, want 2행", m["log"])
	}
	first := m["log"].([]any)[0].(map[string]any)
	if first["commit_hash"] != "hash2" || first["committer"] != "committer-b" ||
		first["date"] != "2026-10-22 10:00:00" || first["message"] != "knowledge: s [session:S2]" {
		t.Fatalf("컬럼 4종 불일치: %v", first)
	}

	// --limit 1 → 1행.
	out, code = mustRun(t, []string{"audit", "--limit", "1"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if lg := decode(t, out)["log"].([]any); len(lg) != 1 {
		t.Fatalf("log=%d행, want 1", len(lg))
	}

	// --session S1 → 매칭 1행.
	out, code = mustRun(t, []string{"audit", "--session", "S1"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	lg := decode(t, out)["log"].([]any)
	if len(lg) != 1 || !strings.Contains(lg[0].(map[string]any)["message"].(string), "[session:S1]") {
		t.Fatalf("session 필터 결과=%v, want 1행(S1)", lg)
	}

	// 무효 session → 0행 exit 0.
	out, code = mustRun(t, []string{"audit", "--session", "nope"}, "")
	if code != 0 {
		t.Fatalf("무효 session exit=%d, want 0", code)
	}
	if got := strings.TrimSpace(out); got != `{"log":[]}` {
		t.Fatalf("무효 session 출력=%q, want empty log", got)
	}
}

// TestAuditLimitBoundary — 0·음수 → 기본 20, 상한 100 clamp.
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

// TestAuditServerDownExit2 — 쿼리 실패(서버 다운)는 조용히 exit 2.
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

// TestAuditBadFlagExit3 — 알 수 없는 플래그는 무효 인자 exit 3 한 줄 JSON
// (v1.0.1 F3 — exit 1에서 통일).
func TestAuditBadFlagExit3(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"audit", "--bogus"}, "")
	if code != 3 {
		t.Fatalf("exit=%d, want 3", code)
	}
	decode(t, out)
}

// TestAuditSanitizesStringColumns — v1.0.1 감사(F4): dolt_log·dolt_diff의
// 문자열 열(commit_hash·committer·table_name)이 render.Text 재살균을
// 통과한다 — 미살균이면 [flint] 마커·제어문자가 원문 그대로 노출된다.
func TestAuditSanitizesStringColumns(t *testing.T) {
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "dolt_diff") {
			return [][]any{{"ha\x01sh1", "table_[flint]_x", true, false}}, nil
		}
		return [][]any{
			{"ha\x01sh1", "committer-[flint]-b", "2026-10-22 09:00:00", "m"},
		}, nil
	}
	withFake(t, f)
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
