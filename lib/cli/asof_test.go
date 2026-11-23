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

func asofLogRows() [][]any {
	return [][]any{
		{"abc12f00000000000000000000000000", time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)},
		{"abc34f00000000000000000000000000", time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)},
		{"he08irluq000000000000000000000z", time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)},
	}
}

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

func TestRelativeDh(t *testing.T) {
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

func TestPrefixUniqueAmbiguousZero(t *testing.T) {
	f := asofFake(asofLogRows())
	st := asofStore(t, f)
	ctx := context.Background()

	hash, err := ParseAsOfSpec(ctx, st, "abc12")
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("unique prefix → (%q,%v), want 전체 해시", hash, err)
	}

	if len(f.queries) == 0 || !strings.Contains(f.queries[len(f.queries)-1], "LIKE $1||'%'") {
		t.Fatalf("SQL=%v, want LIKE $1||'%%' 파라미터화", f.queries)
	}

	hash, err = ParseAsOfSpec(ctx, st, "he0")
	if err != nil || hash != "he08irluq000000000000000000000z" {
		t.Fatalf("base32 prefix he0 → (%q,%v), want 전체 해시", hash, err)
	}

	if _, err := ParseAsOfSpec(ctx, st, "abc"); !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("ambiguous err=%v, want ErrInvalidAsOf", err)
	}

	if _, err := ParseAsOfSpec(ctx, st, "abcd"); !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("zero-match err=%v, want ErrInvalidAsOf", err)
	}

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

func TestIsoBeforeFirst(t *testing.T) {
	f := asofFake(asofLogRows())
	st := asofStore(t, f)
	ctx := context.Background()

	spec := "2026-10-24T13:00"
	hash, err := ParseAsOfSpec(ctx, st, spec)
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("ISO %q → (%q,%v), want 최신 커밋", spec, hash, err)
	}

	lastSQL := f.queries[0]
	if !strings.Contains(lastSQL, "date <= $1") {
		t.Fatalf("date 비교 SQL=%q, want 파라미터화", lastSQL)
	}

	hash, err = ParseAsOfSpec(ctx, st, "2026-10-21T00:00:00Z")
	if err != nil || hash != "he08irluq000000000000000000000z" {
		t.Fatalf("RFC3339 → (%q,%v), want 첫 커밋 해시", hash, err)
	}

	_, err = ParseAsOfSpec(ctx, st, "2026-10-01T00:00")
	if !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("before-first err=%v, want ErrInvalidAsOf", err)
	}
	if !strings.Contains(err.Error(), "2026-10-20 09:00:00") {
		t.Fatalf("문구에 첫 커밋 시각 없음: %v", err)
	}
}

func TestAsOfSpecNaturalLanguageRejected(t *testing.T) {
	f := asofFake(asofLogRows())
	st := asofStore(t, f)
	for _, bad := range []string{"2 days ago", "yesterday", "  "} {
		if _, err := ParseAsOfSpec(context.Background(), st, bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("%q err=%v, want ErrInvalidAsOf", bad, err)
		}
	}

	if len(f.queries) != 1 {
		t.Fatalf("질의 수=%d, want 1(yesterday 접두어 질의만): %v", len(f.queries), f.queries)
	}
}

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

	t.Cleanup(base.Close)
	name := fmt.Sprintf("flint_v1196_r82_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() {

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

	uniq := fmt.Sprintf("asof-t81-%d", time.Now().UnixNano())
	recs := []map[string]any{{
		"kind": "knowledge", "topic": uniq, "summary": "as-of 회귀",
		"tags": ":asof:", "confidence": 0.9, "content_hash": uniq,
		"status": "pending", "ingested_at": time.Now(),
	}}
	if _, err := real.InsertBatch(ctx, recs); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

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
