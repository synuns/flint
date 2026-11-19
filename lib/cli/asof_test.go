// asof_test.go — as-of normalization: relative Nh/Nd, ISO, and hash prefixes -> commit hashes.
// dolt_log access is stubbed with a fake Store (queryFn); with SUPERLITE_REAL=1 it delegates to the
// real implementation (delegatingFake — query fixtures prefer the fake).
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// asofLogRows — dolt_log fixtures (index 0 is the newest by commit_order DESC).
// The first two hashes share the "abc" prefix (the ambiguity case); the third is a base32-like
// hash (a Doltgres observation — the full [0-9a-z] range) for the relaxed-validation regression.
func asofLogRows() [][]any {
	return [][]any{
		{"abc12f00000000000000000000000000", time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)},
		{"abc34f00000000000000000000000000", time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)},
		{"he08irluq000000000000000000000z", time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)},
	}
}

// asofFake — a queryFn mimicking the 3 queries: LIKE prefix, date <=, and first commit.
func asofFake(rows [][]any) *fakeStore {
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "LIKE"):
			prefix, _ := args[0].(string)
			var out [][]any
			for _, r := range rows {
				if strings.HasPrefix(r[0].(string), prefix) {
					out = append(out, r)
				}
			}
			return out, nil
		case strings.Contains(sql, "date <= $1"):
			t, _ := args[0].(time.Time)
			for _, r := range rows {
				if !r[1].(time.Time).After(t) {
					return [][]any{{r[0]}}, nil
				}
			}
			return nil, nil
		case strings.Contains(sql, "commit_order ASC"):
			if len(rows) == 0 {
				return nil, nil
			}
			return [][]any{{rows[len(rows)-1][1]}}, nil
		}
		return nil, errors.New("예상 밖 쿼리: " + sql)
	}
	return f
}

// asofStore — the default fake; with FLINT_TEST_DSN set + SUPERLITE_REAL=1, a store delegating to the real
// implementation (fake fallback when unset — N1 seamRealDSN).
func asofStore(t *testing.T, f *fakeStore) Store {
	t.Helper()
	if dsn, real := seamRealDSN(); real {
		realStore, err := newRealStore(context.Background(), dsn)
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		t.Cleanup(realStore.Close)
		return &delegatingFake{fake: f, real: realStore}
	}
	return f
}

// TestRelativeDh — test_relative_dh (72h, 7d, invalid 2.5h, "2 days ago").
func TestRelativeDh(t *testing.T) {
	now := time.Now()
	cases := []struct {
		spec string
		want time.Duration // past offset from now
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

// TestPrefixUniqueAmbiguousZero — prefix: 1 hit resolved, ambiguous, 0 hits, and
// LIKE parameterization (injection-proofing).
func TestPrefixUniqueAmbiguousZero(t *testing.T) {
	f := asofFake(asofLogRows())
	st := asofStore(t, f)
	ctx := context.Background()

	// 1 hit resolved — prefix "abc12" -> full hash returned.
	hash, err := ParseAsOfSpec(ctx, st, "abc12")
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("unique prefix → (%q,%v), want 전체 해시", hash, err)
	}
	// LIKE parameterized — SQL uses a $1 placeholder; the prefix is an argument.
	if len(f.queries) == 0 || !strings.Contains(f.queries[len(f.queries)-1], "LIKE $1||'%'") {
		t.Fatalf("SQL=%v, want LIKE $1||'%%' 파라미터화", f.queries)
	}

	// base32-like hash regression (closes an observed defect) — accepts the lowercase-alphanumeric prefix "he0".
	hash, err = ParseAsOfSpec(ctx, st, "he0")
	if err != nil || hash != "he08irluq000000000000000000000z" {
		t.Fatalf("base32 prefix he0 → (%q,%v), want 전체 해시", hash, err)
	}

	// ambiguous — "abc" matches 2 rows -> err.
	if _, err := ParseAsOfSpec(ctx, st, "abc"); !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("ambiguous err=%v, want ErrInvalidAsOf", err)
	}
	// 0 hits — no match for prefix "abcd" -> err.
	if _, err := ParseAsOfSpec(ctx, st, "abcd"); !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("zero-match err=%v, want ErrInvalidAsOf", err)
	}
	// under 3 chars, uppercase, special chars — instant err without a query.
	f2 := asofFake(asofLogRows())
	st2 := asofStore(t, f2)
	for _, bad := range []string{"ab", "ABC12", "abc12!", "zzz!"} {
		if _, err := ParseAsOfSpec(ctx, st2, bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("%q err=%v, want ErrInvalidAsOf", bad, err)
		}
	}
	if len(f2.queries) != 0 {
		t.Fatalf("무효 스펙인데 질의함: %v", f2.queries)
	}
}

// TestIsoBeforeFirst — an ISO time before the first commit -> err; the message includes
// the first commit time. A valid ISO picks the newest commit at or before the time.
func TestIsoBeforeFirst(t *testing.T) {
	f := asofFake(asofLogRows())
	st := asofStore(t, f)
	ctx := context.Background()

	// valid — a time between t3 and t2 -> picks the newest commit (abc12f...).
	spec := "2026-10-24T13:00"
	hash, err := ParseAsOfSpec(ctx, st, spec)
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("ISO %q → (%q,%v), want 최신 커밋", spec, hash, err)
	}
	// the date-comparison argument is a Go-computed time.Time (no SQL intervals).
	lastSQL := f.queries[0]
	if !strings.Contains(lastSQL, "date <= $1") {
		t.Fatalf("date 비교 SQL=%q, want 파라미터화", lastSQL)
	}

	// RFC3339 form is also accepted.
	hash, err = ParseAsOfSpec(ctx, st, "2026-10-21T00:00:00Z")
	if err != nil || hash != "he08irluq000000000000000000000z" {
		t.Fatalf("RFC3339 → (%q,%v), want 첫 커밋 해시", hash, err)
	}

	// before the first commit (2026-10-20 09:00) -> ErrInvalidAsOf + the first commit time in the message.
	_, err = ParseAsOfSpec(ctx, st, "2026-10-01T00:00")
	if !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("before-first err=%v, want ErrInvalidAsOf", err)
	}
	if !strings.Contains(err.Error(), "2026-10-20 09:00:00") {
		t.Fatalf("문구에 첫 커밋 시각 없음: %v", err)
	}
}

// TestAsOfSpecNaturalLanguageRejected — natural language and empty specs are ErrInvalidAsOf (§1 exclusion).
// Specs with spaces or empty err immediately without a query. A pure lowercase word ("yesterday") is a plausible
// alphabetical prefix candidate, so it is queried and errs via 0 matches (the normal path).
func TestAsOfSpecNaturalLanguageRejected(t *testing.T) {
	f := asofFake(asofLogRows())
	st := asofStore(t, f)
	for _, bad := range []string{"2 days ago", "yesterday", "  "} {
		if _, err := ParseAsOfSpec(context.Background(), st, bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("%q err=%v, want ErrInvalidAsOf", bad, err)
		}
	}
	// only "2 days ago" (with a space) queries once — the single "yesterday" case is a normal query on the prefix path.
	if len(f.queries) != 1 {
		t.Fatalf("질의 수=%d, want 1(yesterday 접두어 질의만): %v", len(f.queries), f.queries)
	}
}

// TestParseAsOfSpecRealPrefix — real-DB regression (only when FLINT_DSN is set): after committing one write,
// verifies by observation that a 3-character prefix of the real (base32-like) hash makes ParseAsOfSpec return the
// full hash. End-to-end guarantee of the relaxed lowercase-alphanumeric validation.
// The uniq row is deleted and committed in cleanup (preventing shared-DB pending pollution).
func TestParseAsOfSpecRealPrefix(t *testing.T) {
	dsn := os.Getenv("FLINT_DSN")
	if dsn == "" {
		t.Skip("FLINT_DSN 미설정 — 실DB 회귀 건너뜀")
	}
	ctx := context.Background()
	real, err := newRealStore(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	t.Cleanup(real.Close)
	// avoid an empty commit — stage exactly 1 unique content_hash, then commit.
	uniq := fmt.Sprintf("asof-t81-%d", time.Now().UnixNano())
	recs := []map[string]any{{
		"kind": "knowledge", "topic": uniq, "summary": "as-of 회귀",
		"tags": ":asof:", "confidence": 0.9, "content_hash": uniq,
		"status": "pending", "ingested_at": time.Now(),
	}}
	if _, err := real.InsertBatch(ctx, recs); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	// prevent shared-DB pollution — delete and commit the uniq pending row this test leaves at the end
	// (a pollution source for the global e2e pending assertion). t.Cleanup is LIFO, so this cleanup is
	// registered after (the real.Close registration above) so it runs before real.Close.
	t.Cleanup(func() {
		rs, ok := real.(*realStore)
		if !ok {
			t.Fatalf("uniq 행 정리 실패: real이 *realStore 아님(%T)", real)
		}
		if _, err := rs.pool.Exec(ctx,
			`DELETE FROM knowledge WHERE topic = $1`, uniq); err != nil {
			t.Fatalf("uniq 행 정리 실패: %v", err)
		}
		if _, err := real.Commit(ctx, "asof prefix 회귀 정리 [session:T81]"); err != nil {
			t.Fatalf("정리 커밋 실패: %v", err)
		}
	})
	full, err := real.Commit(ctx, "asof prefix 회귀 [session:T81]")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(full) < asofMinPrefix {
		t.Fatalf("커밋 해시가 너무 짧음: %q", full)
	}
	// prefix window selection — by product contract the relative syntax (\d+[hd]) parses before prefixes
	// (asof.go, priority 1), so windows that happen to overlap a relative form like "97h"
	// are avoided (preventing flakes where the relative interpretation fails first on a fresh DB).
	pre := ""
	for i := 0; i+asofMinPrefix <= len(full); i++ {
		w := full[i : i+asofMinPrefix]
		if _, err := ParseRelative(w); err != nil {
			pre = w
			break
		}
	}
	if pre == "" {
		t.Fatalf("상대 문법과 충돌하지 않는 접두어 창을 못 찾음: %q", full)
	}
	got, err := ParseAsOfSpec(ctx, real, pre)
	if err != nil {
		t.Fatalf("ParseAsOfSpec(%q) err=%v (base32 해시 완화 확인)", pre, err)
	}
	if got != full {
		t.Fatalf("접두어 정규화=%q, want 전체 해시 %q", got, full)
	}
}
