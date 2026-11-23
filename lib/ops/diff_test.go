package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

func TestRunDiffUsageErrorType(t *testing.T) {
	for _, args := range [][]string{nil, {"aaaa1"}, {"aaaa1", "bbbb2", "extra"}} {
		var out bytes.Buffer
		code := RunDiff(context.Background(), nil, args, strings.NewReader(""), &out)
		if code != 3 {
			t.Fatalf("%v: exit=%d, want 3 (출력 %q)", args, code, out.String())
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
			t.Fatalf("%v: JSON 아님: %q", args, out.String())
		}
		if m["error"] != "usage" {
			t.Fatalf("%v: error=%v, want \"usage\"", args, m["error"])
		}
		msg, _ := m["message"].(string)
		if !strings.Contains(msg, "usage: flint diff <from> <to>") {
			t.Fatalf("%v: message=%q, want usage 안내 포함", args, msg)
		}
	}
}

type fakeDiffStore struct {
	*fakeStore
	diffRows    []wire.KnowledgeDiffRow
	diffRowsErr error
	commits     []wire.DiffCommitSummary
	commitsErr  error
	gotFromTo   [][2]string
}

func (f *fakeDiffStore) DiffRows(ctx context.Context, from, to string) ([]wire.KnowledgeDiffRow, error) {
	f.gotFromTo = append(f.gotFromTo, [2]string{from, to})
	return f.diffRows, f.diffRowsErr
}

func (f *fakeDiffStore) DiffCommits(ctx context.Context, from, to string) ([]wire.DiffCommitSummary, error) {
	return f.commits, f.commitsErr
}

func diffTestSetup(t *testing.T) {
	t.Helper()
	prev := ParseAsOfSpecFn
	ParseAsOfSpecFn = func(ctx context.Context, st wire.Store, spec string) (string, error) {
		return spec, nil
	}
	t.Cleanup(func() { ParseAsOfSpecFn = prev })
}

func decodeDiff(t *testing.T, args []string, f *fakeDiffStore) map[string]any {
	t.Helper()
	var out bytes.Buffer
	code := RunDiff(context.Background(), f, args, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out.String())
	}
	if n := strings.Count(strings.TrimSpace(out.String()), "\n"); n != 0 {
		t.Fatalf("출력이 한 줄이 아님: %q", out.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out.String())
	}
	return m
}

func TestRunDiffSummaryCommits(t *testing.T) {
	diffTestSetup(t)
	d := time.Date(2026, 11, 18, 0, 0, 1, 0, time.UTC)
	f := &fakeDiffStore{fakeStore: &fakeStore{}, commits: []wire.DiffCommitSummary{
		{Hash: "c3", Message: "audit: demo", Date: d, DataChange: true},
		{Hash: "c2", Message: "schema tweak", Date: d, SchemaChange: true},
		{Hash: "c1", Message: "init", Date: d},
	}}
	m := decodeDiff(t, []string{"aaaa1", "bbbb2", "--summary"}, f)
	if m["from"] != "aaaa1" || m["to"] != "bbbb2" {
		t.Fatalf("from/to=%v/%v, want aaaa1/bbbb2", m["from"], m["to"])
	}
	cs, ok := m["commits"].([]any)
	if !ok || len(cs) != 3 {
		t.Fatalf("commits=%v, want 3건 배열", m["commits"])
	}
	first, _ := cs[0].(map[string]any)
	if first["hash"] != "c3" || first["message"] != "audit: demo" {
		t.Fatalf("첫 커밋=%v, want c3/audit: demo", first)
	}
	if first["data_change"] != true || first["schema_change"] != false {
		t.Fatalf("플래그=%v/%v, want true/false", first["data_change"], first["schema_change"])
	}
	if _, ok := first["date"].(string); !ok {
		t.Fatalf("date=%v, want 문자열", first["date"])
	}

	if len(f.gotFromTo) != 1 || f.gotFromTo[0] != [2]string{"aaaa1", "bbbb2"} {
		t.Fatalf("DiffRows 인수=%v, want [aaaa1 bbbb2]", f.gotFromTo)
	}
}

func TestRunDiffSummaryEmptyRange(t *testing.T) {
	diffTestSetup(t)
	f := &fakeDiffStore{fakeStore: &fakeStore{}}
	m := decodeDiff(t, []string{"aaaa1", "aaaa1", "--summary"}, f)
	if cs, ok := m["commits"].([]any); !ok || len(cs) != 0 {
		t.Fatalf("commits=%v(%T), want 빈 배열", m["commits"], m["commits"])
	}
	if m["added"] != float64(0) || m["modified"] != float64(0) || m["removed"] != float64(0) {
		t.Fatalf("수치=%v/%v/%v, want 0/0/0", m["added"], m["modified"], m["removed"])
	}
}

func TestRunDiffDataSchemaFilter(t *testing.T) {
	diffTestSetup(t)
	d := time.Date(2026, 11, 18, 0, 0, 1, 0, time.UTC)
	newF := func() *fakeDiffStore {
		return &fakeDiffStore{fakeStore: &fakeStore{}, commits: []wire.DiffCommitSummary{
			{Hash: "data-only", Date: d, DataChange: true},
			{Hash: "schema-only", Date: d, SchemaChange: true},
			{Hash: "none"},
		}}
	}
	for _, tc := range []struct {
		flag string
		want []string
	}{
		{"--data", []string{"data-only"}},
		{"--schema", []string{"schema-only"}},
	} {
		m := decodeDiff(t, []string{"aaaa1", "bbbb2", "--summary", tc.flag}, newF())
		cs, _ := m["commits"].([]any)
		if len(cs) != len(tc.want) {
			t.Fatalf("%s: commits %d건, want %d건(%v)", tc.flag, len(cs), len(tc.want), tc.want)
		}
		for i, want := range tc.want {
			c, _ := cs[i].(map[string]any)
			if c["hash"] != want {
				t.Fatalf("%s: commits[%d].hash=%v, want %v", tc.flag, i, c["hash"], want)
			}
		}
	}
}

func TestRunDiffRowDetail(t *testing.T) {
	diffTestSetup(t)
	d := time.Date(2026, 11, 18, 0, 0, 1, 0, time.UTC)
	f := &fakeDiffStore{
		fakeStore: &fakeStore{},
		diffRows: []wire.KnowledgeDiffRow{
			{DiffType: "added", ID: "k-7", Topic: "demo topic"},
			{DiffType: "modified", ID: "k-2", Topic: "demo revised"},
			{DiffType: "removed", ID: "k-1", Topic: "demo stale"},
		},
		commits: []wire.DiffCommitSummary{{Hash: "c1", Message: "audit: demo", Date: d, DataChange: true}},
	}
	m := decodeDiff(t, []string{"aaaa1", "bbbb2"}, f)
	if _, ok := m["rows"]; !ok {
		t.Fatalf("rows 키 없음(기본 모드): %v", m)
	}
	rows, _ := m["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("rows %d건, want 3건", len(rows))
	}
	first, _ := rows[0].(map[string]any)
	if first["diff_type"] != "added" || first["id"] != "k-7" || first["topic"] != "demo topic" {
		t.Fatalf("rows[0]=%v, want added/k-7/demo topic", first)
	}
	if m["added"] != float64(1) || m["modified"] != float64(1) || m["removed"] != float64(1) {
		t.Fatalf("수치=%v/%v/%v, want 1/1/1", m["added"], m["modified"], m["removed"])
	}
	if cs, _ := m["commits"].([]any); len(cs) != 1 {
		t.Fatalf("commits %d건, want 1건(기본 모드 동반)", len(cs))
	}
}
