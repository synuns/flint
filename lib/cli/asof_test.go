// asof_test.go — as-of normalization: relative Nh/Nd, ISO, and hash prefixes -> commit hashes.
// dolt_log access is stubbed with a fake Store (queryFn); with SUPERLITE_REAL=1 it delegates to the
// real implementation (delegatingFake — query fixtures prefer the fake).
package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
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

// TestReadAsOfPreFlintScratchRealSlow — slow (SUPERLITE_REAL=1 + FLINT_TEST_DSN):
// the v1.19.5 R81 real-DB regression (verdict re-based in v1.19.6 R82) on a
// dedicated scratch DB (created and dropped here — never a shared DB).
// newRealStore applies the schema without making a dolt commit, so before
// the first flint write the only commits are the Doltgres auto-initial pair
// — read --as-of (0h, the initial-commit prefix, an ISO just after the
// initial commits) must reject exit 3 with the pre-flint diagnostic
// (pre-fix: the resolved hash reached the as-of view query and failed with
// a raw "references invalid table(s)" error, exit 1). After one seeded
// flint write the same spec kinds resolve normally (0h, the flint-commit
// prefix, an ISO at the first flint commit). Then the v1.19.6 R82 case: a
// MANUAL dolt_commit reusing the "CREATE DATABASE" label after the flint
// write (the schema exists at that commit; its committer is the connecting
// user — the same value as the auto commit, measured) must resolve
// normally too (pre-fix: the label match alone rejected it, exit 3).
func TestReadAsOfPreFlintScratchRealSlow(t *testing.T) {
	dsn, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	// Close via t.Cleanup registered BEFORE the DROP below — Cleanup is LIFO
	// (the DROP needs the pool still open).
	t.Cleanup(base.Close)
	name := fmt.Sprintf("flint_v1196_r82_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort DROP with a short retry — right after the store Close,
		// Doltgres may still hold the session and reject the drop.
		var last error
		for i := 0; i < 3; i++ {
			if _, last = base.Exec(ctx, "DROP DATABASE IF EXISTS "+name); last == nil {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Logf("스크래치 DB 잔존(%q): %v", name, last)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + name
	st, err := newRealStore(ctx, u.String())
	if err != nil {
		t.Fatalf("newRealStore(scratch): %v", err)
	}
	t.Cleanup(st.Close)

	// A. Fresh (no flint write) — the newest commit is the auto-initial pair.
	entries, err := st.LogEntries(ctx, wire.LogQuery{BeforeOrAt: time.Now()})
	if err != nil || len(entries) != 1 {
		t.Fatalf("초기 커밋 조회 → (%d행, %v), want 1행", len(entries), err)
	}
	if entries[0].Message != "CREATE DATABASE" {
		t.Fatalf("신규 DB 최신 커밋 메시지=%q, want CREATE DATABASE(자동 초기 커밋)", entries[0].Message)
	}
	initHash, initDate := entries[0].Hash, entries[0].Date
	initPrefix := nonRelativeWindow(t, initHash)
	initISO := initDate.Add(time.Second).Format(time.RFC3339)
	for _, spec := range []string{"0h", initPrefix, initISO} {
		var out strings.Builder
		code := runRead(ctx, st, []string{"--as-of", spec, "v1195-r81-합성"}, strings.NewReader(""), &out)
		if code != 3 {
			t.Fatalf("신규 DB read --as-of %q exit=%d, want 3; out=%s", spec, code, out.String())
		}
		if !strings.Contains(out.String(), "pre-flint") {
			t.Fatalf("신규 DB %q 진단에 pre-flint 없음: %s", spec, out.String())
		}
	}

	// B. Seeded — one flint write commits; the same spec kinds resolve normally.
	uniq := fmt.Sprintf("asof-r81-%d", time.Now().UnixNano())
	recs := []map[string]any{{
		"kind": "knowledge", "topic": uniq, "summary": "as-of r81 회귀",
		"tags": ":asof:", "confidence": 0.9, "content_hash": uniq,
		"status": "pending", "ingested_at": time.Now(),
	}}
	if _, err := st.InsertBatch(ctx, recs); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	flintHash, err := st.Commit(ctx, "knowledge: asof r81 seed [session:0]")
	if err != nil || len(flintHash) < asofMinPrefix {
		t.Fatalf("첫 flint 커밋 실패(%q,%v)", flintHash, err)
	}
	fe, err := st.LogEntries(ctx, wire.LogQuery{Hash: flintHash})
	if err != nil || len(fe) != 1 {
		t.Fatalf("flint 커밋 조회 → (%d행, %v), want 1행", len(fe), err)
	}
	flintPrefix := nonRelativeWindow(t, flintHash)
	flintISO := fe[0].Date.Add(time.Second).Format(time.RFC3339)
	for _, spec := range []string{"0h", flintPrefix, flintISO} {
		var out strings.Builder
		code := runRead(ctx, st, []string{"--as-of", spec, uniq}, strings.NewReader(""), &out)
		if code != 0 {
			t.Fatalf("시드 후 read --as-of %q exit=%d, want 0; out=%s", spec, code, out.String())
		}
	}

	// C. v1.19.6 R82 — a MANUAL dolt commit reusing the auto-initial
	// "CREATE DATABASE" label after the flint write: the flint schema exists
	// at that commit, so every spec resolving to it must adopt normally
	// (pre-fix: the label match alone rejected it, exit 3). Made on a second
	// raw connection — flint's Commit only makes flint-label messages.
	uniq2 := fmt.Sprintf("asof-r82-%d", time.Now().UnixNano())
	recs2 := []map[string]any{{
		"kind": "knowledge", "topic": uniq2, "summary": "as-of r82 회귀",
		"tags": ":asof:", "confidence": 0.9, "content_hash": uniq2,
		"status": "pending", "ingested_at": time.Now(),
	}}
	if _, err := st.InsertBatch(ctx, recs2); err != nil {
		t.Fatalf("InsertBatch(r82): %v", err)
	}
	scr, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("스크래치 원시 연결: %v", err)
	}
	t.Cleanup(scr.Close)
	if _, err := scr.Exec(ctx, "SELECT dolt_add('knowledge')"); err != nil {
		t.Fatalf("dolt_add(knowledge): %v", err)
	}
	var manualHash string
	if err := scr.QueryRow(ctx, `SELECT dolt_commit('-m', $1)`, "CREATE DATABASE").Scan(&manualHash); err != nil {
		t.Fatalf("수동 CREATE DATABASE 라벨 커밋: %v", err)
	}
	me, err := st.LogEntries(ctx, wire.LogQuery{Hash: manualHash})
	if err != nil || len(me) != 1 {
		t.Fatalf("수동 라벨 커밋 조회 → (%d행, %v), want 1행", len(me), err)
	}
	if me[0].Message != "CREATE DATABASE" {
		t.Fatalf("수동 라벨 커밋 메시지=%q, want CREATE DATABASE", me[0].Message)
	}
	manualPrefix := nonRelativeWindow(t, manualHash)
	manualISO := me[0].Date.Add(time.Second).Format(time.RFC3339)
	for _, spec := range []string{"0h", manualPrefix, manualISO} {
		var out strings.Builder
		code := runRead(ctx, st, []string{"--as-of", spec, uniq2}, strings.NewReader(""), &out)
		if code != 0 {
			t.Fatalf("수동 라벨 커밋 read --as-of %q exit=%d, want 0; out=%s", spec, code, out.String())
		}
	}
}

// nonRelativeWindow — a 3-char prefix window of the hash that does not parse
// as the relative form (the relative syntax takes precedence — the
// TestParseAsOfSpecRealPrefix convention).
func nonRelativeWindow(t *testing.T, hash string) string {
	t.Helper()
	for i := 0; i+asofMinPrefix <= len(hash); i++ {
		w := hash[i : i+asofMinPrefix]
		if _, err := ParseRelative(w); err != nil {
			return w
		}
	}
	t.Fatalf("상대 문법과 충돌하지 않는 접두어 창을 못 찾음: %q", hash)
	return ""
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
