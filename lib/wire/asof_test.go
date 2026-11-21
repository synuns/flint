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
// date <= newest 1, first commit) from the fixtures. Records the queries in
// calls. QueryAsOf simulates the v1.19.6 R82 schema probe: hashes in noSchema
// answer the measured base-table absence error, everything else succeeds.
type asofFakeStore struct {
	entries  []LogEntry
	calls    []LogQuery
	noSchema map[string]bool
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
	if f.noSchema[q.Hash] {
		// the measured Doltgres shape — a table absent at the probed commit
		return nil, errors.New("table not found: knowledge")
	}
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

// TestWireRelativeCeiling — a relative spec past the Duration ceiling
// (2,562,047h) is rejected up front (v1.19.3 R77-1/R78-1): the pre-fix
// time.Duration(v)*time.Hour wrapped negative → a FUTURE as-of time, which
// commitAtOrBefore silently resolved to the newest commit (exit 0 with a
// meaningless verdict). 106751d (2,562,024h) is the largest day form that
// still fits — finite, not clamped-to-infinity.
func TestWireRelativeCeiling(t *testing.T) {
	for _, bad := range []string{"4000000h", "2562048h", "999999999d", "106752d"} {
		if _, err := ParseRelative(bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("ParseRelative(%q) err=%v, want ErrInvalidAsOf(상한 초과 거부)", bad, err)
		}
	}
	for _, ok := range []string{"24h", "7d", "2562047h", "106751d"} {
		got, err := ParseRelative(ok)
		if err != nil {
			t.Fatalf("ParseRelative(%q) err=%v, want 통과(상한 내)", ok, err)
		}
		if !got.Before(time.Now()) {
			t.Fatalf("ParseRelative(%q)=%v — 과거 시각 아님(래프 의심)", ok, got)
		}
	}
	// 106751d resolves to a finite past time (≈ now − 2,562,024h), not a
	// wrapped/instant value — the boundary is a real span, not a rejection wall.
	got, _ := ParseRelative("106751d")
	if want := time.Now().Add(-106751 * 24 * time.Hour); got.Sub(want) > 2*time.Second || got.Sub(want) < -2*time.Second {
		t.Fatalf("ParseRelative(106751d)=%v, want ≈%v(유한 최대 스팬)", got, want)
	}
}

// TestWireRelativeDayWrapGuard — the v1.19.4 R79-1 regression: a day count whose
// v*24 wraps int64 slipped past both post-checks (a wrapped negative is not > ceiling)
// and resolved a meaningless as-of time — MaxInt64 days wrapped to −24h (a FUTURE
// time → newest commit, exit 0) and (2^64+8)/24 days to +8h. Both now reject BEFORE
// the multiply; through ParseAsOfSpec they never reach the store.
func TestWireRelativeDayWrapGuard(t *testing.T) {
	for _, bad := range []string{"9223372036854775807d", "768614336404564651d"} {
		if _, err := ParseRelative(bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("ParseRelative(%q) err=%v, want ErrInvalidAsOf(곱셈 전 가드)", bad, err)
		}
	}
	f := &asofFakeStore{entries: asofLogEntries()}
	for _, bad := range []string{"9223372036854775807d", "768614336404564651d"} {
		if _, err := ParseAsOfSpec(context.Background(), f, bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("ParseAsOfSpec(%q) err=%v, want ErrInvalidAsOf", bad, err)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("래프 스펙이 저장소 질의까지 감(무의미 해석 여지): %+v", f.calls)
	}
}

// TestWireCeilingHintReachable — the v1.19.4 R79-2/R80-2 regression: a past-ceiling
// Nh spec (4000000h) failed relative parsing, fell through to the hash-prefix step
// (lowercase-alphanumeric) and misreported the ceiling rejection as "no commit
// matches prefix" — the hint could never reach the user. The shaped-failure path now
// returns the ceiling wording itself.
func TestWireCeilingHintReachable(t *testing.T) {
	f := &asofFakeStore{entries: asofLogEntries()}
	_, err := ParseAsOfSpec(context.Background(), f, "4000000h")
	if !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("err=%v, want ErrInvalidAsOf", err)
	}
	if !strings.Contains(err.Error(), "exceeds") || !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("상한 문구 도달 안 함: %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("접두어 폴백 질의함(오안내 경로): %+v", f.calls)
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

// TestWirePreFlintCommitRejected — the v1.19.5 R81 regression (verdict
// re-based in v1.19.6 R82): on a fresh DB (no flint write yet) the only
// commits are the 2 Doltgres automatic initial ones, and an as-of resolving
// to them (0h, an ISO after them, or their hash prefix) must reject with the
// pre-flint diagnostic (exit 3 convention) — pre-fix the resolved hash
// reached the as-of view query and failed with a raw "references invalid
// table(s)" error (exit 1). The verdict is the schema probe, not the message
// alone: with a flint write present, a MANUAL commit reusing the
// "CREATE DATABASE" label (the schema exists at that commit) resolves
// normally on both paths — the v1.19.6 R82 false-rejection regression
// (pre-fix: exit 3 despite the schema existing there). The committer is
// deliberately not a discriminator (measured: the auto CREATE DATABASE
// commit carries the connecting user, the same value as a manual commit).
func TestWirePreFlintCommitRejected(t *testing.T) {
	ctx := context.Background()
	// now-anchored fixture dates (the relative specs resolve against time.Now
	// — a fixed date drifts in/out of the future as the wall clock moves).
	// Truncated to whole seconds so an ISO spec formatted from them re-parses
	// to the exact same instant (no sub-second rounding mismatch).
	now := time.Now()
	initNew := now.Add(-2 * time.Hour).Truncate(time.Second) // CREATE DATABASE commit
	initOld := initNew.Add(-time.Second)                     // Initialize data repository
	flintAt := now.Add(-time.Hour).Truncate(time.Second)     // the first flint write
	manualAt := now.Add(-30 * time.Minute).Truncate(time.Second)
	// fresh-DB fixture — only the 2 auto-initial commits (newest first);
	// the probe answers "absent" for both (no flint schema at them).
	fresh := &asofFakeStore{
		entries: []LogEntry{
			{Hash: "cr8ate000000000000000000000000db", Message: "CREATE DATABASE", Date: initNew},
			{Hash: "in1t000000000000000000000000000r", Message: "Initialize data repository", Date: initOld},
		},
		noSchema: map[string]bool{
			"cr8ate000000000000000000000000db": true,
			"in1t000000000000000000000000000r": true,
		},
	}
	for _, spec := range []string{
		"0h", // relative — resolves to the newest (CREATE DATABASE)
		initNew.Add(time.Minute).Format(time.RFC3339), // ISO after both initial commits
		"cr8",                        // prefix of the CREATE DATABASE commit
		"in1",                        // prefix of the Initialize commit
		initNew.Format(time.RFC3339), // ISO exactly at the CREATE DATABASE commit
	} {
		_, err := ParseAsOfSpec(ctx, fresh, spec)
		if !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("fresh %q err=%v, want ErrInvalidAsOf", spec, err)
		}
		if !strings.Contains(err.Error(), "pre-flint") {
			t.Fatalf("fresh %q 문구에 pre-flint 없음: %v", spec, err)
		}
	}

	// seeded fixture — initial pair + one flint commit + a MANUAL commit
	// reusing the "CREATE DATABASE" label AFTER the flint write (the R82
	// scenario). The manual label commit is NOT in noSchema — the schema
	// exists at it — so every spec resolving to it adopts normally; specs
	// resolving to the genuine initial commits still reject.
	const flintH = "kn0w0000000000000000000000000abc"
	const manualH = "man0ua100000000000000000000000cd"
	seeded := &asofFakeStore{
		entries: []LogEntry{
			{Hash: manualH, Message: "CREATE DATABASE", Date: manualAt}, // manual, schema present
			{Hash: flintH, Message: "knowledge: seed write [session:0]", Date: flintAt},
		},
		noSchema: fresh.noSchema, // only the genuine initial commits
	}
	seeded.entries = append(seeded.entries, fresh.entries...)
	rejected := "<rejected>" // marker — a time resolving to a genuine initial commit
	for spec, want := range map[string]string{
		"0h":                          manualH, // newest = the manual label commit → adopt (R82)
		"man":                         manualH, // manual label commit prefix → adopt (R82)
		manualAt.Format(time.RFC3339): manualH, // ISO exactly at the manual label commit
		"kn0":                         flintH,  // flint commit prefix
		flintAt.Format(time.RFC3339):  flintH,  // ISO at the first flint commit
		initNew.Add(30 * time.Minute).Format(time.RFC3339): rejected, // between: initial commit adopted → rejected
	} {
		hash, err := ParseAsOfSpec(ctx, seeded, spec)
		if want == rejected {
			// a time pointing before the first flint write resolves to the
			// CREATE DATABASE auto commit → the probe says absent → rejected.
			if !errors.Is(err, ErrInvalidAsOf) || !strings.Contains(err.Error(), "pre-flint") {
				t.Fatalf("seeded %q → (%q,%v), want pre-flint 거부", spec, hash, err)
			}
			continue
		}
		if err != nil || hash != want {
			t.Fatalf("seeded %q → (%q,%v), want %q", spec, hash, err, want)
		}
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
