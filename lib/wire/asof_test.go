package wire

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func asofLogEntries() []LogEntry {
	return []LogEntry{
		{Hash: "abc12f00000000000000000000000000", Date: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)},
		{Hash: "abc34f00000000000000000000000000", Date: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)},
		{Hash: "he08irluq000000000000000000000z", Date: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)},
	}
}

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
func (f *asofFakeStore) OpenWorkState(ctx context.Context, rec map[string]any) (int, string, error) {
	return 0, "", errors.New("not implemented")
}
func (f *asofFakeStore) LatestProjectForSession(ctx context.Context, session string) (int, error) {
	return 0, errors.New("not implemented")
}
func (f *asofFakeStore) ResolveWorkState(ctx context.Context, id int, byKnowledgeID int) error {
	return errors.New("not implemented")
}
func (f *asofFakeStore) ListOpenWorkState(ctx context.Context, projectID int, limit int) []map[string]any {
	return nil
}
func (f *asofFakeStore) StaleOpenWorkStates(ctx context.Context, days int) []map[string]any {
	return nil
}
func (f *asofFakeStore) QueryAsOf(ctx context.Context, q AsOfQuery) ([][]any, error) {
	if f.noSchema[q.Hash] {

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

	got, _ := ParseRelative("106751d")
	if want := time.Now().Add(-106751 * 24 * time.Hour); got.Sub(want) > 2*time.Second || got.Sub(want) < -2*time.Second {
		t.Fatalf("ParseRelative(106751d)=%v, want ≈%v(유한 최대 스팬)", got, want)
	}
}

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

func TestWireRelativeBoundary(t *testing.T) {
	ctx := context.Background()

	f := &asofFakeStore{entries: asofLogEntries()}
	hash, err := ParseAsOfSpec(ctx, f, "1h")
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("relative 1h → (%q,%v), want 최신 커밋", hash, err)
	}
	if len(f.calls) == 0 || f.calls[0].BeforeOrAt.IsZero() {
		t.Fatalf("calls=%+v, want LogQuery{BeforeOrAt}", f.calls)
	}

	hash, err = ParseAsOfSpec(ctx, f, "2026-10-24T12:00:00Z")
	if err != nil || hash != "abc12f00000000000000000000000000" {
		t.Fatalf("ISO == 커밋 시각 → (%q,%v), want 경계 포함", hash, err)
	}

	empty := &asofFakeStore{}
	_, err = ParseAsOfSpec(ctx, empty, "1h")
	if !errors.Is(err, ErrInvalidAsOf) || !strings.Contains(err.Error(), "no commits") {
		t.Fatalf("빈 저장소 err=%v, want no-commits 문구", err)
	}
}

func TestWirePreFlintCommitRejected(t *testing.T) {
	ctx := context.Background()

	now := time.Now()
	initNew := now.Add(-2 * time.Hour).Truncate(time.Second)
	initOld := initNew.Add(-time.Second)
	flintAt := now.Add(-time.Hour).Truncate(time.Second)
	manualAt := now.Add(-30 * time.Minute).Truncate(time.Second)

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
		"0h",
		initNew.Add(time.Minute).Format(time.RFC3339),
		"cr8",
		"in1",
		initNew.Format(time.RFC3339),
	} {
		_, err := ParseAsOfSpec(ctx, fresh, spec)
		if !errors.Is(err, ErrInvalidAsOf) {
			t.Fatalf("fresh %q err=%v, want ErrInvalidAsOf", spec, err)
		}
		if !strings.Contains(err.Error(), "pre-flint") {
			t.Fatalf("fresh %q 문구에 pre-flint 없음: %v", spec, err)
		}
	}

	const flintH = "kn0w0000000000000000000000000abc"
	const manualH = "man0ua100000000000000000000000cd"
	seeded := &asofFakeStore{
		entries: []LogEntry{
			{Hash: manualH, Message: "CREATE DATABASE", Date: manualAt},
			{Hash: flintH, Message: "knowledge: seed write [session:0]", Date: flintAt},
		},
		noSchema: fresh.noSchema,
	}
	seeded.entries = append(seeded.entries, fresh.entries...)
	rejected := "<rejected>"
	for spec, want := range map[string]string{
		"0h":                          manualH,
		"man":                         manualH,
		manualAt.Format(time.RFC3339): manualH,
		"kn0":                         flintH,
		flintAt.Format(time.RFC3339):  flintH,
		initNew.Add(30 * time.Minute).Format(time.RFC3339): rejected,
	} {
		hash, err := ParseAsOfSpec(ctx, seeded, spec)
		if want == rejected {

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
