package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/ops"
)

func TestRecheckConditionsAndDistillCapRealSlow(t *testing.T) {
	_, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	st, err := newRealStore(ctx, testengine.Scratch(t))
	if err != nil {
		t.Fatalf("newRealStore: %v", err)
	}
	t.Cleanup(st.Close)
	rs := st.(*realStore)

	seed := `INSERT INTO knowledge (topic, summary, tags, confidence, status, content_hash)
	         VALUES ($1, $2, ':demo:', 0.9, 'active', $3)`
	if _, err := rs.pool.Exec(ctx, seed, "r96-realslow-demo", "synthetic recheck body", "hash-r96-real"); err != nil {
		t.Fatalf("seed knowledge: %v", err)
	}
	idRow, err := st.Query(ctx, `SELECT id, content_hash FROM knowledge WHERE topic = 'r96-realslow-demo'`)
	if err != nil || len(idRow) != 1 {
		t.Fatalf("seed lookup: %v rows=%d", err, len(idRow))
	}
	origHash := fmt.Sprint(idRow[0][1])

	var out strings.Builder
	if code := ops.RunMaintain(ctx, st, []string{"--recheck", fmt.Sprint(idRow[0][0]), "--conditions", "when demo holds (r96 real)", "--yes"}, nil, &out); code != 0 {
		t.Fatalf("backfill exit=%d out=%s", code, out.String())
	}
	got, err := st.Query(ctx, `SELECT conditions, content_hash FROM knowledge WHERE topic = 'r96-realslow-demo'`)
	if err != nil || len(got) != 1 {
		t.Fatalf("post-update lookup: %v", err)
	}
	if c, _ := got[0][0].(string); c != "when demo holds (r96 real)" {
		t.Fatalf("conditions round-trip = %q", c)
	}
	if h := fmt.Sprint(got[0][1]); h != origHash {
		t.Fatalf("content_hash moved: %q -> %q (conditions must be hash-excluded)", origHash, h)
	}
	runs, err := st.Query(ctx, `SELECT count(*) FROM maintenance_runs WHERE kind = 'recheck'`)
	if err != nil || len(runs) != 1 || fmt.Sprint(runs[0][0]) != "1" {
		t.Fatalf("maintenance_runs kind=recheck: %v %v", runs, err)
	}

	out.Reset()
	if code := ops.RunMaintain(ctx, st, []string{"--recheck", fmt.Sprint(idRow[0][0])}, nil, &out); code != 0 || !strings.Contains(out.String(), "when demo holds (r96 real)") {
		t.Fatalf("report round-trip exit code/값 불일치: %d %s", code, out.String())
	}

	for _, s := range []string{"s2", "s3"} {
		if _, err := rs.pool.Exec(ctx, seed, "r96-realslow-"+s, strings.Repeat("body ", 80), "hash-r96-real-"+s); err != nil {
			t.Fatalf("seed %s: %v", s, err)
		}
	}
	if _, err := rs.pool.Exec(ctx, `INSERT INTO meta (key, value) VALUES ('distill_max_runes', '600')`); err != nil {
		t.Fatalf("seed meta cap: %v", err)
	}
	dir := t.TempDir()
	stderr := new(strings.Builder)
	prevErr, prevNow := distillStderr, distillNowFn
	distillStderr = stderr
	fixed := time.Date(2026, 11, 23, 9, 41, 0, 0, time.UTC)
	distillNowFn = func() time.Time { return fixed }
	t.Cleanup(func() { distillStderr, distillNowFn = prevErr, prevNow })
	var stdout strings.Builder
	if code := runDistill(ctx, st, []string{"--out", dir, "--min-score", "0"}, strings.NewReader(""), &stdout); code != 0 {
		t.Fatalf("distill exit=%d out=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stamped, err := st.Query(ctx, `SELECT count(*) FROM knowledge WHERE distilled_at IS NOT NULL`)
	if err != nil || len(stamped) != 1 {
		t.Fatalf("stamped lookup: %v", err)
	}
	firstStamped := cellInt(t, stamped[0][0])
	if firstStamped == 3 || firstStamped == 0 {
		t.Fatalf("stamped rows = %d, want a partial count (1 or 2 — the cap 600 bound)", firstStamped)
	}
	if !strings.Contains(stderr.String(), "rows deferred") {
		t.Fatalf("deferred notice missing: %q", stderr.String())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "insights-") {
		t.Fatalf("insights file missing: %v", entries)
	}
	first, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil || strings.Count(string(first), "\n## #") == 0 {
		t.Fatalf("insights body empty: %v", err)
	}

	stdout.Reset()
	if code := runDistill(ctx, st, []string{"--out", dir, "--min-score", "0"}, strings.NewReader(""), &stdout); code != 0 {
		t.Fatalf("distill rerun exit=%d out=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	again, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if string(again) != string(first) {
		t.Fatalf("same-minute rerun overwrote the earlier file (r98: must survive)")
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("rerun files = %d, want 2 (the -2 suffix): %v", len(entries), entries)
	}

	stamped, err = st.Query(ctx, `SELECT count(*) FROM knowledge WHERE distilled_at IS NOT NULL`)
	if err != nil || len(stamped) != 1 {
		t.Fatalf("post-rerun stamped lookup: %v", err)
	}
	if next := cellInt(t, stamped[0][0]); next <= firstStamped {
		t.Fatalf("post-rerun stamped = %d (was %d), want an increase (deferred rows emit)", next, firstStamped)
	}
}

func cellInt(t *testing.T, v any) int {
	t.Helper()
	switch n := v.(type) {
	case int32:
		return int(n)
	case int64:
		return int(n)
	case uint32:
		return int(n)
	case int:
		return n
	}
	t.Fatalf("count cell type %T (%v)", v, v)
	return 0
}
