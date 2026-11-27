package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
	"strings"
	"testing"
	"time"
)

func TestReadStatusTrailingExits3(t *testing.T) {
	for _, args := range [][]string{
		{"--status"},
		{"--as-of", "7d", "--status"},
	} {
		var out bytes.Buffer
		f := &fakeStore{queryFn: statusQueryFn()}
		if code := runRead(context.Background(), asofFakeOrReal(t, f), args, strings.NewReader(""), &out); code != 3 {
			t.Fatalf("args %v exit = %d, want 3 (out %q)", args, code, out.String())
		}
		if !strings.Contains(out.String(), `"field":"status"`) {
			t.Errorf("args %v status 스키마 에러 미출력: %q", args, out.String())
		}
		for _, q := range f.queries {
			t.Errorf("args %v 검증 전 질의 발행: %q", args, q)
		}
	}
}

func TestReadStatusInvalidWithAsOf(t *testing.T) {
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{queryFn: statusQueryFn()}}
	if code := runRead(context.Background(), readAsofStoreOrReal(t, f),
		[]string{"--as-of", "7d", "--status", "bogus"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("exit = %d, want 3 (out %q)", code, out.String())
	}
	if !strings.Contains(out.String(), `"field":"status"`) {
		t.Errorf("status 스키마 에러 미출력: %q", out.String())
	}
	for _, q := range f.queries {
		t.Errorf("검증 전 질의 발행: %q", q)
	}
}

func TestReadDashDashTreatsRestAsQuery(t *testing.T) {
	var gotArgs [][]any
	fn := func(sql string, args []any) ([][]any, error) {
		if readQueryMatch(sql) {
			gotArgs = append(gotArgs, args)
		}
		return [][]any{{float64(1), "topic", "summary text"}}, nil
	}
	f := &fakeStore{queryFn: fn}
	var out bytes.Buffer
	if code := runRead(context.Background(), f, []string{"--", "--json"},
		strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d, want 0 (out %q)", code, out.String())
	}

	if len(gotArgs) == 0 || len(gotArgs[0]) < 2 ||
		!strings.Contains(fmt.Sprint(gotArgs[0][0]), "--json") {
		t.Fatalf("검색어 인자 = %v, want LIKE 인자에 \"--json\" — 플래그로 파싱됨", gotArgs)
	}
	if strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Errorf("출력이 JSON 모드로 바뀜: %q — 모드 불변이어야 한다", out.String())
	}
	if !strings.Contains(out.String(), "1|topic|summary text") {
		t.Errorf("기본 텍스트 행 출력 없음: %q", out.String())
	}
}

func TestReadDashDashKeepsLeadingFlags(t *testing.T) {
	fn := func(sql string, args []any) ([][]any, error) {
		return [][]any{{float64(1), "t", "s"}}, nil
	}
	f := &fakeStore{queryFn: fn}
	var out bytes.Buffer
	if code := runRead(context.Background(), f, []string{"--json", "--", "--json"},
		strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d, want 0 (out %q)", code, out.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Errorf("선행 --json 무시됨: %q — 모드 불변이어야 한다", out.String())
	}
}

func TestReadTrailingFilterFlagsExit3(t *testing.T) {
	for _, flag := range []string{"--tag", "--entity", "--domain", "--fields"} {
		var out bytes.Buffer
		f := &fakeStore{}
		if code := runRead(context.Background(), f, []string{flag},
			strings.NewReader(""), &out); code != 3 {
			t.Fatalf("%s exit = %d, want 3 (out %q)", flag, code, out.String())
		}
		field := strings.TrimPrefix(flag, "--")
		if !strings.Contains(out.String(), `"field":"`+field+`"`) {
			t.Errorf("%s 스키마 에러 미출력: %q", flag, out.String())
		}
		if len(f.queries) != 0 {
			t.Errorf("%s 검증 전 질의 발행: %q", flag, f.queries[0])
		}
	}
}

func statusQueryFn() func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return [][]any{
				fallbackRow(35, "꿀송이방 안내문", "확정"),
				fallbackRow(41, "엔츠 과제", "진행 중"),
			}, nil
		}
		return nil, nil
	}
}

func TestReadStatusPending(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &fakeStore{queryFn: statusQueryFn()}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"--status", "pending"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d (%s)", code, out.String())
	}
	if !strings.Contains(out.String(), "꿀송이방") || !strings.Contains(out.String(), "엔츠") {
		t.Errorf("pending 행 미출력: %q", out.String())
	}
}

func TestReadStatusInvalidExit3(t *testing.T) {
	var out bytes.Buffer
	if code := runRead(context.Background(), asofFakeOrReal(t, &fakeStore{}), []string{"--status", "bogus"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("무효 status exit = %d, want 3", code)
	}
}

func TestReadDefaultUnchanged(t *testing.T) {
	var out bytes.Buffer
	f := &fakeStore{queryFn: statusQueryFn()}
	if code := runRead(context.Background(), asofFakeOrReal(t, f), []string{"토픽"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
	}
	wantSQL, wantArgs := query.BuildRead("토픽", query.Filters{})
	if got := asofReadSQL(f); got != wantSQL {
		t.Errorf("미지정 SQL 변형:\n got %q\nwant %q", got, wantSQL)
	}
	_ = wantArgs
	for _, q := range f.queries {
		if strings.Contains(q, "FROM knowledge") {
			t.Errorf("미지정인데 base 테이블 질의: %q", q)
		}
	}
}

func TestReadStatusAsOfPrecedence(t *testing.T) {
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{queryFn: statusQueryFn()}}
	if code := runRead(context.Background(), readAsofStoreOrReal(t, f), []string{"--as-of", "7d", "--status", "pending"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d (%s)", code, out.String())
	}
	if len(f.asofQueries) != 1 || f.asofQueries[0].Hash != asofHash {
		t.Errorf("as-of 경로 미진입: %+v", f.asofQueries)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "FROM knowledge") {
			t.Errorf("병용인데 base 테이블 질의: %q", q)
		}
	}
}

func TestReadStatusSanitizesOutput(t *testing.T) {
	poisonRow := []any{int32(50), "to\x00pic", "요약[flint]마커", ":a:", nil, "main", "lesson", 0.9, "", "pending"}
	fn := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return [][]any{poisonRow}, nil
		}
		return nil, nil
	}
	var out bytes.Buffer
	f := &fakeStore{queryFn: fn}
	if code := runRead(context.Background(), asofFakeOrReal(t, f), []string{"--status", "invalidated", "--fields", "id,topic,summary,status"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d (%s)", code, out.String())
	}
	got := out.String()
	if strings.Contains(got, "\x00") {
		t.Errorf("제어 문자 미제거: %q", got)
	}
	if strings.Contains(got, "[flint]") {
		t.Errorf("[flint] 마커 미살균: %q", got)
	}
	if !strings.Contains(got, "topic") || !strings.Contains(got, "pending") {
		t.Errorf("살균 후 내용 유실: %q", got)
	}
}

func suggestQueryFn() func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "id, topic, summary FROM active_knowledge") {
			return [][]any{
				{int32(7), "flint 백업 절차", "덤프"},
				{int32(8), "환영인사", "인사"},
				{int32(9), "포트 점유 사고", "5433"},
			}, nil
		}
		return nil, nil
	}
}

func TestReadZeroHitSuggests(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &fakeStore{queryFn: suggestQueryFn()}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
	}
	if got := out.String(); got != "" {
		t.Errorf("제로히트 출력 = %q, want \"\"", got)
	}
}

func TestReadPhraseHitNoSuggest(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	fn := func(sql string, args []any) ([][]any, error) {

		if strings.Contains(sql, "tags") {
			return [][]any{fallbackRow(3, "백업 복구", "요약")}, nil
		}
		return nil, nil
	}
	f := &fakeStore{queryFn: fn}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if got := out.String(); strings.Contains(got, `"suggestions"`) {
		t.Errorf("구문 히트인데 suggestions 출력: %q", got)
	}
}

func mustSuggestStore(scan [][]any) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "id, topic, summary FROM active_knowledge") {
			return scan, nil
		}
		return nil, nil
	}}
}

func nearCountingStore(metaInserts *[]string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "id, topic, summary FROM active_knowledge"):
			return [][]any{{int32(7), "flint 백업 절차", "덤프"}}, nil
		case strings.Contains(sql, "INSERT INTO meta"):
			if len(args) > 1 {
				*metaInserts = append(*metaInserts, fmt.Sprint(args[0])+"="+fmt.Sprint(args[1]))
			}
			return nil, nil
		default:
			return nil, nil
		}
	}}
}

func TestReadZeroHitSilent(t *testing.T) {
	cases := []struct {
		name string
		scan [][]any
	}{
		{"scan-hit", [][]any{{int32(7), "flint 백업 절차", "덤프"}}},
		{"scan-empty", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var out bytes.Buffer
			f := mustSuggestStore(tc.scan)
			if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
			}
			if got := out.String(); got != "" {
				t.Errorf("제로히트 출력 = %q, want \"\"", got)
			}
		})
	}
}

func TestReadZeroHitNearCounted(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	var inserts []string
	f := nearCountingStore(&inserts)
	st := &readClassRecorder{fakeStore: f}
	if code := runRead(ctx, st, []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
	}
	if len(st.events) != 1 || !st.events[0].Zero || !st.events[0].Near {
		t.Fatalf("read_events = %+v, want 1행 {Zero:true Near:true}", st.events)
	}
	for _, kv := range inserts {
		if strings.HasPrefix(kv, "read_stats=") {
			t.Errorf("read_stats 쓰기 잔존(폐지): %s", kv)
		}
	}
}

type readClassRecorder struct {
	*fakeStore
	events []db.ReadEvent
	usages int
}

func (r *readClassRecorder) RecordReadUsage(ctx context.Context, zero, near bool) error {
	r.usages++
	return nil
}

func (r *readClassRecorder) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	r.events = append(r.events, ev)
	return nil
}

func TestReadInstrumentationQueryBudget(t *testing.T) {
	count := 0
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		count++
		switch {
		case readQueryMatch(sql):
			return [][]any{{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"}}, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := runRead(context.Background(), st, []string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if count > 6 {
		t.Errorf("쿼리 왕복 %d회, 본 쿼리 포함 ≤ 6 초과", count)
	}
}

func TestReadClassifiesZeroNear(t *testing.T) {
	t.Run("zero-near", func(t *testing.T) {
		var inserts []string
		st := &readClassRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "id, topic, summary FROM active_knowledge"):
				return [][]any{{int32(7), "flint 백업 절차", "덤프"}}, nil
			case strings.Contains(sql, "INSERT INTO meta"):
				if len(args) > 1 {
					inserts = append(inserts, args[0].(string)+"="+args[1].(string))
				}
				return nil, nil
			}
			return nil, nil
		}}}
		var out strings.Builder
		if code := runRead(context.Background(), st, []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
		}
		if len(st.events) != 1 || !st.events[0].Zero || !st.events[0].Near {
			t.Errorf("read_events = %+v, want 1행 {Zero:true Near:true}", st.events)
		}
		if st.usages != 0 {
			t.Errorf("RecordReadUsage %d회, want 0(read kind daily 쓰기 폐지)", st.usages)
		}
		for _, kv := range inserts {
			if strings.HasPrefix(kv, "read_stats=") {
				t.Errorf("read_stats 쓰기 잔존(폐지): %s", kv)
			}
		}
	})
	t.Run("hit", func(t *testing.T) {
		st := &readClassRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if readQueryMatch(sql) {
				return [][]any{{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"}}, nil
			}
			return nil, nil
		}}}
		var out strings.Builder
		if code := runRead(context.Background(), st, []string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit %d: %s", code, out.String())
		}
		if len(st.events) != 1 || st.events[0].Zero || st.events[0].Near {
			t.Errorf("read_events = %+v, want 1행 {Zero:false Near:false}", st.events)
		}
		if st.usages != 0 {
			t.Errorf("RecordReadUsage %d회, want 0", st.usages)
		}
	})
}

func TestReadSuggestScanCapOverride(t *testing.T) {
	t.Cleanup(func() {
		def := store.DefaultThresholds()
		query.SetSuggestThresholds(def.SuggestMinScore, def.SuggestScanCap)
	})
	ctx := context.Background()
	var out bytes.Buffer
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "id, topic, summary FROM active_knowledge") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM meta WHERE key IN") {
			return [][]any{{"suggest_scan_cap", "42"}}, nil
		}
		return nil, nil
	}}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("제로히트 exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
	}
	scanned := false
	for _, q := range f.queries {
		if strings.Contains(q, "id, topic, summary FROM active_knowledge") {
			scanned = true
			if !strings.Contains(q, "LIMIT 42") {
				t.Errorf("제안 스캔 상한 미반영: %q, want LIMIT 42(기본 5000 잔존 의심)", q)
			}
		}
	}
	if !scanned {
		t.Error("제안 스캔 질의 미발생")
	}
}

type readEventRecorder struct {
	*fakeStore
	events []readEventRecord
}

type readEventRecord struct {
	hits     int
	zero     bool
	near     bool
	fallback bool
	session  string
	ids      string
}

func (r *readEventRecorder) RecordReadUsage(ctx context.Context, zero, near bool) error {
	return nil
}

func (r *readEventRecorder) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	r.events = append(r.events, readEventRecord{ev.Hits, ev.Zero, ev.Near, ev.Fallback, ev.Session, ev.IDs})
	return nil
}

var hitRowsFixture = [][]any{
	{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"},
	{"2", "백업 절차", "매일", ":b:", "", "", "0.9", "active", "0"},
}

func TestReadEventRecordedOnHit(t *testing.T) {
	st := &readEventRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case readQueryMatch(sql):
			return hitRowsFixture, nil
		case strings.Contains(sql, "SELECT value FROM meta"):
			return [][]any{{`{"calls":5,"zero":2}`}}, nil
		}
		return nil, nil
	}}}
	var out strings.Builder
	code := runRead(context.Background(), st,
		[]string{"--session", "s-42", "백업 정책"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if len(st.events) != 1 {
		t.Fatalf("이벤트 %d건, want 1", len(st.events))
	}
	ev := st.events[0]
	if ev.hits != 2 || ev.zero || ev.near || ev.fallback {
		t.Errorf("이벤트 = {%d %v %v %v}, want {2 false false false}", ev.hits, ev.zero, ev.near, ev.fallback)
	}
	if ev.session != "s-42" {
		t.Errorf("session = %q, want s-42", ev.session)
	}

	if ev.ids != "1,2" {
		t.Errorf("ids = %q, want 1,2", ev.ids)
	}
}

func TestReadEventRecordsFallback(t *testing.T) {

	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &readEventRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, " OR "):

			return [][]any{{"3", "백업 상태", "매일 점검", ":b:", "", "", "0.9", "active", "0"}}, nil
		case strings.Contains(sql, "SELECT value FROM meta"):
			return [][]any{{`{"calls":5,"zero":2}`}}, nil
		}
		return nil, nil
	}}}
	var out strings.Builder

	code := runRead(context.Background(), st,
		[]string{"백업 정책 상태"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if len(st.events) != 1 {
		t.Fatalf("이벤트 %d건, want 1", len(st.events))
	}
	ev := st.events[0]
	if ev.hits != 1 || ev.zero || ev.near || !ev.fallback {
		t.Errorf("이벤트 = {%d %v %v %v}, want {1 false false true}", ev.hits, ev.zero, ev.near, ev.fallback)
	}
	if ev.session != "" {
		t.Errorf("session = %q, want 빈 라벨(미지정)", ev.session)
	}
}

func TestReadSessionFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--session"},
		{"--session", "--json"},
		{"--session", "-q"},
	} {
		st := &fakeStore{}
		var out strings.Builder
		code := runRead(context.Background(), st, args, strings.NewReader(""), &out)
		if code != 3 {
			t.Fatalf("args=%v exit = %d, want 3: %s", args, code, out.String())
		}
		if !strings.Contains(out.String(), `"session"`) {
			t.Errorf("args=%v 오류 필드에 session 없음: %s", args, out.String())
		}
	}
}

func withFakeStore(t *testing.T, st Store) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return st, nil }
	t.Cleanup(func() { newStore = prev })
}

func readAsofStoreOrReal(t *testing.T, st Store) Store {
	t.Helper()
	return st
}

const asofHash = "he08irluqn3v2m1k9x7c4jw5z6b8d0f"

var asofCommitDate = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

type readAsofFake struct {
	*fakeStore
	asofQueries []wire.AsOfQuery
	zeroHit     bool
	asofErr     error
}

func (f *readAsofFake) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	switch {
	case q.Prefix != "":
		if strings.HasPrefix(asofHash, q.Prefix) {
			return []wire.LogEntry{{Hash: asofHash}}, nil
		}
		return nil, nil
	case !q.BeforeOrAt.IsZero():
		return []wire.LogEntry{{Hash: asofHash}}, nil
	case q.Hash == asofHash:
		return []wire.LogEntry{{Hash: asofHash, Date: asofCommitDate}}, nil
	}
	return nil, nil
}

func (f *readAsofFake) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	f.asofQueries = append(f.asofQueries, q)
	if f.asofErr != nil {
		return nil, f.asofErr
	}
	if f.zeroHit {
		return nil, nil
	}
	return [][]any{{int32(3), "토픽", "요약", ":a:", nil, "main", 0.9, "active"}}, nil
}

func asofFakeOrReal(t *testing.T, f *fakeStore) Store {
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

func asofReadSQL(f *fakeStore) string {
	for _, q := range f.queries {
		if readQueryMatch(q) {
			return q
		}
	}
	return ""
}

func TestReadAsOfAddsField(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	if code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "7d", "토픽"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d", code)
	}
	if len(f.asofQueries) != 1 {
		t.Fatalf("과거 스냅샷 질의 %d건, want 1", len(f.asofQueries))
	}
	aq := f.asofQueries[0]
	if aq.Hash != asofHash || aq.Table != "recall_knowledge" {
		t.Errorf("해시 접합 실패(v1.38.0 recall 뷰): %+v", aq)
	}

	if !strings.Contains(aq.Where, "ORDER BY (lower(topic)=lower($2)) DESC") {
		t.Errorf("RankOrder 절 변형: %q", aq.Where)
	}
	if aq.Limit <= 0 {
		t.Errorf("LIMIT 유실: %d", aq.Limit)
	}

	got := out.String()
	if !strings.HasPrefix(got, "3|토픽|요약\n") {
		t.Errorf("기존 행 출력 변형: %q", got)
	}
	if !strings.Contains(got, `"as_of":{"hash":"he08irluqn3v2m1k9x7c4jw5z6b8d0f","commit_date":"2026-10-20T12:00:00Z"}`) {
		t.Errorf("as_of 필드 누락/불일치: %q", got)
	}

	var out2 bytes.Buffer
	f2 := &fakeStore{}
	if code := runRead(ctx, asofFakeOrReal(t, f2), []string{"토픽"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("미지정 exit = %d", code)
	}
	if strings.Contains(out2.String(), "as_of") {
		t.Errorf("미지정인데 as_of 출력: %q", out2.String())
	}
}

func TestReadAsOfInvalidExit3(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "2 days ago", "토픽"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d", code)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"error":"invalid_as_of"`) {
		t.Errorf("한 줄 JSON invalid_as_of 여부: %q", out.String())
	}
	if len(f.asofQueries) != 0 {
		t.Errorf("무효 스펙인데 과거 스냅샷 질의 실행: %+v", f.asofQueries)
	}
}

func TestReadAsOfLimitRegression(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	if code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "7d", "토픽", "--limit", "3"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if len(f.asofQueries) != 1 {
		t.Fatalf("과거 스냅샷 질의 %d건, want 1", len(f.asofQueries))
	}
	aq := f.asofQueries[0]
	if aq.Hash != asofHash {
		t.Errorf("해시 접합 유실: %+v", aq)
	}

	if !strings.Contains(aq.Where, "LIKE lower($1)") || !strings.Contains(aq.Where, "lower($2)") {
		t.Errorf("q LIKE·랭킹 절 변형: %q", aq.Where)
	}
	if aq.Limit != 3 {
		t.Errorf("Limit=%d, want 3", aq.Limit)
	}
}

func TestReadAsOfMissingValueExit3(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d, want 3; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"error":"invalid_as_of"`) {
		t.Errorf("invalid_as_of 한 줄 JSON 아님: %q", out.String())
	}
	if len(f.asofQueries) != 0 {
		t.Errorf("무효 스펙인데 과거 스냅샷 질의 실행: %+v", f.asofQueries)
	}
}

func TestReadAsOfViewColumnHint(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	f.asofErr = errors.New(`view "active_knowledge" incompatible at this commit (column "confidence" does not exist) — retry with base table "knowledge"`)
	code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "7d"}, strings.NewReader(""), &out)
	if code == 0 {
		t.Fatalf("오류 주입인데 exit 0")
	}
	if !strings.Contains(out.String(), "active_knowledge") || !strings.Contains(out.String(), "knowledge") {
		t.Errorf("기본 테이블 안내 문구 누락: %q", out.String())
	}
}

func fallbackRow(id int, topic, summary string) []any {
	return []any{int32(id), topic, summary, ":a:", nil, "main", 0.9, "active"}
}

func fallbackQueryFn(metaKeys *[]string) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case readQueryMatch(sql):
			if strings.Contains(sql, " OR ") {
				return [][]any{
					fallbackRow(1, "백업 절차", "정기 절차"),
					fallbackRow(2, "flint 백업", "복구는 저장소 정지 조건"),
				}, nil
			}
			return nil, nil
		case strings.Contains(sql, "SELECT value FROM meta"):
			if len(args) > 0 {
				*metaKeys = append(*metaKeys, "get:"+argKey(args[0]))
			}
			return nil, nil
		case strings.Contains(sql, "INSERT INTO meta"):
			if len(args) > 0 {
				*metaKeys = append(*metaKeys, "set:"+argKey(args[0]))
			}
			return nil, nil
		}
		return nil, nil
	}
}

func argKey(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestReadFallbackOnZeroPhrase(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	var metaKeys []string
	f := &fakeStore{queryFn: fallbackQueryFn(&metaKeys)}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0 (출력 %q)", code, out.String())
	}

	var orIssued bool
	for _, q := range f.queries {
		if readQueryMatch(q) && strings.Contains(q, " OR ") {
			orIssued = true
		}
	}
	if !orIssued {
		t.Errorf("폴백 OR 질의 미발행: %v", f.queries)
	}

	got := out.String()
	if !strings.HasPrefix(got, "2|flint 백업|복구는 저장소 정지 조건\n1|백업 절차|정기 절차\n") {
		t.Errorf("정합수 내림 재랭킹 실패: %q", got)
	}

	var bumped bool
	for _, k := range metaKeys {
		if k == "set:read_fallback_calls" {
			bumped = true
		}
	}
	if !bumped {
		t.Errorf("read_fallback_calls 증분 없음: %v", metaKeys)
	}
}

func TestReadPhraseHitUnchanged(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	fn := func(sql string, args []any) ([][]any, error) {
		switch {
		case readQueryMatch(sql):
			return [][]any{fallbackRow(3, "백업 복구 절차", "요약")}, nil
		}
		return nil, nil
	}
	f := &fakeStore{queryFn: fn}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0", code)
	}
	if got := out.String(); got != "3|백업 복구 절차|요약\n" {
		t.Errorf("구문 히트 출력 변형: %q", got)
	}
	for _, q := range f.queries {
		if readQueryMatch(q) && strings.Contains(q, " OR ") {
			t.Errorf("구문 히트인데 폴백 OR 질의 발행: %q", q)
		}
	}
}

func TestReadFallbackSkippedOnAsOf(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}, zeroHit: true}
	if code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "7d", "백업 복구"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0 (출력 %q)", code, out.String())
	}
	for _, q := range f.queries {
		if readQueryMatch(q) && strings.Contains(q, " OR ") {
			t.Errorf("as-of 질의에 폴백 OR 발행: %q", q)
		}
	}
	if got := out.String(); !strings.Contains(got, `"as_of"`) {
		t.Errorf("as_of 첨부 누락: %q", got)
	}
}

func TestReadFallbackSkippedOnSingleToken(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	var metaKeys []string
	f := &fakeStore{queryFn: fallbackQueryFn(&metaKeys)}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
	}
	if got := out.String(); got != "" {
		t.Errorf("1-토큰 제로히트 출력 있음: %q", got)
	}
	for _, q := range f.queries {
		if readQueryMatch(q) && strings.Contains(q, " OR ") {
			t.Errorf("1-토큰 질의에 폴백 OR 발행: %q", q)
		}
	}
	for _, k := range metaKeys {
		if k == "set:read_fallback_calls" {
			t.Errorf("폴백 미시도인데 read_fallback_calls 증분")
		}
	}
}

func readQueryMatch(s string) bool {
	return strings.Contains(s, "FROM recall_knowledge") ||
		strings.Contains(s, "FROM active_knowledge")
}
