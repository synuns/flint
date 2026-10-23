// asof_test.go — T81 as-of 정규화: 상대 Nh/Nd·ISO·해시 접두어 → 커밋 해시.
// dolt_log 접근은 fake Store(queryFn)으로 세우고 SUPERLITE_REAL=1이면
// 실구현 위임(delegatingFake — 질의 픽스처는 fake 우선).
package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// asofLogRows — dolt_log 픽스처(인덱스 0이 commit_order DESC 최신).
// 첫 두 해시는 "abc" 접두어 공유(모호성 케이스).
func asofLogRows() [][]any {
	return [][]any{
		{"abc12f00000000000000000000000000", time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)},
		{"abc34f00000000000000000000000000", time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)},
		{"fff99900000000000000000000000000", time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)},
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

	// 모호 — "abc"가 2건과 일치 → err.
	if _, err := ParseAsOfSpec(ctx, st, "abc"); !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("ambiguous err=%v, want ErrInvalidAsOf", err)
	}
	// 0건 — "abcd" 접두어 매칭 없음 → err.
	if _, err := ParseAsOfSpec(ctx, st, "abcd"); !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("zero-match err=%v, want ErrInvalidAsOf", err)
	}
	// 3자 미만·비16진 — 질의 없이 즉시 err.
	f2 := asofFake(asofLogRows())
	st2 := asofStore(t, f2)
	for _, bad := range []string{"ab", "abc12g", "ABC12", "zzz!"} {
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
	if err != nil || hash != "fff99900000000000000000000000000" {
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

// TestAsOfSpecNaturalLanguageRejected — 자연어·빈 스펙은 질의 없이 err(§1 배제).
func TestAsOfSpecNaturalLanguageRejected(t *testing.T) {
	f := asofFake(asofLogRows())
	st := asofStore(t, f)
	for _, bad := range []string{"2 days ago", "yesterday", "  "} {
		if _, err := ParseAsOfSpec(context.Background(), st, bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("%q err=%v, want ErrInvalidAsOf", bad, err)
		}
	}
	if len(f.queries) != 0 {
		t.Fatalf("무효 스펙인데 질의함: %v", f.queries)
	}
}
