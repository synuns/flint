// asof_test.go — verifies as-of normalization (relative Nh/Nd,
// ISO, hash prefix -> commit hash) on the wire package's direct surface.
// dolt_log access is stubbed with a LogEntries fake (the real-DB regression is
// cli/asof_test.go TestParseAsOfSpecRealPrefix — realStore infrastructure
// resides there). The fixture and wording assertions match the cli original's values.
package wire

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// asofLogEntries — the LogEntry fixtures (index 0 is the newest). The first two
// hashes share the "abc" prefix (the ambiguity case); the third is a base32-like
// hash (a Doltgres observation — the full [0-9a-z] range) for the relaxed-validation regression.
func asofLogEntries() []LogEntry {
	return []LogEntry{
		{Hash: "abc12f00000000000000000000000000", Date: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)},
		{Hash: "abc34f00000000000000000000000000", Date: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)},
		{Hash: "he08irluq000000000000000000000z", Date: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)},
	}
}

// asofFakeStore — a fake Store answering the 3 LogQuery kinds (prefix match,
// date <= newest 1, first commit) from the fixtures. Records the queries in calls.
type asofFakeStore struct {
	entries []LogEntry
	calls   []LogQuery
}

func (f *asofFakeStore) Migrate(ctx context.Context) error { return nil }
func (f *asofFakeStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (f *asofFakeStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (f *asofFakeStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (f *asofFakeStore) Commit(ctx context.Context, msg string) (string, error) {
	return "abc123", nil
}
func (f *asofFakeStore) Ping(ctx context.Context) error { return nil }
func (f *asofFakeStore) Close()                         {}
func (f *asofFakeStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	return nil, errors.New("asof는 원시 SQL을 치면 안 됨: " + sql)
}
func (f *asofFakeStore) DiffCommits(ctx context.Context, fromHash, toHash string) ([]DiffCommitSummary, error) {
	return nil, nil
}
func (f *asofFakeStore) QueryAsOf(ctx context.Context, q AsOfQuery) ([][]any, error) {
	return nil, nil
}
func (f *asofFakeStore) DiffRows(ctx context.Context, fromHash, toHash string) ([]KnowledgeDiffRow, error) {
	return nil, nil
}
func (f *asofFakeStore) DiffStat(ctx context.Context, hashes []string) ([]TableDiffStat, error) {
	return nil, nil
}

// LogEntries — mimics the Prefix (all prefix matches), BeforeOrAt (the newest 1
// with date <= t), and Earliest (the first commit) semantics from the fixtures
// (entries assumed descending by Date).
func (f *asofFakeStore) LogEntries(ctx context.Context, q LogQuery) ([]LogEntry, error) {
	f.calls = append(f.calls, q)
	switch {
	case q.Prefix != "":
		var out []LogEntry
		for _, e := range f.entries {
			if strings.HasPrefix(e.Hash, q.Prefix) {
				out = append(out, e)
			}
		}
		return out, nil
	case !q.BeforeOrAt.IsZero():
		for _, e := range f.entries {
			if !e.Date.After(q.BeforeOrAt) {
				return []LogEntry{e}, nil
			}
		}
		return nil, nil
	case q.Earliest:
		if len(f.entries) == 0 {
			return nil, nil
		}
		return []LogEntry{f.entries[len(f.entries)-1]}, nil
	}
	return nil, errors.New("예상 밖 LogQuery")
}

// TestWireRelativeDh — relative Nh/Nd expansion and the exclusion of invalid forms (2.5h, "2 days ago").
func TestWireRelativeDh(t *testing.T) {
	now := time.Now()
	cases := []struct {
		spec string
		want time.Duration
	}{
		{"72h", 72 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"1h", time.Hour},
	}
	for _, c := range cases {
		got, err := ParseRelative(c.spec)
		if err != nil {
			t.Fatalf("ParseRelative(%q) err=%v", c.spec, err)
		}
		want := now.Add(-c.want)
		if diff := got.Sub(want); diff < -2*time.Second || diff > 2*time.Second {
			t.Fatalf("ParseRelative(%q)=%v, want ≈%v", c.spec, got, want)
		}
	}
	for _, bad := range []string{"2.5h", "2 days ago", "h", "-3h", "7w", ""} {
		if _, err := ParseRelative(bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("ParseRelative(%q) err=%v, want ErrInvalidAsOf", bad, err)
		}
	}
}

// TestWirePrefixUniqueAmbiguousZero — prefix: 1 hit resolved, ambiguous, 0 hits, and LogQuery{Prefix} usage.
func TestWirePrefixUniqueAmbiguousZero(t *testing.T) {
	f := &asofFakeStore{entries: asofLogEntries()}
	ctx := context.Background()

	hash, err := ParseAsOfSpec(ctx, f, "abc12")
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("unique prefix → (%q,%v), want 전체 해시", hash, err)
	}
	if len(f.calls) == 0 || f.calls[0].Prefix != "abc12" {
		t.Fatalf("calls=%+v, want LogQuery{Prefix:%q}", f.calls, "abc12")
	}

	// the base32-like hash regression — accepts the lowercase-alphanumeric prefix "he0".
	hash, err = ParseAsOfSpec(ctx, f, "he0")
	if err != nil || hash != "he08irluq000000000000000000000z" {
		t.Fatalf("base32 prefix he0 → (%q,%v), want 전체 해시", hash, err)
	}

	_, err = ParseAsOfSpec(ctx, f, "abc")
	if !errors.Is(err, ErrInvalidAsOf) || !strings.Contains(err.Error(), "matches 2 commits") {
		t.Fatalf("ambiguous err=%v, want ErrInvalidAsOf with count", err)
	}
	if _, err := ParseAsOfSpec(ctx, f, "abcd"); !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("zero-match err=%v, want ErrInvalidAsOf", err)
	}
	// under 3 chars, uppercase, special characters — an immediate err without a query.
	f2 := &asofFakeStore{entries: asofLogEntries()}
	for _, bad := range []string{"ab", "ABC12", "abc12!", "zzz!"} {
		if _, err := ParseAsOfSpec(ctx, f2, bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("%q err=%v, want ErrInvalidAsOf", bad, err)
		}
	}
	if len(f2.calls) != 0 {
		t.Fatalf("무효 스펙인데 질의함: %+v", f2.calls)
	}
}

// TestWireIsoBeforeFirst — an ISO time before the first commit -> err (the message includes the first commit time).
func TestWireIsoBeforeFirst(t *testing.T) {
	f := &asofFakeStore{entries: asofLogEntries()}
	ctx := context.Background()

	spec := "2026-10-24T13:00"
	hash, err := ParseAsOfSpec(ctx, f, spec)
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("ISO %q → (%q,%v), want 최신 커밋", spec, hash, err)
	}
	if len(f.calls) == 0 || f.calls[0].BeforeOrAt.IsZero() {
		t.Fatalf("calls=%+v, want LogQuery{BeforeOrAt} 설정", f.calls)
	}

	hash, err = ParseAsOfSpec(ctx, f, "2026-10-21T00:00:00Z")
	if err != nil || hash != "he08irluq000000000000000000000z" {
		t.Fatalf("RFC3339 → (%q,%v), want 첫 커밋 해시", hash, err)
	}

	_, err = ParseAsOfSpec(ctx, f, "2026-10-01T00:00")
	if !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("before-first err=%v, want ErrInvalidAsOf", err)
	}
	if !strings.Contains(err.Error(), "2026-10-20 09:00:00") {
		t.Fatalf("문구에 첫 커밋 시각 없음: %v", err)
	}
}

// TestWireRelativeBoundary — the boundary where a relative spec connects to
// commitAtOrBefore (date <=). A t exactly equal to a commit time is adopted
// (<=); a repository with no commits gets the no-commits wording.
func TestWireRelativeBoundary(t *testing.T) {
	ctx := context.Background()

	// being now-based, the whole fixture is in the past — the newest commit is adopted.
	f := &asofFakeStore{entries: asofLogEntries()}
	hash, err := ParseAsOfSpec(ctx, f, "1h")
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("relative 1h → (%q,%v), want 최신 커밋", hash, err)
	}
	if len(f.calls) == 0 || f.calls[0].BeforeOrAt.IsZero() {
		t.Fatalf("calls=%+v, want LogQuery{BeforeOrAt}", f.calls)
	}

	// boundary — an ISO time exactly equal to the newest commit time adopts that commit (<=).
	hash, err = ParseAsOfSpec(ctx, f, "2026-10-24T12:00:00Z")
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("ISO == 커밋 시각 → (%q,%v), want 경계 포함", hash, err)
	}

	// a repository with no commits — both date<= and the first commit yield 0 rows.
	empty := &asofFakeStore{}
	_, err = ParseAsOfSpec(ctx, empty, "1h")
	if !errors.Is(err, ErrInvalidAsOf) || !strings.Contains(err.Error(), "no commits") {
		t.Fatalf("빈 저장소 err=%v, want no-commits 문구", err)
	}
}

// TestWireAsOfSpecNaturalLanguageRejected — natural language and empty specs are ErrInvalidAsOf.
func TestWireAsOfSpecNaturalLanguageRejected(t *testing.T) {
	f := &asofFakeStore{entries: asofLogEntries()}
	for _, bad := range []string{"2 days ago", "yesterday", "  "} {
		if _, err := ParseAsOfSpec(context.Background(), f, bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("%q err=%v, want ErrInvalidAsOf", bad, err)
		}
	}
	if len(f.calls) != 1 {
		t.Fatalf("질의 수=%d, want 1(yesterday 접두어 질의만): %+v", len(f.calls), f.calls)
	}
}
