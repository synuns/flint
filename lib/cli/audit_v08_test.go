// audit_v08_test.go — audit 확장: --stat(테이블별 변경 요약 — DiffStat)·
// relTime 상대 시각 포맷·기존 출력 키 회귀.
package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// TestRelTimeFormat — relTime h·d 경계(분 단위 포함).
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

// statFakeStore — audit --stat용 계약 canned: 로그 질의는 queryFn 승계
// (LogEntries 번역 seam — limit·NOT LIKE/LIKE 흉내), 테이블별 변경 요약은
// DiffStat 오버라이드. diffMap: commit_hash → {table, data_change,
// schema_change} 목록. 해시 목록은 슬라이스 인수로 전달된다(배치 —
// 파라미터화는 계약 시그니처 자체가 증명).
type statFakeStore struct {
	*fakeStore
	diffMap   map[string][][]any
	diffCalls int
	diffArgs  [][]string
}

func (f *statFakeStore) DiffStat(ctx context.Context, hashes []string) ([]wire.TableDiffStat, error) {
	f.diffCalls++
	f.diffArgs = append(f.diffArgs, hashes)
	var out []wire.TableDiffStat
	for _, h := range hashes {
		for _, r := range f.diffMap[h] {
			out = append(out, wire.TableDiffStat{
				CommitHash:   h,
				Table:        r[0].(string),
				DataChange:   r[1].(bool),
				SchemaChange: r[2].(bool),
			})
		}
	}
	return out, nil
}

// statFake — 로그 queryFn(LIMIT·NOT LIKE/LIKE 흉내) + DiffStat canned.
// seen은 로그 질의 캡처(DiffStat은 Query를 거치지 않는다).
func statFake(t *testing.T, logRows [][]any, diffMap map[string][][]any, seen *[]string) *statFakeStore {
	t.Helper()
	f := &statFakeStore{fakeStore: &fakeStore{}, diffMap: diffMap}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if seen != nil {
			*seen = append(*seen, sql)
		}
		out := logRows
		if limit, ok := args[0].(int); ok && limit > 0 && len(out) > limit {
			out = out[:limit]
		}
		// v1.7.2 T2: 기본 쿼리 NOT LIKE 'telemetry: %' 제외 + --session LIKE.
		if strings.Contains(sql, "message NOT LIKE ") && len(args) > 1 {
			pat := strings.Trim(args[1].(string), "%")
			var filtered [][]any
			for _, r := range out {
				if !strings.Contains(r[3].(string), pat) {
					filtered = append(filtered, r)
				}
			}
			out = filtered
		}
		if strings.Contains(sql, "message LIKE ") && len(args) > 1 {
			pat := strings.Trim(args[len(args)-1].(string), "%")
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

// TestAuditStatField — --stat 시 stat(DiffStat 요약)·date_rel 부록.
func TestAuditStatField(t *testing.T) {
	f := statFake(t, statLogRows(), map[string][][]any{
		"hash2": {{"public.knowledge", true, false}, {"public.meta", false, true}},
		"hash1": nil, // 변경 요약 없음 → 빈 stat
	}, nil)
	withFakeStore(t, f)

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
	withFakeStore(t, f)

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

// TestAuditStatQueryShape — DiffStat은 해시 목록을 슬라이스 인수로 받는다
// (파라미터화 — 계약 시그니처 증명) + 로그 질의 commit_order 정렬 유지.
func TestAuditStatQueryShape(t *testing.T) {
	var seen []string
	f := statFake(t, statLogRows(), map[string][][]any{
		"hash2": {{"public.knowledge", true, false}},
		"hash1": nil,
	}, &seen)
	withFakeStore(t, f)

	if _, code := mustRun(t, []string{"audit", "--stat"}, ""); code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if len(seen) == 0 || !strings.Contains(seen[len(seen)-1], "ORDER BY commit_order DESC") {
		t.Fatalf("로그 질의 정렬 유실: %q", seen)
	}
	// 해시 목록은 인수 슬라이스로 전달 — 커밋 수와 무관하게 배치 1회.
	if f.diffCalls != 1 || len(f.diffArgs) != 1 {
		t.Fatalf("DiffStat 호출=%d회, want 1(배치)", f.diffCalls)
	}
	for _, h := range f.diffArgs[0] {
		if h != "hash1" && h != "hash2" {
			t.Fatalf("DiffStat 해시 인수 불일치: %v", f.diffArgs[0])
		}
	}
}

// TestAuditStatSingleBatchQuery — N+1 회귀: 커밋 수와 무관하게 DiffStat
// 호출은 1회(해시 목록 배치 — 상한 100 커밋에서 왕복 1회).
func TestAuditStatSingleBatchQuery(t *testing.T) {
	f := statFake(t, statLogRows(), map[string][][]any{
		"hash2": {{"public.knowledge", true, false}},
		"hash1": {{"public.meta", false, true}},
	}, nil)
	withFakeStore(t, f)
	if _, code := mustRun(t, []string{"audit", "--stat"}, ""); code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if f.diffCalls != 1 {
		t.Fatalf("DiffStat 호출=%d회, want 1(배치 — N+1 회귀)", f.diffCalls)
	}
}
