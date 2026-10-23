// asof_test.go — T81 as-of 정규화: 상대 Nh/Nd·ISO·해시 접두어 → 커밋 해시.
// dolt_log 접근은 fake Store(queryFn)으로 세우고 SUPERLITE_REAL=1이면
// 실구현 위임(delegatingFake — 질의 픽스처는 fake 우선).
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

// asofLogRows — dolt_log 픽스처(인덱스 0이 commit_order DESC 최신).
// 첫 두 해시는 "abc" 접두어 공유(모호성 케이스), 셋째는 base32계
// (Doltgres 실측 — [0-9a-z] 전범위) 해시로 완화 검증 회귀.
func asofLogRows() [][]any {
	return [][]any{
		{"abc12f00000000000000000000000000", time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)},
		{"abc34f00000000000000000000000000", time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)},
		{"he08irluq000000000000000000000z", time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)},
	}
}

// asofFake — LIKE 접두어·date <=·첫 커밋 3종 질의를 흉내 내는 queryFn.
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

// asofStore — 기본 fake, SUPERLITE_REAL=1이면 실구현 위임 store.
func asofStore(t *testing.T, f *fakeStore) Store {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") == "1" {
		real, err := newRealStore(context.Background(), dsnFromEnv())
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		t.Cleanup(real.Close)
		return &delegatingFake{fake: f, real: real}
	}
	return f
}

// TestRelativeDh — Review Focus: test_relative_dh(72h·7d·무효 2.5h·"2 days ago").
func TestRelativeDh(t *testing.T) {
	now := time.Now()
	cases := []struct {
		spec string
		want time.Duration // now 기준 과거 오프셋
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

// TestPrefixUniqueAmbiguousZero — Review Focus: 접두어 1건 확정·모호·0건·
// LIKE 파라미터화(주입 방지).
func TestPrefixUniqueAmbiguousZero(t *testing.T) {
	f := asofFake(asofLogRows())
	st := asofStore(t, f)
	ctx := context.Background()

	// 1건 확정 — 접두어 "abc12" → 전체 해시 반환.
	hash, err := ParseAsOfSpec(ctx, st, "abc12")
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("unique prefix → (%q,%v), want 전체 해시", hash, err)
	}
	// LIKE 파라미터화 — SQL은 $1 플레이스홀더, 접두어는 인자로.
	if len(f.queries) == 0 || !strings.Contains(f.queries[len(f.queries)-1], "LIKE $1||'%'") {
		t.Fatalf("SQL=%v, want LIKE $1||'%%' 파라미터화", f.queries)
	}

	// base32계 해시 회귀(실측 결함 폐쇄) — 소문자 영숫자 접두어 "he0" 수용.
	hash, err = ParseAsOfSpec(ctx, st, "he0")
	if err != nil || hash != "he08irluq000000000000000000000z" {
		t.Fatalf("base32 prefix he0 → (%q,%v), want 전체 해시", hash, err)
	}

	// 모호 — "abc"가 2건과 일치 → err.
	if _, err := ParseAsOfSpec(ctx, st, "abc"); !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("ambiguous err=%v, want ErrInvalidAsOf", err)
	}
	// 0건 — "abcd" 접두어 매칭 없음 → err.
	if _, err := ParseAsOfSpec(ctx, st, "abcd"); !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("zero-match err=%v, want ErrInvalidAsOf", err)
	}
	// 3자 미만·대문자·특수문자 — 질의 없이 즉시 err.
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

// TestIsoBeforeFirst — Review Focus: 첫 커밋 이전 ISO 시각 → err, 문구에
// 첫 커밋 시각 포함. 정상 ISO는 시각 직전 최신 커밋 채택.
func TestIsoBeforeFirst(t *testing.T) {
	f := asofFake(asofLogRows())
	st := asofStore(t, f)
	ctx := context.Background()

	// 정상 — t3와 t2 사이 시각 → 최신 커밋(abc12f…) 채택.
	spec := "2026-10-24T13:00"
	hash, err := ParseAsOfSpec(ctx, st, spec)
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("ISO %q → (%q,%v), want 최신 커밋", spec, hash, err)
	}
	// date 비교 인자는 Go 계산 time.Time(v0.6 원칙 — SQL interval 금지).
	lastSQL := f.queries[0]
	if !strings.Contains(lastSQL, "date <= $1") {
		t.Fatalf("date 비교 SQL=%q, want 파라미터화", lastSQL)
	}

	// RFC3339 형식도 수용.
	hash, err = ParseAsOfSpec(ctx, st, "2026-10-21T00:00:00Z")
	if err != nil || hash != "he08irluq000000000000000000000z" {
		t.Fatalf("RFC3339 → (%q,%v), want 첫 커밋 해시", hash, err)
	}

	// 첫 커밋(2026-10-20 09:00) 이전 → ErrInvalidAsOf + 문구에 첫 커밋 시각.
	_, err = ParseAsOfSpec(ctx, st, "2026-10-01T00:00")
	if !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("before-first err=%v, want ErrInvalidAsOf", err)
	}
	if !strings.Contains(err.Error(), "2026-10-20 09:00:00") {
		t.Fatalf("문구에 첫 커밋 시각 없음: %v", err)
	}
}

// TestAsOfSpecNaturalLanguageRejected — 자연어·빈 스펙은 ErrInvalidAsOf(§1 배제).
// 공백 포함·빈 스펙은 질의 없이 즉시 err. 순소문자 단어("yesterday")는 완화
// 알파벳상 접두어 후보라 질의 후 0건 매칭으로 같은 err(정상 경로).
func TestAsOfSpecNaturalLanguageRejected(t *testing.T) {
	f := asofFake(asofLogRows())
	st := asofStore(t, f)
	for _, bad := range []string{"2 days ago", "yesterday", "  "} {
		if _, err := ParseAsOfSpec(context.Background(), st, bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("%q err=%v, want ErrInvalidAsOf", bad, err)
		}
	}
	// "2 days ago"(공백) 1건만 질의 — "yesterday" 1건은 접두어 경로 정상 질의.
	if len(f.queries) != 1 {
		t.Fatalf("질의 수=%d, want 1(yesterday 접두어 질의만): %v", len(f.queries), f.queries)
	}
}

// TestParseAsOfSpecRealPrefix — 실DB 회귀(FLINT_DSN 설정 시에만): write 1건
// 커밋 후 실제 해시(base32계) 접두어 3자로 ParseAsOfSpec이 전체 해시를
// 되돌리는지 실측. 소문자 영숫자 완화 검증의 엔드투엔드 보증.
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
	// 변경 없는 커밋 방지 — 고유 content_hash 1건 적립 후 커밋.
	uniq := fmt.Sprintf("asof-t81-%d", time.Now().UnixNano())
	recs := []map[string]any{{
		"kind": "knowledge", "topic": uniq, "summary": "as-of 회귀",
		"tags": ":asof:", "confidence": 0.9, "content_hash": uniq,
		"status": "pending", "ingested_at": time.Now(),
	}}
	if _, err := real.InsertBatch(ctx, recs); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	full, err := real.Commit(ctx, "asof prefix 회귀 [session:T81]")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(full) < asofMinPrefix {
		t.Fatalf("커밋 해시가 너무 짧음: %q", full)
	}
	got, err := ParseAsOfSpec(ctx, real, full[:3])
	if err != nil {
		t.Fatalf("ParseAsOfSpec(%q) err=%v (base32 해시 완화 확인)", full[:3], err)
	}
	if got != full {
		t.Fatalf("접두어 정규화=%q, want 전체 해시 %q", got, full)
	}
}
