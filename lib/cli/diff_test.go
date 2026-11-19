// diff_test.go — flint diff: row details, one-line JSON summary, invalid-spec exit 3.
// Built on canned wire.Store contract overrides (LogEntries prefix, DiffRows, DiffCommits).
// SUPERLITE_REAL=1 is the real-implementation delegation seam.
package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// Hash fixtures fixed by observation (2026-10-24) — for prefix-normalization responses.
const (
	diffFromHash = "aaaa1111aaaa1111aaaa1111aaaa1111"
	diffToHash   = "bbbb2222bbbb2222bbbb2222bbbb2222"
)

// diffCliFake — contract canned for the diff command: prefix normalization via LogEntries,
// row details via DiffRows, commit summary via DiffCommits overrides. Prefix arguments are
// recorded in logPrefixes (parameterization verification — proof of query-argument passing).
type diffCliFake struct {
	*fakeStore
	logPrefixes []string
}

func (f *diffCliFake) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	if q.Prefix == "" {
		return nil, nil
	}
	f.logPrefixes = append(f.logPrefixes, q.Prefix)
	switch {
	case strings.HasPrefix(diffFromHash, q.Prefix):
		return []wire.LogEntry{{Hash: diffFromHash}}, nil
	case strings.HasPrefix(diffToHash, q.Prefix):
		return []wire.LogEntry{{Hash: diffToHash}}, nil
	}
	return nil, nil
}

func (f *diffCliFake) DiffRows(ctx context.Context, fromHash, toHash string) ([]wire.KnowledgeDiffRow, error) {
	// observed diff_type distribution: added 2, modified 1, removed 1 (IDs are contract strings).
	return []wire.KnowledgeDiffRow{
		{DiffType: "added", ID: "7", Topic: "new-topic"},
		{DiffType: "added", ID: "8", Topic: "newer-topic"},
		{DiffType: "modified", ID: "3", Topic: "changed-topic"},
		{DiffType: "removed", ID: "5", Topic: "gone-topic"},
	}, nil
}

func (f *diffCliFake) DiffCommits(ctx context.Context, fromHash, toHash string) ([]wire.DiffCommitSummary, error) {
	return []wire.DiffCommitSummary{{Hash: diffToHash, Date: time.Now(), Message: "second write", DataChange: true}}, nil
}

// TestDiffAddedCounts — default-mode one-line JSON counts and row details.
func TestDiffAddedCounts(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
	out, code := mustRun(t, []string{"diff", "aaaa1", "bbbb2"}, "")
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	m := decode(t, out)
	if m["from"] != diffFromHash || m["to"] != diffToHash {
		t.Fatalf("from/to 해시 정규화 실패: %v", m)
	}
	for k, want := range map[string]float64{"added": 2, "modified": 1, "removed": 1} {
		if got, _ := m[k].(float64); got != want {
			t.Fatalf("%s=%v want %v", k, m[k], want)
		}
	}
	rows, _ := m["rows"].([]any)
	if len(rows) != 4 {
		t.Fatalf("rows len=%d want 4", len(rows))
	}
	first, _ := rows[0].(map[string]any)
	if first["diff_type"] != "added" || first["id"] != "7" {
		t.Fatalf("rows[0]=%v", rows[0])
	}
	commits, _ := m["commits"].([]any)
	if len(commits) != 1 {
		t.Fatalf("commits len=%d want 1", len(commits))
	}
	cm, _ := commits[0].(map[string]any)
	if cm["hash"] != diffToHash || cm["data_change"] != true {
		t.Fatalf("commits[0]=%v", cm)
	}
}

// TestDiffSummaryShape — --summary shows only counts + commit summary, no rows.
func TestDiffSummaryShape(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
	out, code := mustRun(t, []string{"diff", "aaaa1", "bbbb2", "--summary"}, "")
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	m := decode(t, out)
	if _, ok := m["rows"]; ok {
		t.Fatalf("--summary에 rows 포함: %s", out)
	}
	for _, k := range []string{"from", "to", "added", "modified", "removed", "commits"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("키 %s 부재: %s", k, out)
		}
	}
	commits, _ := m["commits"].([]any)
	cm, _ := commits[0].(map[string]any)
	for _, k := range []string{"hash", "date", "message", "data_change", "schema_change"} {
		if _, ok := cm[k]; !ok {
			t.Fatalf("commits[0] 키 %s 부재: %v", k, cm)
		}
	}
}

// TestDiffInvalidExit3 — invalid as-of specs exit 3 (from and to each).
func TestDiffInvalidExit3(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
	cases := [][]string{
		{"diff", "2 days ago", "bbbb2"},
		{"diff", "aaaa1", "zzz9"}, // valid format (lowercase base32) but a prefix with 0 matches on a real DB
		{"diff", "aaaa1"},         // too few args
	}
	for _, args := range cases {
		out, code := mustRun(t, args, "")
		if code != 3 {
			t.Fatalf("%v exit=%d want 3 out=%s", args, code, out)
		}
		m := decode(t, out)
		if errKind, _ := m["error"].(string); errKind != "invalid_as_of" &&
			!strings.HasPrefix(errKind, "usage") {
			t.Fatalf("%v error=%v", args, m["error"])
		}
	}
}

// TestDiffPrefixParameterized — hash prefixes are passed as LogQuery{Prefix} values
// (capture proof that the prefix flows as a query argument and contract field — not SQL splicing).
func TestDiffPrefixParameterized(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
	out, code := mustRun(t, []string{"diff", "aaaa1", "bbbb2"}, "")
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	if len(f.logPrefixes) != 2 || f.logPrefixes[0] != "aaaa1" || f.logPrefixes[1] != "bbbb2" {
		t.Fatalf("접두어 인자 기록: %v", f.logPrefixes)
	}
}

// TestDiffSeamReal — SUPERLITE_REAL=1 real-implementation seam: after 2 write commits (A, B),
// diff A B yields added >= 1 with from/to hashes matching. The hashes are pinned by commit-message
// session tags, deterministic regardless of concurrent commits. Skips without a real DB. The real DB
// is used only when FLINT_TEST_DSN is set (never the default production DSN).
func TestDiffSeamReal(t *testing.T) {
	dsn, ok := seamRealDSN()
	if !ok {
		t.Skip("실구현 seam 검증은 SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시만")
	}
	f := &fakeStore{}
	withFake(t, f) // no stubs set — everything delegates to the real implementation
	if out, code := mustRun(t, []string{"write", "knowledge", "--session", "diff-seam"},
		`{"topic":"diff-seam-a","summary":"seam a","confidence":0.5}`); code != 0 {
		t.Fatalf("write A exit=%d out=%s", code, out)
	}
	if out, code := mustRun(t, []string{"write", "knowledge", "--session", "diff-seam"},
		`{"topic":"diff-seam-b","summary":"seam b","confidence":0.6}`); code != 0 {
		t.Fatalf("write B exit=%d out=%s", code, out)
	}
	// look up our two commits by session tag (newest first -> [B, A]). The LogEntries contract
	// (LatestN + MessageLike filter — hash and date typed) means the spec uses ISO times
	// (the hex-prefix assumption in asof.go mismatched observed Doltgres hashes, so
	// ISO-time specs are used — carried over from the incident record).
	ctx := context.Background()
	real, err := newRealStore(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	defer real.Close()
	entries, err := real.LogEntries(ctx, wire.LogQuery{
		LatestN: 2, MessageLike: `%[session:diff-seam]%`,
	})
	if err != nil || len(entries) != 2 {
		t.Fatalf("세션 커밋 조회 실패: entries=%d err=%v", len(entries), err)
	}
	latestHash, prevHash := entries[0].Hash, entries[1].Hash
	latestDate, prevDate := entries[0].Date, entries[1].Date
	out, code := mustRun(t, []string{"diff", prevDate.Format(time.RFC3339Nano), latestDate.Format(time.RFC3339Nano)}, "")
	if code != 0 {
		t.Fatalf("diff exit=%d out=%s", code, out)
	}
	m := decode(t, out)
	if m["from"] != prevHash || m["to"] != latestHash {
		t.Fatalf("from/to 불일치: %s", out)
	}
	if added, _ := m["added"].(float64); added < 1 {
		t.Fatalf("added=%v want ≥1: %s", m["added"], out)
	}
	// diff of identical points is 0 — normalization symmetry check.
	out2, code2 := mustRun(t, []string{"diff", latestDate.Format(time.RFC3339Nano), latestDate.Format(time.RFC3339Nano)}, "")
	if code2 != 0 {
		t.Fatalf("동일 지점 exit=%d out=%s", code2, out2)
	}
	m2 := decode(t, out2)
	if m2["added"].(float64) != 0 || m2["removed"].(float64) != 0 {
		t.Fatalf("동일 지점 diff가 0이 아님: %s", out2)
	}
}
