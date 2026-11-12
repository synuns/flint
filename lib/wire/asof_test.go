// asof_test.go — T4 wire 이동 동반 테스트(T81 원본 승계). as-of 정규화
// (상대 Nh/Nd·ISO·해시 접두어 → 커밋 해시)를 wire 패키지 직접 표면로
// 검증한다. dolt_log 접근은 queryFn fake으로 세운다(실DB 회귀는
// cli/asof_test.go TestParseAsOfSpecRealPrefix가 담당 — realStore 인프라
// 잔류). 픽스처·문구 단정은 cli 원본과 동일 값.
package wire

import (
	"context"
	"errors"
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

// asofQueryFn — LIKE 접두어·date <=·첫 커밋 3종 질의를 흉내 내는 fake.
type asofQueryFn struct {
	queries []string
	queryFn func(sql string, args []any) ([][]any, error)
}

func (f *asofQueryFn) Migrate(ctx context.Context) error { return nil }
func (f *asofQueryFn) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (f *asofQueryFn) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (f *asofQueryFn) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (f *asofQueryFn) Commit(ctx context.Context, msg string) (string, error) {
	return "abc123", nil
}
func (f *asofQueryFn) Ping(ctx context.Context) error { return nil }
func (f *asofQueryFn) Close()                         {}
func (f *asofQueryFn) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	return f.queryFn(sql, args)
}

// newAsofFake — 픽스처 행으로 3종 질의를 응답하는 fake Store.
func newAsofFake(rows [][]any) *asofQueryFn {
	return &asofQueryFn{queryFn: func(sql string, args []any) ([][]any, error) {
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
	}}
}

// TestWireRelativeDh — 상대 Nh/Nd 확장·무효 형식(2.5h·"2 days ago") 배제.
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

// TestWirePrefixUniqueAmbiguousZero — 접두어 1건 확정·모호·0건·LIKE 파라미터화.
func TestWirePrefixUniqueAmbiguousZero(t *testing.T) {
	f := newAsofFake(asofLogRows())
	ctx := context.Background()

	hash, err := ParseAsOfSpec(ctx, f, "abc12")
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("unique prefix → (%q,%v), want 전체 해시", hash, err)
	}
	if len(f.queries) == 0 || !strings.Contains(f.queries[len(f.queries)-1], "LIKE $1||'%'") {
		t.Fatalf("SQL=%v, want LIKE $1||'%%' 파라미터화", f.queries)
	}

	// base32계 해시 회귀 — 소문자 영숫자 접두어 "he0" 수용.
	hash, err = ParseAsOfSpec(ctx, f, "he0")
	if err != nil || hash != "he08irluq000000000000000000000z" {
		t.Fatalf("base32 prefix he0 → (%q,%v), want 전체 해시", hash, err)
	}

	if _, err := ParseAsOfSpec(ctx, f, "abc"); !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("ambiguous err=%v, want ErrInvalidAsOf", err)
	}
	if _, err := ParseAsOfSpec(ctx, f, "abcd"); !errors.Is(err, ErrInvalidAsOf) {
		t.Fatalf("zero-match err=%v, want ErrInvalidAsOf", err)
	}
	// 3자 미만·대문자·특수문자 — 질의 없이 즉시 err.
	f2 := newAsofFake(asofLogRows())
	for _, bad := range []string{"ab", "ABC12", "abc12!", "zzz!"} {
		if _, err := ParseAsOfSpec(ctx, f2, bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("%q err=%v, want ErrInvalidAsOf", bad, err)
		}
	}
	if len(f2.queries) != 0 {
		t.Fatalf("무효 스펙인데 질의함: %v", f2.queries)
	}
}

// TestWireIsoBeforeFirst — 첫 커밋 이전 ISO 시각 → err(문구에 첫 커밋 시각).
func TestWireIsoBeforeFirst(t *testing.T) {
	f := newAsofFake(asofLogRows())
	ctx := context.Background()

	spec := "2026-10-24T13:00"
	hash, err := ParseAsOfSpec(ctx, f, spec)
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("ISO %q → (%q,%v), want 최신 커밋", spec, hash, err)
	}
	if !strings.Contains(f.queries[0], "date <= $1") {
		t.Fatalf("date 비교 SQL=%q, want 파라미터화", f.queries[0])
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

// TestWireAsOfSpecNaturalLanguageRejected — 자연어·빈 스펙은 ErrInvalidAsOf.
func TestWireAsOfSpecNaturalLanguageRejected(t *testing.T) {
	f := newAsofFake(asofLogRows())
	for _, bad := range []string{"2 days ago", "yesterday", "  "} {
		if _, err := ParseAsOfSpec(context.Background(), f, bad); !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("%q err=%v, want ErrInvalidAsOf", bad, err)
		}
	}
	if len(f.queries) != 1 {
		t.Fatalf("질의 수=%d, want 1(yesterday 접두어 질의만): %v", len(f.queries), f.queries)
	}
}
