// asof_test.go — as-of 정규화
// (상대 Nh/Nd·ISO·해시 접두어 → 커밋 해시)를 wire 패키지 직접 표면로
// 검증한다. dolt_log 접근은 LogEntries fake으로 세운다(실DB 회귀는
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

// asofLogEntries — LogEntry 픽스처(인덱스 0이 최신). 첫 두 해시는 "abc"
// 접두어 공유(모호성 케이스), 셋째는 base32계(Doltgres 실측 — [0-9a-z]
// 전범위) 해시로 완화 검증 회귀.
func asofLogEntries() []LogEntry {
	return []LogEntry{
		{Hash: "abc12f00000000000000000000000000", Date: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)},
		{Hash: "abc34f00000000000000000000000000", Date: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)},
		{Hash: "he08irluq000000000000000000000z", Date: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)},
	}
}

// asofFakeStore — 접두 일치·date≤ 최신 1건·첫 커밋 3종 LogQuery를
// 픽스처로 응답하는 fake Store. calls로 호출된 쿼리를 기록한다.
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

// LogEntries — Prefix(접두 일치 전체)·BeforeOrAt(date≤ 최신 1건)·
// Earliest(첫 커밋) 시맨틱를 픽스처(entries는 Date 내림차순 가정)로
// 흉내 낸다.
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

// TestWirePrefixUniqueAmbiguousZero — 접두어 1건 확정·모호·0건·LogQuery{Prefix} 사용.
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

	// base32계 해시 회귀 — 소문자 영숫자 접두어 "he0" 수용.
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
	// 3자 미만·대문자·특수문자 — 질의 없이 즉시 err.
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

// TestWireIsoBeforeFirst — 첫 커밋 이전 ISO 시각 → err(문구에 첫 커밋 시각).
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

// TestWireRelativeBoundary — 상대 스펙이 commitAtOrBefore(date≤)로
// 연결되는 경계. 커밋 시각과 정확히 같은 t는 채택(≤), 커밋 없는 저장소는
// no-commits 문구.
func TestWireRelativeBoundary(t *testing.T) {
	ctx := context.Background()

	// now 기반이라 픽스처 전체가 과거 — 최신 커밋 확정.
	f := &asofFakeStore{entries: asofLogEntries()}
	hash, err := ParseAsOfSpec(ctx, f, "1h")
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("relative 1h → (%q,%v), want 최신 커밋", hash, err)
	}
	if len(f.calls) == 0 || f.calls[0].BeforeOrAt.IsZero() {
		t.Fatalf("calls=%+v, want LogQuery{BeforeOrAt}", f.calls)
	}

	// 경계 — ISO 시각이 최신 커밋 시각과 정확히 같으면 그 커밋 채택(≤).
	hash, err = ParseAsOfSpec(ctx, f, "2026-10-24T12:00:00Z")
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("ISO == 커밋 시각 → (%q,%v), want 경계 포함", hash, err)
	}

	// 커밋 없는 저장소 — date≤도 첫 커밋도 0건.
	empty := &asofFakeStore{}
	_, err = ParseAsOfSpec(ctx, empty, "1h")
	if !errors.Is(err, ErrInvalidAsOf) || !strings.Contains(err.Error(), "no commits") {
		t.Fatalf("빈 저장소 err=%v, want no-commits 문구", err)
	}
}

// TestWireAsOfSpecNaturalLanguageRejected — 자연어·빈 스펙은 ErrInvalidAsOf.
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
