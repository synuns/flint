// read_test.go — the read contract
// zero-hit exit 0·--session auto-label·as-of. Contract invariants: an
// eligible (present-point·stateless-filter) zero-hit is fully silent in
// text mode·--json is a one-line {"hits":0}, exit 0; without --session,
// the wire.ReadSessionLabel label is quietly adopted; --as-of appends a
// one-line as_of JSON after the results. The as-of spec parser is
// cli-asof.go-owned, so a double is injected through the ParseAsOfSpecFn
// seam (the real-implementation path belongs to the cli-residual
// read_v08_test.go under SUPERLITE_REAL=1).
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// readEventRecorder — a fakeStore with the ReadEventStore seam layered
// on (moved from the cli read_v0190_test.go convention). Also absorbs
// RecordReadUsage.
type readEventRecorder struct {
	*fakeStore
	events []readEventRecord
}

// readEventRecord — recorded RecordReadEvent call arguments.
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

// bytesRecorder — a fakeStore with the BytesRecorder seam layered on (G5
// byte metering). Records RecordReadEvent call arguments (package-shared
// — the hot·brief regressions use the same fake).
type bytesRecorder struct {
	*fakeStore
	recorded []bytesCall
}

// bytesCall — recorded RecordBytes call arguments.
type bytesCall struct {
	kind string
	n    int
}

func (b *bytesRecorder) RecordBytes(ctx context.Context, kind string, n int) error {
	b.recorded = append(b.recorded, bytesCall{kind, n})
	return nil
}

// TestRunReadRecordsBytes — right after the read output, records
// len(final output) with kind="read" — even a 0-byte zero-hit is
// recorded (call-count denominator consistency, spec §1).
func TestRunReadRecordsBytes(t *testing.T) {
	t.Run("히트 — 최종 출력 바이트", func(t *testing.T) {
		st := &bytesRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			switch {
			case strings.Contains(sql, "FROM active_knowledge"):
				return hitRowsFixture, nil
			}
			return nil, nil
		}}}
		var out strings.Builder
		if code := RunRead(context.Background(), st,
			[]string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		if len(st.recorded) != 1 || st.recorded[0].kind != "read" {
			t.Fatalf("RecordBytes 기록 = %v, want 1건(kind=read)", st.recorded)
		}
		if got := st.recorded[0].n; got != len(out.String()) {
			t.Errorf("read bytes = %d, want %d(len 최종 출력 — io.Writer 쓴 총량)", got, len(out.String()))
		}
	})
	t.Run("제로히트 — 0바이트도 기록", func(t *testing.T) {
		st := &bytesRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			return nil, nil // zero-hit — fallback·suggestion scan also all 0 rows
		}}}
		var out strings.Builder
		if code := RunRead(context.Background(), st,
			[]string{"없는토픽"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
		}
		if len(st.recorded) != 1 || st.recorded[0] != (bytesCall{"read", 0}) {
			t.Errorf("제로히트 RecordBytes = %v, want [{read 0}](0바이트 기록 — 분모 정합)", st.recorded)
		}
	})
}

// injectAsOfSeams — injects a fixed-hash double at the ParseAsOfSpecFn·
// ErrInvalidAsOf injection points (spec "7d" → one hash resolved).
// Restored via t.Cleanup.
func injectAsOfSeams(t *testing.T, hash string) {
	t.Helper()
	savedFn, savedErr := ParseAsOfSpecFn, ErrInvalidAsOf
	ParseAsOfSpecFn = func(ctx context.Context, st wire.Store, spec string) (string, error) {
		return hash, nil
	}
	ErrInvalidAsOf = errors.New("invalid as-of spec")
	t.Cleanup(func() { ParseAsOfSpecFn, ErrInvalidAsOf = savedFn, savedErr })
}

// hitRowsFixture — the active_knowledge rows of a hit read (9 columns,
// inheriting the cli convention).
var hitRowsFixture = [][]any{
	{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"},
	{"2", "백업 절차", "매일", ":b:", "", "", "0.9", "active", "0"},
}

// TestRunReadZeroHitExit0Silent — an eligible (present-point·stateless
// filter) zero-hit is fully silent in text (silence kept) with exit 0
// (previously 1) — a zero-hit is not an error, so the agent loop is not
// broken.
func TestRunReadZeroHitExit0Silent(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // zero-hit — fallback·suggestion scan also all 0 rows
	}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"없는토픽"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0(제로히트 exit 0)", code)
	}
	if out.String() != "" {
		t.Errorf("제로히트 텍스트 침묵 위반: %q", out.String())
	}
}

// TestRunReadZeroHitJSONHits — a --json zero-hit is a one-line
// hits:0 JSON ({"hits":0}) + exit 0 — the caller machine-reads the hit
// count. Unlike text mode, it is not silent.
func TestRunReadZeroHitJSONHits(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // zero-hit — fallback·suggestion scan also all 0 rows
	}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"--json", "없는토픽"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0(제로히트 exit 0)", code)
	}
	if got := strings.TrimSpace(out.String()); got != `{"hits":0}` {
		t.Errorf("--json 제로히트 출력 = %q, want {\"hits\":0}", got)
	}
}

// TestRunReadSessionAutoLabel — without --session, the current session
// label file (wire.ReadSessionLabel) is quietly adopted and recorded in
// read_events. The label file is set up in a temporary directory via the
// wire.ConfigDir seam.
func TestRunReadSessionAutoLabel(t *testing.T) {
	dir := t.TempDir()
	// The label source is the per-session file, the single
	// session-<id>.label path — the legacy session-current is no longer
	// read.
	if err := os.WriteFile(dir+"/session-demo1.label", []byte("auto-label-7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	savedDir := wire.ConfigDir
	wire.ConfigDir = func() string { return dir }
	t.Cleanup(func() { wire.ConfigDir = savedDir })

	st := &readEventRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			return hitRowsFixture, nil
		case strings.Contains(sql, "SELECT value FROM meta"):
			return [][]any{{`{"calls":5,"zero":2}`}}, nil
		}
		return nil, nil
	}}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"백업 정책"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, out.String())
	}
	if len(st.events) != 1 {
		t.Fatalf("read_events 기록 %d건, want 1", len(st.events))
	}
	if ev := st.events[0]; ev.session != "auto-label-7" {
		t.Errorf("session = %q, want auto-label-7(자동 라벨 채택)", ev.session)
	}
}

// asOfStore — a fakeStore with the history seams (QueryAsOf·LogEntries)
// layered on. Records call arguments to verify the dialect severance
// (raw AS OF SQL·dolt_log queries abolished).
type asOfStore struct {
	*fakeStore
	asOfCalls  []wire.AsOfQuery
	logCalls   []wire.LogQuery
	asOfRows   [][]any
	asOfErr    error
	logEntries []wire.LogEntry
	logErr     error
}

func (s *asOfStore) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	s.asOfCalls = append(s.asOfCalls, q)
	if s.asOfErr != nil {
		return nil, s.asOfErr
	}
	return s.asOfRows, nil
}

func (s *asOfStore) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	s.logCalls = append(s.logCalls, q)
	if s.logErr != nil {
		return nil, s.logErr
	}
	return s.logEntries, nil
}

// TestRunReadAsOfAddsField — with --as-of, a one-line
// as_of{hash,commit_date} JSON is appended after the existing row
// output. The query goes through Store.QueryAsOf (dialect severance —
// raw AS OF inlining abolished); the commit time through
// LogEntries{Hash}.
func TestRunReadAsOfAddsField(t *testing.T) {
	const asOfHash = "he08irluqn3v2m1k9x7c4jw5z6b8d0f"
	injectAsOfSeams(t, asOfHash)
	st := &asOfStore{
		fakeStore: &fakeStore{},
		asOfRows:  [][]any{{"3", "토픽", "요약", ":t:", "", "", "0.9", "active", "0"}},
		logEntries: []wire.LogEntry{{
			Hash: asOfHash,
			Date: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		}},
	}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"--as-of", "7d", "토픽"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, out.String())
	}
	got := out.String()
	if !strings.HasPrefix(got, "3|토픽|요약\n") {
		t.Errorf("기존 행 출력 변형: %q", got)
	}
	if !strings.Contains(got, `"as_of":{"hash":"he08irluqn3v2m1k9x7c4jw5z6b8d0f","commit_date":"2026-10-20T12:00:00Z"}`) {
		t.Errorf("as_of 필드 누락/불일치: %q", got)
	}
	// The query is QueryAsOf once — the hash·view-table contract.
	if len(st.asOfCalls) != 1 {
		t.Fatalf("QueryAsOf 호출 %d회, want 1: %v", len(st.asOfCalls), st.asOfCalls)
	}
	if aq := st.asOfCalls[0]; aq.Hash != asOfHash || aq.Table != "active_knowledge" {
		t.Errorf("AsOfQuery = %+v, want Hash·Table=active_knowledge", aq)
	}
	// Raw AS OF clauses abolished — any AS OF string left on the Query
	// path is a dialect-severance violation.
	for _, raw := range st.fakeStore.queries {
		if strings.Contains(raw, "AS OF") {
			t.Errorf("원시 AS OF 절 잔존: %q", raw)
		}
	}
	// The commit-time lookup is LogEntries{Hash} once (raw dolt_log
	// queries abolished).
	if len(st.logCalls) != 1 || st.logCalls[0].Hash != asOfHash {
		t.Errorf("LogEntries 호출 = %v, want 1회 Hash=%s", st.logCalls, asOfHash)
	}
}

// TestRunReadAsOfQueryErrorExit — on QueryAsOf failure (even after the
// db-implementation transfer of gate errors such as a hash-charset
// violation), the caller-side exit contract is a one-line error JSON +
// MapExit (an ordinary error exits 1).
func TestRunReadAsOfQueryErrorExit(t *testing.T) {
	injectAsOfSeams(t, "he08irluqn3v2m1k9x7c4jw5z6b8d0f")
	st := &asOfStore{
		fakeStore: &fakeStore{},
		asOfErr:   fmt.Errorf(`as-of hash charset violation: %q`, "NOTHASH"),
	}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"--as-of", "7d", "토픽"}, strings.NewReader(""), &out)
	if code != 1 {
		t.Fatalf("exit = %d, want 1(일반 오류 MapExit): %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"error":"internal"`) ||
		!strings.Contains(out.String(), "as-of hash charset violation") {
		t.Errorf("게이트 오류 전파 미관찰: %q", out.String())
	}
}

// TestRunReadAsOfDateQuietFailure — a commit-time lookup (LogEntries)
// failure is quiet — the as_of line's commit_date is empty·exit 0 (§13).
func TestRunReadAsOfDateQuietFailure(t *testing.T) {
	injectAsOfSeams(t, "he08irluqn3v2m1k9x7c4jw5z6b8d0f")
	st := &asOfStore{
		fakeStore: &fakeStore{},
		asOfRows:  [][]any{{"3", "토픽", "요약", ":t:", "", "", "0.9", "active", "0"}},
		logErr:    errors.New("log entries boom"),
	}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"--as-of", "7d", "토픽"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0(메타데이터 실패는 조용히): %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"commit_date":""`) {
		t.Errorf("조용한 실패 위반 — commit_date 빈 값 아님: %q", out.String())
	}
}

// TestRunReadAsOfInvalidExit3 — an invalid spec is exit 3 with a
// one-line JSON invalid_as_of (judged via the ErrInvalidAsOf injection
// point).
func TestRunReadAsOfInvalidExit3(t *testing.T) {
	savedFn, savedErr := ParseAsOfSpecFn, ErrInvalidAsOf
	ParseAsOfSpecFn = func(ctx context.Context, st wire.Store, spec string) (string, error) {
		return "", fmt.Errorf("무효 as-of 스펙 %q: %w", spec, ErrInvalidAsOf)
	}
	ErrInvalidAsOf = errors.New("invalid as-of spec")
	t.Cleanup(func() { ParseAsOfSpecFn, ErrInvalidAsOf = savedFn, savedErr })

	var out strings.Builder
	code := RunRead(context.Background(), &fakeStore{},
		[]string{"--as-of", "2 days ago", "토픽"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	if !strings.Contains(out.String(), `"error":"invalid_as_of"`) {
		t.Errorf("invalid_as_of 여부: %q", out.String())
	}
}

// TestReadFieldsValidation — --fields accepts known columns only. A
// typo ("sumary") is exit 3; valid fields pass.
func TestReadFieldsValidation(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"백업", "--fields", "topic,sumary"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d, want 3(오타 필드): %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"fields"`) {
		t.Errorf("오류 필드에 fields 없음: %s", out.String())
	}
	// Valid fields pass (regression — the existing usage shape).
	var out2 strings.Builder
	code2 := RunRead(context.Background(), st,
		[]string{"백업", "--fields", "topic,confidence"}, strings.NewReader(""), &out2)
	if code2 == 3 {
		t.Fatalf("정상 필드 exit 3: %s", out2.String())
	}
}

// TestReadRRFKInjectionBeforeReorder — the rrf_k injection (the
// wire.LoadThresholdsFn seam) is called before the fallback rerank.
func TestReadRRFKInjectionBeforeReorder(t *testing.T) {
	var order []string
	savedLT := wire.LoadThresholdsFn
	wire.LoadThresholdsFn = func(ctx context.Context, st wire.Store) (store.Thresholds, error) {
		order = append(order, "loadThresholds")
		return store.DefaultThresholds(), nil
	}
	t.Cleanup(func() { wire.LoadThresholdsFn = savedLT })
	// A fallback-path read (zero-hit phrase → OR re-query hit).
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			if strings.Contains(sql, " OR ") {
				return [][]any{{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"}}, nil
			}
			return nil, nil
		case strings.Contains(sql, "SELECT value FROM meta"):
			return nil, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	code := RunRead(context.Background(), f, []string{"백업 정책"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if len(order) == 0 {
		t.Fatal("폴백 경로에서 loadThresholds 미호출 — rrf_k 주입 누락")
	}
}

// TestReadUnknownFlagExit3 — an unrecognized '-'-prefixed argument is
// not absorbed into the query but is the invalid-arg exit 3 (abolishing
// the blind spot where read --jsonn quietly flowed to zero-hit and the
// agent never noticed the typo). The positional query·"--"
// end-of-options absorption are unchanged.
func TestReadUnknownFlagExit3(t *testing.T) {
	var out strings.Builder
	if code := RunRead(context.Background(), &fakeStore{}, []string{"--jsonn"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out.String())
	}
	// The unknown-flag class carries error:"usage".
	if !strings.Contains(out.String(), `"error":"usage"`) {
		t.Fatalf("에러 JSON error:usage 아님: %q", out.String())
	}
	// Positional query unchanged — non-'-'-prefixed values are still
	// absorbed as query text.
	out.Reset()
	if code := RunRead(context.Background(), &fakeStore{}, []string{"docker"}, strings.NewReader(""), &out); code == 3 {
		t.Fatalf("positional 쿼리가 exit 3: %s", out.String())
	}
	// Everything after "--" is positional — even '--jsonn' is absorbed as
	// a query.
	out.Reset()
	if code := RunRead(context.Background(), &fakeStore{}, []string{"--", "--jsonn"}, strings.NewReader(""), &out); code == 3 {
		t.Fatalf("'--' 이후 쿼리가 exit 3: %s", out.String())
	}
}

// TestReadLimitClampJSONLine — in --json, when the requested limit
// exceeds the cap (50), one additive-field line with limit_requested·
// limit_applied discloses the clamp (existing keys unchanged). Under the
// cap·text mode keep the existing format — no clamp line.
func TestReadLimitClampJSONLine(t *testing.T) {
	newStore := func() *fakeStore {
		return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM active_knowledge") {
				return hitRowsFixture, nil
			}
			return nil, nil
		}}
	}
	t.Run("--json --limit 99 — clamp 라인 추가", func(t *testing.T) {
		var out strings.Builder
		if code := RunRead(context.Background(), newStore(),
			[]string{"--json", "--limit", "99", "백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit=%d, want 0: %s", code, out.String())
		}
		lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
		if len(lines) != 3 { // 2 hit rows + 1 clamp line
			t.Fatalf("출력 라인 수 = %d, want 3(히트 2 + clamp 1): %q", len(lines), out.String())
		}
		var clamp map[string]int
		if err := json.Unmarshal([]byte(lines[2]), &clamp); err != nil {
			t.Fatalf("clamp 라인 JSON 아님: %q", lines[2])
		}
		if clamp["limit_requested"] != 99 || clamp["limit_applied"] != 50 {
			t.Fatalf("clamp 필드 = %v, want limit_requested:99·limit_applied:50", clamp)
		}
		// Existing keys unchanged — the hit rows keep id·topic·summary
		// as-is.
		var row map[string]string
		if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
			t.Fatalf("히트 행 JSON 아님: %q", lines[0])
		}
		if row["topic"] != "백업 정책" {
			t.Errorf("기존 키 변형 — topic=%q", row["topic"])
		}
	})
	t.Run("--json --limit 30 — 상한 이하 clamp 라인 없음", func(t *testing.T) {
		var out strings.Builder
		if code := RunRead(context.Background(), newStore(),
			[]string{"--json", "--limit", "30", "백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		if strings.Contains(out.String(), "limit_requested") {
			t.Errorf("상한 이하인데 clamp 라인 출력: %q", out.String())
		}
	})
	t.Run("텍스트 --limit 99 — 기존 형식 유지", func(t *testing.T) {
		var out strings.Builder
		if code := RunRead(context.Background(), newStore(),
			[]string{"--limit", "99", "백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		if strings.Contains(out.String(), "limit_requested") {
			t.Errorf("텍스트 모드에 clamp 라인 출력: %q", out.String())
		}
	})
}

// ── Suggestion (SuggestSimilar) input-cap truncation. ──

// TestSuggestQueryTruncate — the suggestion input is truncated to the
// cap (suggestQueryMaxRunes runes) (multibyte-safe — inheriting the
// single TruncateRunes implementation). The body search query itself is
// unchanged (the cap applies only to the suggestion similarity scan).
func TestSuggestQueryTruncate(t *testing.T) {
	short := "백업 복구 절차"
	if got := suggestQuery(short); got != short {
		t.Fatalf("상한 이하 쿼리 변형: %q", got)
	}
	huge := strings.Repeat("데", 100000)
	got := suggestQuery(huge)
	if n := len([]rune(got)); n != suggestQueryMaxRunes {
		t.Fatalf("truncate 크기=%d rune, want %d", n, suggestQueryMaxRunes)
	}
	if !strings.HasPrefix(got, strings.Repeat("데", 100)) {
		t.Fatal("rune 경계 절단 위반(멀티바이트 파손)")
	}
}

// ── The separated metering commit — the telemetry commit on the read/hot completion paths. ──

// telemetryCommitter — a fakeStore with the TelemetryCommitter seam
// layered on. Records CommitTelemetry call arguments (cmd).
type telemetryCommitter struct {
	*fakeStore
	commands []string
	err      error
}

func (tc *telemetryCommitter) CommitTelemetry(ctx context.Context, cmd string) error {
	tc.commands = append(tc.commands, cmd)
	return tc.err
}

// TestRunReadCommitsTelemetryOnce — the read completion path calls the
// metering commit exactly once (cmd="read"). Failures are quiet (§13 —
// exit 0·output unchanged). A non-implementing Store (fakeStore alone)
// skips it.
func TestRunReadCommitsTelemetryOnce(t *testing.T) {
	newStore := func() *telemetryCommitter {
		return &telemetryCommitter{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM active_knowledge") {
				return hitRowsFixture, nil
			}
			return nil, nil
		}}}
	}
	t.Run("히트 — telemetry 커밋 1회(cmd=read)", func(t *testing.T) {
		st := newStore()
		var out strings.Builder
		if code := RunRead(context.Background(), st,
			[]string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		if len(st.commands) != 1 || st.commands[0] != "read" {
			t.Fatalf("CommitTelemetry 호출 = %v, want [read] 1회", st.commands)
		}
	})
	t.Run("계측 커밋 실패 — 조용(exit 0·출력 불변)", func(t *testing.T) {
		st := newStore()
		st.err = errors.New("telemetry commit boom")
		var out strings.Builder
		if code := RunRead(context.Background(), st,
			[]string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d, want 0(계측 실패는 조용): %s", code, out.String())
		}
		if len(st.commands) != 1 {
			t.Fatalf("실패에도 호출은 1회: %v", st.commands)
		}
	})
	t.Run("미구현 Store — 스킵(패닉 없음)", func(t *testing.T) {
		var out strings.Builder
		if code := RunRead(context.Background(), &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			return hitRowsFixture, nil
		}}, []string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
	})
}

// TestRunHotCommitsTelemetryOnce — the hot completion path also calls
// the metering commit once (cmd="hot") (common to the headline·--full
// paths).
func TestRunHotCommitsTelemetryOnce(t *testing.T) {
	injectHotSeams(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"헤드라인", nil},
		{"--full 주입", []string{"--full"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &telemetryCommitter{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
				if strings.Contains(sql, "FROM hot_view") {
					return v1200HotRows(time.Now().UTC()), nil
				}
				return nil, nil
			}}}
			var out strings.Builder
			if code := RunHot(context.Background(), st, tc.args, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("RunHot exit = %d: %s", code, out.String())
			}
			if len(st.commands) != 1 || st.commands[0] != "hot" {
				t.Fatalf("CommitTelemetry 호출 = %v, want [hot] 1회", st.commands)
			}
		})
	}
}

// TestRunDoneCommitsTelemetryOnce — the done completion path calls the
// metering commit once (cmd="done") after the existing audit Commit
// (session-end). Failures are quiet (exit 0·output unchanged).
func TestRunDoneCommitsTelemetryOnce(t *testing.T) {
	stubAutoBackup(t)
	newDone := func() *telemetryDoneFake {
		return &telemetryDoneFake{doneFake: &doneFake{
			fakeStore: &fakeStore{}, stubbed: true, runs: 0}}
	}
	t.Run("ok 경로 — 감사 Commit 후 telemetry 커밋 1회(cmd=done)", func(t *testing.T) {
		d := newDone()
		var out strings.Builder
		if code := RunDone(context.Background(), d,
			[]string{"--session", "claude"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		if len(d.telemetryCmds) != 1 || d.telemetryCmds[0] != "done" {
			t.Fatalf("CommitTelemetry 호출 = %v, want [done] 1회", d.telemetryCmds)
		}
		if len(d.commits) != 1 || !strings.Contains(d.commits[0], "session-end") {
			t.Fatalf("감사 Commit = %v, want session-end 1회(기존 계약 불변)", d.commits)
		}
	})
	t.Run("계측 커밋 실패 — 조용(exit 0)", func(t *testing.T) {
		d := newDone()
		d.telErr = errors.New("telemetry commit boom")
		var out strings.Builder
		if code := RunDone(context.Background(), d,
			[]string{"--session", "claude"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d, want 0(계측 실패는 조용): %s", code, out.String())
		}
	})
}

// telemetryDoneFake — a doneFake with the TelemetryCommitter seam
// layered on.
type telemetryDoneFake struct {
	*doneFake
	telemetryCmds []string
	telErr        error
}

func (d *telemetryDoneFake) CommitTelemetry(ctx context.Context, cmd string) error {
	d.telemetryCmds = append(d.telemetryCmds, cmd)
	return d.telErr
}

// TestRunReadSuggestHugeQueryPerf — a G4 availability regression: a
// 100KB query suggestion finishes within seconds even with suggestion
// scan rows (2k) present, thanks to the cap truncation (before the fix,
// ~17s — bigramDice rebuilt the query bigram set per row). Unit-test
// ceiling 5s (absorbing environment variance — the measured value goes
// to t.Log).
func TestRunReadSuggestHugeQueryPerf(t *testing.T) {
	scanRows := make([][]any, 0, 2000)
	for i := 1; i <= 2000; i++ {
		scanRows = append(scanRows, []any{
			int32(i), fmt.Sprintf("demo suggest %04d", i), "합성 데모 요약 — 제안 스캔 행.",
		})
	}
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT id, topic, summary FROM active_knowledge") {
			return scanRows, nil // suggestion scan rows present
		}
		return nil, nil // body phrase·fallback zero-hit
	}}
	q := strings.Repeat("쿼", 51200) // 100KB (UTF-8 3 bytes × 51200 runes)
	var out strings.Builder
	start := time.Now()
	code := RunRead(context.Background(), st, []string{q}, strings.NewReader(""), &out)
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("exit=%d, want 0: %s", code, out.String())
	}
	t.Logf("100KB 쿼리 제안 경과: %s(상한 5s)", elapsed)
	if elapsed > 5*time.Second {
		t.Fatalf("100KB 쿼리 제안 %s — 상한 5s 초과(truncate 미적용 의심)", elapsed)
	}
}

// ── Recall reinforcement wiring (recall_score+1·last_recalled_at=now) ──

// recallBumperRecorder — a fake recording RecordKnowledgeReads·
// BumpRecallScores calls (implementing the KnowledgeReadsRecorder·
// RecallBumper seams together). The real-implementation path (the real
// DB increments of db.Repo.BumpRecallScores) is covered by
// lib/db reads_v1700_test.
type recallBumperRecorder struct {
	*fakeStore
	kreads []kreadsCall
	bumps  [][]int
}

func (r *recallBumperRecorder) RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error {
	r.kreads = append(r.kreads, kreadsCall{ids, session, kind})
	return nil
}

func (r *recallBumperRecorder) BumpRecallScores(ctx context.Context, ids []int) error {
	r.bumps = append(r.bumps, ids)
	return nil
}

// TestRunReadBumpsRecallScores — the full set of read hit rows passes
// through recall reinforcement (the RecallBumper seam) once — coupled to
// the RecordKnowledgeReads path (kind=read).
func TestRunReadBumpsRecallScores(t *testing.T) {
	st := &recallBumperRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM active_knowledge") {
			return hitRowsFixture, nil
		}
		return nil, nil
	}}}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	if len(st.bumps) != 1 || len(st.bumps[0]) != 2 || st.bumps[0][0] != 1 || st.bumps[0][1] != 2 {
		t.Fatalf("BumpRecallScores 호출 = %v, want 1회 ids [1 2](read 히트 행 전수)", st.bumps)
	}
	if len(st.kreads) != 1 || st.kreads[0].kind != "read" {
		t.Errorf("RecordKnowledgeReads = %v, want 1건 kind=read(강화와 동일 ids)", st.kreads)
	}
}

// TestRecordKnowledgeReadsHotNoBump — hot injection (kind=hot) is
// excluded from recall reinforcement — the ruling breaking the
// exposure→reinforcement→re-exposure cycle (hot ranking consumes
// recall_score). The history recording (knowledge_reads) is unchanged.
func TestRecordKnowledgeReadsHotNoBump(t *testing.T) {
	st := &recallBumperRecorder{fakeStore: &fakeStore{}}
	ctx := context.Background()
	recordKnowledgeReads(ctx, st, []int{3, 7}, "t2-v1700", "hot")
	if len(st.kreads) != 1 || len(st.bumps) != 0 {
		t.Fatalf("hot: kreads=%v bumps=%v, want 이력 1건·강화 0회", st.kreads, st.bumps)
	}
	recordKnowledgeReads(ctx, st, []int{3}, "t2-v1700", "read")
	if len(st.bumps) != 1 || st.bumps[0][0] != 3 {
		t.Fatalf("read: bumps=%v, want 1회 ids [3]", st.bumps)
	}
}

// ── v1.12.0 T2 — the gapq counter: the recall-gap signal of a zero-hit
// read on owned ground (owned_miss). The normalized-query daily counter
// (daily:gapq:<sha256>:<date>) is the T3 threshold input. ──

// gapQueryRecorder — a fakeStore layered for the gapq path: a map-backed
// meta store answering the gapq flat-key upsert·readback, an active-count
// row for the zero-hit decomposition, and everything else delegated to
// the embedded fakeStore. fail=true injects a transport error on the
// gapq branches (the quiet-failure path).
type gapQueryRecorder struct {
	*fakeStore
	active int
	fail   bool
	gapq   map[string]int
	bumps  []string
}

func (g *gapQueryRecorder) RecordReadUsage(ctx context.Context, zero, near bool) error {
	return nil
}

func (g *gapQueryRecorder) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	return nil
}

func (g *gapQueryRecorder) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	if g.fail {
		return nil, errors.New("gapq transport failure")
	}
	switch {
	case strings.Contains(sql, "INSERT INTO meta") && len(args) > 0 &&
		strings.HasPrefix(fmt.Sprint(args[0]), "daily:gapq:"):
		k := fmt.Sprint(args[0])
		g.gapq[k]++
		g.bumps = append(g.bumps, k)
		return nil, nil
	case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1") && len(args) > 0 &&
		strings.HasPrefix(fmt.Sprint(args[0]), "daily:gapq:"):
		k := fmt.Sprint(args[0])
		return [][]any{{fmt.Sprint(g.gapq[k])}}, nil
	case strings.Contains(sql, "FROM knowledge WHERE status = 'active'"):
		return [][]any{{fmt.Sprint(g.active)}}, nil
	}
	return g.fakeStore.Query(ctx, sql, args...)
}

// TestNormalizeGapQueryEquivalence — the normalization pipeline collapses
// representation noise before hashing (the same recall failure counts
// once): NFKC composed/decomposed Korean·case·whitespace runs·[flint]
// markers converge; differences beyond the 80-rune truncation vanish;
// genuinely different queries hash apart.
func TestNormalizeGapQueryEquivalence(t *testing.T) {
	composed := "각"     // U+AC01
	decomposed := "각" // NFD jamo sequence — converges under NFKC
	if NormalizeGapQuery(composed) != NormalizeGapQuery(decomposed) {
		t.Fatalf("NFKC 동치 실패: %q vs %q", NormalizeGapQuery(composed), NormalizeGapQuery(decomposed))
	}
	if NormalizeGapQuery("Flint  BACKUP") != NormalizeGapQuery(" flint backup ") {
		t.Fatalf("대소문자·공백 동치 실패: %q vs %q",
			NormalizeGapQuery("Flint  BACKUP"), NormalizeGapQuery(" flint backup "))
	}
	if NormalizeGapQuery("demo [flint] query") != NormalizeGapQuery("demo query") {
		t.Fatalf("살균 동치 실패: %q vs %q",
			NormalizeGapQuery("demo [flint] query"), NormalizeGapQuery("demo query"))
	}
	long1 := strings.Repeat("가", 90) + "X"
	long2 := strings.Repeat("가", 90) + "Y"
	if NormalizeGapQuery(long1) != NormalizeGapQuery(long2) {
		t.Fatal("80룬 절단 후 해시 상이 — 절단 미적용")
	}
	if NormalizeGapQuery("백업 정책") == NormalizeGapQuery("복구 정책") {
		t.Fatal("상이 쿼리가 동일 정규화 — 충돌")
	}
}

// TestNormalizeGapQueryTruncateAfterNFKC — v1.12.1 regression: the 80-rune
// truncation applies to the NFKC-normalized form. An NFD input of 81 runes
// (27 Hangul syllables × 3 jamo) must hash identically to its 27-rune NFC
// twin — truncating before normalization split the last syllable's jamo
// sequence and hashed the two spellings apart.
func TestNormalizeGapQueryTruncateAfterNFKC(t *testing.T) {
	nfc := strings.Repeat("각", 27) // 27 runes
	nfd := norm.NFD.String(nfc)    // 81 runes (3 jamo each)
	if got := len([]rune(nfd)); got != 81 {
		t.Fatalf("NFD 전제 룬수=%d, want 81(픽스처 가정 붕괴)", got)
	}
	hn, hf := GapQueryHash(NormalizeGapQuery(nfc)), GapQueryHash(NormalizeGapQuery(nfd))
	if hn != hf {
		t.Fatalf("NFKC 후 절단 동치 실패: NFC 27룬 %q vs NFD 81룬 — 해시 %s / %s",
			NormalizeGapQuery(nfc), hn, hf)
	}
}

// TestGapQueryHashShape — the digest is 64 lowercase hex chars (the
// sha256 output reused as the meta key fragment).
func TestGapQueryHashShape(t *testing.T) {
	h := GapQueryHash(NormalizeGapQuery("데모 쿼리"))
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(h) {
		t.Fatalf("해시 형태=%q, want 64자 소문자 hex", h)
	}
}

// TestBumpGapQueryCountAndQuiet — 3 same-day bumps return 1·2·3 under one
// key (the T3 threshold input); a store failure is quiet ok=false (§13 —
// the counter never breaks the caller).
func TestBumpGapQueryCountAndQuiet(t *testing.T) {
	g := &gapQueryRecorder{fakeStore: &fakeStore{}, active: 1, gapq: map[string]int{}}
	ctx := context.Background()
	for want := int64(1); want <= 3; want++ {
		n, ok := BumpGapQuery(ctx, g, "데모 쿼리")
		if !ok || n != want {
			t.Fatalf("BumpGapQuery=%d,%v — want %d,true", n, ok, want)
		}
	}
	distinct := map[string]bool{}
	for _, k := range g.bumps {
		distinct[k] = true
	}
	if len(g.bumps) != 3 || len(distinct) != 1 {
		t.Fatalf("bumps=%v — want 3회·단일 키(당일 동일 쿼리 집계)", g.bumps)
	}
	// failure injection — quiet, no panic
	bad := &gapQueryRecorder{fakeStore: &fakeStore{}, fail: true, gapq: map[string]int{}}
	if n, ok := BumpGapQuery(ctx, bad, "데모 쿼리"); ok || n != 0 {
		t.Fatalf("오류 주입 결과=%d,%v — want 0,false(조용히)", n, ok)
	}
}

// TestRunReadBumpsGapQueryOnOwnedMiss — the read completion path bumps
// the gapq counter only on owned_miss; unowned (active < floor) is
// DB-void noise — not counted (research ruling).
func TestRunReadBumpsGapQueryOnOwnedMiss(t *testing.T) {
	ctx := context.Background()
	t.Run("owned_miss — 카운트", func(t *testing.T) {
		g := &gapQueryRecorder{fakeStore: &fakeStore{}, active: 3, gapq: map[string]int{}}
		var out strings.Builder
		if code := RunRead(ctx, g, []string{"없는 데모 쿼리"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit=%d, want 0(제로히트); out=%s", code, out.String())
		}
		if len(g.bumps) != 1 {
			t.Fatalf("gapq bump=%d회, want 1(owned_miss)", len(g.bumps))
		}
		day := time.Now().UTC().Format("2006-01-02")
		want := "daily:gapq:" + GapQueryHash(NormalizeGapQuery("없는 데모 쿼리")) + ":" + day
		if g.bumps[0] != want {
			t.Fatalf("bump 키=%q, want %q", g.bumps[0], want)
		}
	})
	t.Run("unowned — 미카운트", func(t *testing.T) {
		g := &gapQueryRecorder{fakeStore: &fakeStore{}, active: 0, gapq: map[string]int{}}
		var out strings.Builder
		if code := RunRead(ctx, g, []string{"없는 데모 쿼리"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit=%d, want 0(제로히트); out=%s", code, out.String())
		}
		if len(g.bumps) != 0 {
			t.Fatalf("gapq bump=%d회, want 0(unowned 노이즈)", len(g.bumps))
		}
	})
}

// TestRunReadGapAccrualAfterThresholdReads — three owned_miss reads of
// the same query auto-accrue one pending gap candidate (the T3 read-path
// integration): the 3rd read's count reaching the threshold ADDs the
// candidate — the 1st·2nd do not.
func TestRunReadGapAccrualAfterThresholdReads(t *testing.T) {
	g := &gapQueryRecorder{fakeStore: &fakeStore{}, active: 2, gapq: map[string]int{}}
	ctx := context.Background()
	for i := 1; i <= 2; i++ {
		var out strings.Builder
		if code := RunRead(ctx, g, []string{"없는 데모 쿼리"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("read %d exit=%d, want 0; out=%s", i, code, out.String())
		}
		if len(g.inserted) != 0 {
			t.Fatalf("read %d 후 삽입=%d건, want 0(임계 미달)", i, len(g.inserted))
		}
	}
	var out strings.Builder
	if code := RunRead(ctx, g, []string{"없는 데모 쿼리"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("read 3 exit=%d, want 0; out=%s", code, out.String())
	}
	if len(g.inserted) != 1 {
		t.Fatalf("read 3 후 삽입=%d건, want 1(임계 도달 적립)", len(g.inserted))
	}
	rec := g.inserted[0]
	if want := "격차 후보: 없는 데모 쿼리"; rec["topic"] != want {
		t.Fatalf("topic=%v, want %q", rec["topic"], want)
	}
	if want := "회상 실패 3회 — 지식 격차 후보"; rec["summary"] != want {
		t.Fatalf("summary=%v, want %q", rec["summary"], want)
	}
	if rec["status"] != "pending" || rec["tags"] != ":gap-candidate:" {
		t.Fatalf("status=%v tags=%v, want pending/:gap-candidate:", rec["status"], rec["tags"])
	}
}

// TestRunReadAsOfSkipsGapCount — an as-of read (a past-point snapshot
// inspection) never counts as a present gap even on a zero-hit: no bump,
// no accrual (the T2 incident ruling — one guard line at the bump hook).
func TestRunReadAsOfSkipsGapCount(t *testing.T) {
	injectAsOfSeams(t, "he08irluqn3v2m1k9x7c4jw5z6b8d0f")
	g := &gapAsOfStore{asOfStore: &asOfStore{
		fakeStore: &fakeStore{},
		logEntries: []wire.LogEntry{{
			Hash: "he08irluqn3v2m1k9x7c4jw5z6b8d0f",
			Date: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		}},
	}, active: 2, gapq: map[string]int{}}
	var out strings.Builder
	code := RunRead(context.Background(), g,
		[]string{"--as-of", "1d", "없는 데모 쿼리"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0(as-of 제로히트); out=%s", code, out.String())
	}
	if len(g.bumps) != 0 || len(g.inserted) != 0 {
		t.Fatalf("as-of 가드 위반: bumps=%d inserted=%d, want 0/0", len(g.bumps), len(g.inserted))
	}
}

// gapAsOfStore — the asOfStore fake layered with the gapq meta
// interception (an as-of zero-hit on owned ground still must not bump).
type gapAsOfStore struct {
	*asOfStore
	active int
	gapq   map[string]int
	bumps  []string
}

func (g *gapAsOfStore) RecordReadUsage(ctx context.Context, zero, near bool) error {
	return nil
}

func (g *gapAsOfStore) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	return nil
}

func (g *gapAsOfStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	switch {
	case strings.Contains(sql, "INSERT INTO meta") && len(args) > 0 &&
		strings.HasPrefix(fmt.Sprint(args[0]), "daily:gapq:"):
		k := fmt.Sprint(args[0])
		g.gapq[k]++
		g.bumps = append(g.bumps, k)
		return nil, nil
	case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1") && len(args) > 0 &&
		strings.HasPrefix(fmt.Sprint(args[0]), "daily:gapq:"):
		return [][]any{{fmt.Sprint(g.gapq[fmt.Sprint(args[0])])}}, nil
	case strings.Contains(sql, "FROM knowledge WHERE status = 'active'"):
		return [][]any{{fmt.Sprint(g.active)}}, nil
	}
	return g.fakeStore.Query(ctx, sql, args...)
}
