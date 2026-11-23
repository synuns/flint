package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

type distillFakeStore struct {
	*fakeStore
	stampedIDs []int
	stampErr   error
}

func (f *distillFakeStore) MarkDistilled(ctx context.Context, ids []int) (int64, error) {
	f.stampedIDs = append(f.stampedIDs, ids...)
	if f.stampErr != nil {
		return 0, f.stampErr
	}
	return int64(len(ids)), nil
}

var distillClock = time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC)

const distillFileName = "insights-20261123-0941.md"

func installDistillFakes(t *testing.T, rows []knowledge.DistillRow) (*bytes.Buffer, *float64) {
	t.Helper()
	if rows == nil {
		rows = []knowledge.DistillRow{
			{ID: 2, Topic: "demo-b", Summary: "beta body", Tags: []string{"demo"}, RecallScore: 1},
			{ID: 1, Topic: "demo-a", Summary: "alpha body", Conditions: "when demo holds", Tags: []string{"demo", "a"}, RecallScore: 3},
		}
	}
	var minSeen float64
	var stderr bytes.Buffer
	oldRows, oldNow, oldErr := distillRowsFn, distillNowFn, distillStderr
	distillRowsFn = func(ctx context.Context, st wire.Store, min float64) ([]knowledge.DistillRow, error) {
		minSeen = min
		return rows, nil
	}
	distillNowFn = func() time.Time { return distillClock }
	distillStderr = &stderr
	t.Cleanup(func() {
		distillRowsFn, distillNowFn, distillStderr = oldRows, oldNow, oldErr
	})
	return &stderr, &minSeen
}

func runDistillOnce(st Store, args []string) (string, int) {
	var out bytes.Buffer
	code := runDistill(context.Background(), st, args, strings.NewReader(""), &out)
	return out.String(), code
}

func TestRunDistillRequiresOut(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--out"},
		{"--out", ""},
		{"--min-score", "2"},
	} {
		called := false
		old := distillRowsFn
		distillRowsFn = func(ctx context.Context, st wire.Store, min float64) ([]knowledge.DistillRow, error) {
			called = true
			return nil, nil
		}
		out, code := runDistillOnce(&distillFakeStore{fakeStore: &fakeStore{}}, args)
		distillRowsFn = old
		if code != 3 {
			t.Errorf("args %v: exit = %d, want 3 (out=%s)", args, code, out)
		}
		if !strings.Contains(out, `"field":"out"`) {
			t.Errorf("args %v: schema error missing out field: %s", args, out)
		}
		if called {
			t.Errorf("args %v: source query ran before the --out check", args)
		}
	}
}

func TestRunDistillInvalidMinScore(t *testing.T) {
	installDistillFakes(t, nil)
	dir := t.TempDir()
	for _, v := range []string{"abc", "-1", "1e309", "1000000001"} {
		out, code := runDistillOnce(&distillFakeStore{fakeStore: &fakeStore{}},
			[]string{"--out", dir, "--min-score", v})
		if code != 3 {
			t.Errorf("--min-score %s: exit = %d, want 3 (out=%s)", v, code, out)
		}
		if !strings.Contains(out, `"field":"min-score"`) {
			t.Errorf("--min-score %s: schema error missing min-score field: %s", v, out)
		}
	}

	if out, code := runDistillOnce(&distillFakeStore{fakeStore: &fakeStore{}},
		[]string{"--out", dir, "--min-score", "1000000000"}); code != 0 {
		t.Fatalf("--min-score 1e9: exit = %d, want 0 (out=%s)", code, out)
	}
}

func TestRunDistillWritesAndStamps(t *testing.T) {
	stderr, _ := installDistillFakes(t, nil)
	dir := t.TempDir()
	fst := &distillFakeStore{fakeStore: &fakeStore{}}
	out, code := runDistillOnce(fst, []string{"--out", dir})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out=%s)", code, out)
	}
	if out != "" {
		t.Errorf("stdout must stay empty with --out DIR, got %q", out)
	}
	path := filepath.Join(dir, distillFileName)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("aggregate file not written: %v", err)
	}
	s := string(body)

	if strings.Index(s, "## #1 demo-a") > strings.Index(s, "## #2 demo-b") {
		t.Errorf("sections not in id order:\n%s", s)
	}
	for _, want := range []string{"recall_score: 3", "conditions: when demo holds", "tags: demo|a", "rows: 2"} {
		if !strings.Contains(s, want) {
			t.Errorf("file missing %q:\n%s", want, s)
		}
	}
	if len(fst.stampedIDs) != 2 || fst.stampedIDs[0] != 1 || fst.stampedIDs[1] != 2 {
		t.Errorf("stamped ids = %v, want [1 2]", fst.stampedIDs)
	}
	if len(fst.commits) != 1 || !strings.Contains(fst.commits[0], "distill") {
		t.Errorf("commit recording mismatch: %v", fst.commits)
	}
	if !strings.Contains(stderr.String(), "distilled 2 insights") {
		t.Errorf("stderr summary missing: %q", stderr.String())
	}
}

func TestRunDistillZeroRowsNoFile(t *testing.T) {
	stderr, _ := installDistillFakes(t, []knowledge.DistillRow{})
	dir := t.TempDir()
	fst := &distillFakeStore{fakeStore: &fakeStore{}}
	out, code := runDistillOnce(fst, []string{"--out", dir})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out=%s)", code, out)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("0-row run wrote files: %v", entries)
	}
	if len(fst.stampedIDs) != 0 {
		t.Errorf("0-row run stamped ids: %v", fst.stampedIDs)
	}
	if !strings.Contains(stderr.String(), "distilled 0 insights") {
		t.Errorf("stderr summary missing: %q", stderr.String())
	}
}

func TestRunDistillSameMinuteNoOverwrite(t *testing.T) {
	stderr, _ := installDistillFakes(t, nil)
	dir := t.TempDir()
	path := filepath.Join(dir, distillFileName)
	if err := os.WriteFile(path, []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runDistillOnce(&distillFakeStore{fakeStore: &fakeStore{}}, []string{"--out", dir})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out=%s)", code, out)
	}
	second := filepath.Join(dir, "insights-20261123-0941-2.md")
	if body, err := os.ReadFile(path); err != nil || string(body) != "OLD" {
		t.Fatalf("earlier file must survive byte-identical: %v %q", err, string(body))
	}
	if body, err := os.ReadFile(second); err != nil || len(body) == 0 {
		t.Fatalf("collision file not written: %v", err)
	}
	if !strings.Contains(stderr.String(), "no overwrite") ||
		!strings.Contains(stderr.String(), distillFileName) ||
		!strings.Contains(stderr.String(), "insights-20261123-0941-2.md") {
		t.Fatalf("collision notice must name both files: %q", stderr.String())
	}

	out, code = runDistillOnce(&distillFakeStore{fakeStore: &fakeStore{}}, []string{"--out", dir})
	if code != 0 {
		t.Fatalf("third run exit = %d, want 0 (out=%s)", code, out)
	}
	third := filepath.Join(dir, "insights-20261123-0941-3.md")
	if _, err := os.Stat(third); err != nil {
		t.Fatalf("third collision file missing: %v", err)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "OLD" {
		t.Fatalf("earlier file must still survive: %v %q", err, string(body))
	}
}

func TestRunDistillWriteFailureNoStamp(t *testing.T) {
	installDistillFakes(t, nil)
	dir := t.TempDir()
	fst := &distillFakeStore{fakeStore: &fakeStore{}}
	oldWrite := distillWriteFileFn
	distillWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		return errors.New("demo write failure")
	}
	t.Cleanup(func() { distillWriteFileFn = oldWrite })
	out, code := runDistillOnce(fst, []string{"--out", dir})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out=%s)", code, out)
	}
	if len(fst.stampedIDs) != 0 {
		t.Errorf("failed write must not stamp, got %v", fst.stampedIDs)
	}
	if len(fst.commits) != 0 {
		t.Errorf("failed write must not commit, got %v", fst.commits)
	}
}

func TestRunDistillMinScoreResolution(t *testing.T) {
	dir := t.TempDir()
	metaRows := func(v string) [][]any {
		return [][]any{{"distill_min_score", v}}
	}
	cases := []struct {
		name    string
		meta    string
		flag    string
		wantMin float64
	}{
		{"default", "", "", store.DefaultThresholds().DistillMinScore},
		{"meta-override", "3", "", 3},
		{"flag-beats-meta", "3", "5", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, minSeen := installDistillFakes(t, []knowledge.DistillRow{})
			fs := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
				if strings.Contains(sql, "FROM meta") {
					if tc.meta == "" {
						return nil, nil
					}
					return metaRows(tc.meta), nil
				}
				return nil, nil
			}}
			args := []string{"--out", dir}
			if tc.flag != "" {
				args = append(args, "--min-score", tc.flag)
			}
			if _, code := runDistillOnce(fs, args); code != 0 {
				t.Fatalf("exit != 0")
			}
			if *minSeen != tc.wantMin {
				t.Errorf("effective min = %v, want %v", *minSeen, tc.wantMin)
			}
		})
	}
}

func TestRunDistillNilStoreExit2(t *testing.T) {
	installDistillFakes(t, nil)
	out, code := runDistillOnce(nil, []string{"--out", t.TempDir()})
	if code != store.MapExit(store.ErrServerDown) {
		t.Fatalf("exit = %d, want 2 (out=%s)", code, out)
	}
	if !strings.Contains(out, "server_down") {
		t.Errorf("nil store must report server_down, got %s", out)
	}
}

func TestRunDistillServerDownExit2(t *testing.T) {
	installDistillFakes(t, nil)
	called := false
	old := distillRowsFn
	distillRowsFn = func(ctx context.Context, st wire.Store, min float64) ([]knowledge.DistillRow, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() { distillRowsFn = old })
	out, code := runDistillOnce(&fakeStore{pingErr: store.ErrServerDown}, []string{"--out", t.TempDir()})
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out=%s)", code, out)
	}
	if called {
		t.Error("source query ran on a down store")
	}
}

func TestRunDistillFileCapDefers(t *testing.T) {
	rows := []knowledge.DistillRow{
		{ID: 1, Topic: "demo-c1", Summary: strings.Repeat("c1 body ", 40), Tags: []string{"demo"}, RecallScore: 2},
		{ID: 2, Topic: "demo-c2", Summary: strings.Repeat("c2 body ", 40), Tags: []string{"demo"}, RecallScore: 2},
		{ID: 3, Topic: "demo-c3", Summary: strings.Repeat("c3 body ", 40), Tags: []string{"demo"}, RecallScore: 2},
	}
	stderr, _ := installDistillFakes(t, rows)
	dir := t.TempDir()
	fst := &distillFakeStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {

			return [][]any{{"distill_max_runes", "600"}}, nil
		}
		return nil, nil
	}}}
	out, code := runDistillOnce(fst, []string{"--out", dir})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out=%s)", code, out)
	}
	if len(fst.stampedIDs) != 1 || fst.stampedIDs[0] != 1 {
		t.Fatalf("stamped ids = %v, want [1] (rows 2-3 defer)", fst.stampedIDs)
	}
	body, err := os.ReadFile(filepath.Join(dir, distillFileName))
	if err != nil {
		t.Fatalf("aggregate file not written: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "## #1 demo-c1") || strings.Contains(s, "## #2") || strings.Contains(s, "## #3") {
		t.Fatalf("file sections mismatch (want only #1):\n%s", s)
	}
	if !strings.Contains(s, "rows: 1") {
		t.Fatalf("header rows line must name the emitted count:\n%s", s)
	}
	if !strings.Contains(stderr.String(), "2 rows deferred") {
		t.Fatalf("deferred notice missing: %q", stderr.String())
	}

	if n := strings.Count(stderr.String(), "rows deferred"); n != 1 {
		t.Fatalf("deferred notice lines = %d, want 1: %q", n, stderr.String())
	}
}

func TestDistillFileNameShape(t *testing.T) {
	if name := distillFileNameFor(distillClock); name != distillFileName {
		t.Errorf("file name = %q, want %q", name, distillFileName)
	}
	now := distillFileNameFor(time.Now().UTC())
	stamp := strings.TrimSuffix(strings.TrimPrefix(now, "insights-"), ".md")
	if !strings.HasPrefix(now, "insights-") || !strings.HasSuffix(now, ".md") {
		t.Errorf("file name shape mismatch: %q", now)
	}
	if len(stamp) != 13 || stamp[8] != '-' {
		t.Errorf("timestamp shape mismatch: %q (want YYYYMMDD-HHMM)", stamp)
	}
}

func TestRunDistillSurvivedSessionsInFile(t *testing.T) {
	st := &distillFakeStore{fakeStore: &fakeStore{}}
	rows := []knowledge.DistillRow{
		{ID: 4, Topic: "demo-surv", Summary: "survived body", Tags: []string{"demo"},
			RecallScore: 2, SurvivedSessions: 3},
		{ID: 5, Topic: "demo-fresh", Summary: "fresh body", Tags: []string{"demo"},
			RecallScore: 1, SurvivedSessions: 0},
	}
	_, _ = installDistillFakes(t, rows)
	dir := t.TempDir()
	out, code := runDistillOnce(st, []string{"--out", dir})
	if code != 0 {
		t.Fatalf("distill exit=%d out=%s", code, out)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("insights 파일=%v err=%v", files, err)
	}
	body, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil {
		t.Fatalf("파일 읽기: %v", err)
	}
	if !strings.Contains(string(body), "survived_sessions: 3") {
		t.Fatalf("survived_sessions 병기 부재(3):\n%s", body)
	}
	if !strings.Contains(string(body), "survived_sessions: 0") {
		t.Fatalf("survived_sessions 병기 부재(0 — 0도 행에 병기):\n%s", body)
	}
}
