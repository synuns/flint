// audit_v08_test.go — T86 audit 확장: --stat(dolt_diff 테이블별 요약)·relTime
// 상대 시각 포맷·기존 출력 키 회귀.
package cli

import (
	"strings"
	"testing"
	"time"
)

// TestRelTimeFormat — Review Focus: relTime h·d 경계(분 단위 포함).
// 경계값은 -1s 여유를 두어 테스트 수행 시간(µs)과 무관하게 확정.
func TestRelTimeFormat(t *testing.T) {
	now := time.Now()
	cases := []struct {
		offset time.Duration
		want   string
	}{
		{-30*time.Minute - time.Second, "30m ago"},
		{-59*time.Minute - time.Second, "59m ago"},
		{-3 * time.Hour, "3h ago"},
		{-23*time.Hour - time.Second, "23h ago"},
		{-24*time.Hour - time.Second, "1d ago"}, // h→d 경계(24h 초과 시 d)
		{-25*time.Hour - time.Second, "1d ago"},
		{-2 * 24 * time.Hour, "2d ago"},
		{time.Hour, "0m ago"}, // 미래 시각 → 클램프
	}
	for _, c := range cases {
		if got := relTime(now.Add(c.offset)); got != c.want {
			t.Fatalf("relTime(now%v)=%q, want %q", c.offset, got, c.want)
		}
	}
}

// statFake — dolt_log 질의(기존 auditFake 흉내)와 dolt_diff 질의를 구분 응답.
// diffMap: commit_hash → {table_name, data_change, schema_change} 행목록.
// dolt_diff는 ANY($1) 배치 질의 — 해시 목록 전체에 대해 commit_hash를
// 선행 컬럼으로 붙여 응답한다.
func statFake(t *testing.T, logRows [][]any, diffMap map[string][][]any, seen *[]string) *fakeStore {
	t.Helper()
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if seen != nil {
			*seen = append(*seen, sql)
		}
		if strings.Contains(sql, "dolt_diff") {
			list, _ := args[0].([]string)
			var out [][]any
			for _, h := range list {
				for _, r := range diffMap[h] {
					out = append(out, append([]any{h}, r...))
				}
			}
			return out, nil
		}
		out := logRows
		if limit, ok := args[0].(int); ok && limit > 0 && len(out) > limit {
			out = out[:limit]
		}
		if strings.Contains(sql, "LIKE") && len(args) > 1 {
			pat := strings.Trim(args[1].(string), "%")
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
	return f
}

// statLogRows — dolt_log 2행 픽스처(date는 now-3h time.Time → date_rel 확정).
func statLogRows() [][]any {
	return [][]any{
		{"hash2", "committer-b", time.Now().Add(-3 * time.Hour), "knowledge: s [session:S2]"},
		{"hash1", "committer-a", time.Now().Add(-2 * 24 * time.Hour), "knowledge: t"},
	}
}

// TestAuditStatField — Review Focus: --stat 시 stat(dolt_diff 요약)·date_rel 부록.
func TestAuditStatField(t *testing.T) {
	f := statFake(t, statLogRows(), map[string][][]any{
		"hash2": {{"public.knowledge", true, false}, {"public.meta", false, true}},
		"hash1": nil, // 변경 요약 없음 → 빈 stat
	}, nil)
	withFake(t, f)

	out, code := mustRun(t, []string{"audit", "--stat"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	log := m["log"].([]any)
	if len(log) != 2 {
		t.Fatalf("log=%d행, want 2", len(log))
	}
	// hash2: 테이블별 data_change·schema_change 요약 + 상대 시각.
	first := log[0].(map[string]any)
	stat, ok := first["stat"].(map[string]any)
	if !ok {
		t.Fatalf("stat 필드 없음: %v", first)
	}
	kn, ok := stat["public.knowledge"].(map[string]any)
	if !ok || kn["data_change"] != true || kn["schema_change"] != false {
		t.Fatalf("public.knowledge 요약=%v, want data t/schema f", kn)
	}
	mt, ok := stat["public.meta"].(map[string]any)
	if !ok || mt["data_change"] != false || mt["schema_change"] != true {
		t.Fatalf("public.meta 요약=%v, want data f/schema t", mt)
	}
	if first["date_rel"] != "3h ago" {
		t.Fatalf("date_rel=%v, want '3h ago'", first["date_rel"])
	}
	// hash1: dolt_diff 0행 → stat 미포함(omitempty)·2d ago.
	second := log[1].(map[string]any)
	if st, ok := second["stat"].(map[string]any); ok && len(st) != 0 {
		t.Fatalf("hash1 stat=%v, want 빈·미포함", st)
	}
	if second["date_rel"] != "2d ago" {
		t.Fatalf("date_rel=%v, want '2d ago'", second["date_rel"])
	}
}

// TestAuditStatRegression — 기본 출력은 기존 키 4종만(회귀·출력 키 불변).
func TestAuditStatRegression(t *testing.T) {
	f := statFake(t, statLogRows(), map[string][][]any{
		"hash2": {{"public.knowledge", true, false}},
		"hash1": nil,
	}, nil)
	withFake(t, f)

	out, code := mustRun(t, []string{"audit"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	for _, e := range decode(t, out)["log"].([]any) {
		m := e.(map[string]any)
		for k := range m {
			if k != "commit_hash" && k != "committer" && k != "date" && k != "message" {
				t.Fatalf("기본 출력에 신규 키 %q 혼입: %v", k, m)
			}
		}
	}
}

// TestAuditStatQueryShape — dolt_diff 질의는 해시 파라미터화 + 로그 질의
// commit_order 정렬 유지.
func TestAuditStatQueryShape(t *testing.T) {
	var seen []string
	f := statFake(t, statLogRows(), map[string][][]any{
		"hash2": {{"public.knowledge", true, false}},
		"hash1": nil,
	}, &seen)
	withFake(t, f)

	if _, code := mustRun(t, []string{"audit", "--stat"}, ""); code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	var logSQL, diffSQL string
	for _, q := range seen {
		if strings.Contains(q, "dolt_diff") {
			diffSQL = q
		} else {
			logSQL = q
		}
	}
	if !strings.Contains(logSQL, "ORDER BY commit_order DESC") {
		t.Fatalf("로그 질의 정렬 유실: %q", logSQL)
	}
	if !strings.Contains(diffSQL, "$1") || strings.Contains(diffSQL, "hash2") {
		t.Fatalf("dolt_diff 질의 파라미터화 위반: %q", diffSQL)
	}
}

// TestAuditStatSingleBatchQuery — N+1 회귀: 커밋 수와 무관하게 dolt_diff
// 질의는 1회(해시 목록 ANY($1) 배치 — 상한 100 커밋에서 왕복 1회).
func TestAuditStatSingleBatchQuery(t *testing.T) {
	var seen []string
	f := statFake(t, statLogRows(), map[string][][]any{
		"hash2": {{"public.knowledge", true, false}},
		"hash1": {{"public.meta", false, true}},
	}, &seen)
	withFake(t, f)
	if _, code := mustRun(t, []string{"audit", "--stat"}, ""); code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	diffQueries := 0
	for _, q := range seen {
		if strings.Contains(q, "dolt_diff") {
			diffQueries++
		}
	}
	if diffQueries != 1 {
		t.Fatalf("dolt_diff 질의=%d회, want 1(배치 — N+1 회귀): %v", diffQueries, seen)
	}
}
