// distill_test.go — contract tests for the flint distill CLI wiring
// (v1.22.0 T6, ADR-0018). The source query and the stamp write are
// faked via seam vars / a store extension fake; the runDistill contract
// itself (required --out, min-score resolution, aggregate file,
// overwrite warning, write-then-stamp atomicity, exit 0/1/2/3) is the
// verification target. Synthetic demo rows only.
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

// distillFakeStore — fakeStore + the MarkDistilled capture layer (the
// distillStamper extension the production realStore carries).
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

// distillClock — the fixed run clock (deterministic aggregate filename).
var distillClock = time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC)

// distillFileName — the expected aggregate file name for the fixed clock.
const distillFileName = "insights-20261123-0941.md"

// installDistillFakes — swaps the rows/format/clock/stderr seams and
// captures the effective min score the source query received. rows=nil
// means the default 2-row fake (ids 2 then 1 — order-preservation
// input). Returns the stderr buffer and the min-score capture pointer.
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

// runDistillOnce — the runDistill helper (nil stdin, fresh stdout).
func runDistillOnce(st Store, args []string) (string, int) {
	var out bytes.Buffer
	code := runDistill(context.Background(), st, args, strings.NewReader(""), &out)
	return out.String(), code
}

// RF1 — --out is required: absent flag, missing trailing value, and an
// empty value are all usage errors (exit 3, one schema-error JSON line,
// no source query).
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

// RF2 — an unparsable or negative --min-score is a usage error (exit 3).
func TestRunDistillInvalidMinScore(t *testing.T) {
	installDistillFakes(t, nil)
	dir := t.TempDir()
	for _, v := range []string{"abc", "-1"} {
		out, code := runDistillOnce(&distillFakeStore{fakeStore: &fakeStore{}},
			[]string{"--out", dir, "--min-score", v})
		if code != 3 {
			t.Errorf("--min-score %s: exit = %d, want 3 (out=%s)", v, code, out)
		}
		if !strings.Contains(out, `"field":"min-score"`) {
			t.Errorf("--min-score %s: schema error missing min-score field: %s", v, out)
		}
	}
}

// RF3 — the happy path: one aggregate insights-<YYYYMMDD-HHMM>.md in the
// --out dir, id-sorted sections, summary line on stderr, the selected
// ids stamped exactly once, and a dolt commit records the stamp.
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
	// id-sorted file content: #1 section before #2, all fields present.
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

// RF4 — idempotence surface: 0 unstamped rows above the floor writes no
// file, stamps nothing, and exits 0.
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

// RF5 — overwriting an existing same-name file: one warning line to
// stderr before the write (the export --out convention).
func TestRunDistillOverwriteWarning(t *testing.T) {
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
	lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
	if len(lines) < 1 || !strings.Contains(lines[0], "overwriting existing file") || !strings.Contains(lines[0], path) {
		t.Fatalf("overwrite warning must be first stderr line, got %q", stderr.String())
	}
	if body, err := os.ReadFile(path); err != nil || string(body) == "OLD" {
		t.Errorf("existing file not overwritten: %v %q", err, string(body))
	}
}

// RF6 — atomicity: a file-write failure means no stamp and exit 1 (the
// stamp only follows a successful write — ADR-0018).
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

// RF7 — min-score resolution: the threshold meta override
// (distill_min_score) replaces the default; an explicit --min-score
// takes precedence over the override; no meta row falls back to the
// DefaultThresholds value.
func TestRunDistillMinScoreResolution(t *testing.T) {
	dir := t.TempDir()
	metaRows := func(v string) [][]any {
		return [][]any{{"distill_min_score", v}}
	}
	cases := []struct {
		name    string
		meta    string // "" = no meta rows
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

// RF8 — nil store exits 2 (the export convention — one server_down
// line, MapExit).
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

// RF9 — server-down (ping failure) exits 2 before any source query.
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

// RF10 — the aggregate file name shape: insights-<YYYYMMDD-HHMM>.md
// (verified on the fixed clock; the live clock only needs the shape).
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
