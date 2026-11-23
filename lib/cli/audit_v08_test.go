package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

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
		{-24*time.Hour - time.Second, "1d ago"},
		{-25*time.Hour - time.Second, "1d ago"},
		{-2 * 24 * time.Hour, "2d ago"},
		{time.Hour, "0m ago"},
	}
	for _, c := range cases {
		if got := relTime(now.Add(c.offset)); got != c.want {
			t.Fatalf("relTime(now%v)=%q, want %q", c.offset, got, c.want)
		}
	}
}

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

func statLogRows() [][]any {
	return [][]any{
		{"hash2", "committer-b", time.Now().Add(-3 * time.Hour), "knowledge: s [session:S2]"},
		{"hash1", "committer-a", time.Now().Add(-2 * 24 * time.Hour), "knowledge: t"},
	}
}

func TestAuditStatField(t *testing.T) {
	f := statFake(t, statLogRows(), map[string][][]any{
		"hash2": {{"public.knowledge", true, false}, {"public.meta", false, true}},
		"hash1": nil,
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

	second := log[1].(map[string]any)
	if st, ok := second["stat"].(map[string]any); ok && len(st) != 0 {
		t.Fatalf("hash1 stat=%v, want 빈·미포함", st)
	}
	if second["date_rel"] != "2d ago" {
		t.Fatalf("date_rel=%v, want '2d ago'", second["date_rel"])
	}
}

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

	if f.diffCalls != 1 || len(f.diffArgs) != 1 {
		t.Fatalf("DiffStat 호출=%d회, want 1(배치)", f.diffCalls)
	}
	for _, h := range f.diffArgs[0] {
		if h != "hash1" && h != "hash2" {
			t.Fatalf("DiffStat 해시 인수 불일치: %v", f.diffArgs[0])
		}
	}
}

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
