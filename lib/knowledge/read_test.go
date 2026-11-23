package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

type readEventRecorder struct {
	*fakeStore
	events []readEventRecord
}

type readEventRecord struct {
	hits      int
	zero      bool
	near      bool
	fallback  bool
	session   string
	ids       string
	nearScore float64
}

func (r *readEventRecorder) RecordReadUsage(ctx context.Context, zero, near bool) error {
	return nil
}

func (r *readEventRecorder) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	r.events = append(r.events, readEventRecord{ev.Hits, ev.Zero, ev.Near, ev.Fallback, ev.Session, ev.IDs, ev.NearScore})
	return nil
}

type bytesRecorder struct {
	*fakeStore
	recorded []bytesCall
}

type bytesCall struct {
	kind string
	n    int
}

func (b *bytesRecorder) RecordBytes(ctx context.Context, kind string, n int) error {
	b.recorded = append(b.recorded, bytesCall{kind, n})
	return nil
}

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
			return nil, nil
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

func injectAsOfSeams(t *testing.T, hash string) {
	t.Helper()
	savedFn, savedErr := ParseAsOfSpecFn, ErrInvalidAsOf
	ParseAsOfSpecFn = func(ctx context.Context, st wire.Store, spec string) (string, error) {
		return hash, nil
	}
	ErrInvalidAsOf = errors.New("invalid as-of spec")
	t.Cleanup(func() { ParseAsOfSpecFn, ErrInvalidAsOf = savedFn, savedErr })
}

var hitRowsFixture = [][]any{
	{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"},
	{"2", "백업 절차", "매일", ":b:", "", "", "0.9", "active", "0"},
}

func TestRunReadZeroHitExit0Silent(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
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

func TestRunReadZeroHitJSONHits(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
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

func TestRunReadSessionAutoLabel(t *testing.T) {
	dir := t.TempDir()

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

	if len(st.asOfCalls) != 1 {
		t.Fatalf("QueryAsOf 호출 %d회, want 1: %v", len(st.asOfCalls), st.asOfCalls)
	}
	if aq := st.asOfCalls[0]; aq.Hash != asOfHash || aq.Table != "active_knowledge" {
		t.Errorf("AsOfQuery = %+v, want Hash·Table=active_knowledge", aq)
	}

	for _, raw := range st.fakeStore.queries {
		if strings.Contains(raw, "AS OF") {
			t.Errorf("원시 AS OF 절 잔존: %q", raw)
		}
	}

	if len(st.logCalls) != 1 || st.logCalls[0].Hash != asOfHash {
		t.Errorf("LogEntries 호출 = %v, want 1회 Hash=%s", st.logCalls, asOfHash)
	}
}

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

	var out2 strings.Builder
	code2 := RunRead(context.Background(), st,
		[]string{"백업", "--fields", "topic,confidence"}, strings.NewReader(""), &out2)
	if code2 == 3 {
		t.Fatalf("정상 필드 exit 3: %s", out2.String())
	}
}

func TestReadRRFKInjectionBeforeReorder(t *testing.T) {
	var order []string
	savedLT := wire.LoadThresholdsFn
	wire.LoadThresholdsFn = func(ctx context.Context, st wire.Store) (store.Thresholds, error) {
		order = append(order, "loadThresholds")
		return store.DefaultThresholds(), nil
	}
	t.Cleanup(func() { wire.LoadThresholdsFn = savedLT })

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

func TestReadUnknownFlagExit3(t *testing.T) {
	var out strings.Builder
	if code := RunRead(context.Background(), &fakeStore{}, []string{"--jsonn"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out.String())
	}

	if !strings.Contains(out.String(), `"error":"usage"`) {
		t.Fatalf("에러 JSON error:usage 아님: %q", out.String())
	}

	out.Reset()
	if code := RunRead(context.Background(), &fakeStore{}, []string{"docker"}, strings.NewReader(""), &out); code == 3 {
		t.Fatalf("positional 쿼리가 exit 3: %s", out.String())
	}

	out.Reset()
	if code := RunRead(context.Background(), &fakeStore{}, []string{"--", "--jsonn"}, strings.NewReader(""), &out); code == 3 {
		t.Fatalf("'--' 이후 쿼리가 exit 3: %s", out.String())
	}
}

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
		if len(lines) != 3 {
			t.Fatalf("출력 라인 수 = %d, want 3(히트 2 + clamp 1): %q", len(lines), out.String())
		}
		var clamp map[string]int
		if err := json.Unmarshal([]byte(lines[2]), &clamp); err != nil {
			t.Fatalf("clamp 라인 JSON 아님: %q", lines[2])
		}
		if clamp["limit_requested"] != 99 || clamp["limit_applied"] != 50 {
			t.Fatalf("clamp 필드 = %v, want limit_requested:99·limit_applied:50", clamp)
		}

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

type telemetryCommitter struct {
	*fakeStore
	commands []string
	err      error
}

func (tc *telemetryCommitter) CommitTelemetry(ctx context.Context, cmd string) error {
	tc.commands = append(tc.commands, cmd)
	return tc.err
}

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

type telemetryDoneFake struct {
	*doneFake
	telemetryCmds []string
	telErr        error
}

func (d *telemetryDoneFake) CommitTelemetry(ctx context.Context, cmd string) error {
	d.telemetryCmds = append(d.telemetryCmds, cmd)
	return d.telErr
}

func TestRunReadSuggestHugeQueryPerf(t *testing.T) {
	scanRows := make([][]any, 0, 2000)
	for i := 1; i <= 2000; i++ {
		scanRows = append(scanRows, []any{
			int32(i), fmt.Sprintf("demo suggest %04d", i), "합성 데모 요약 — 제안 스캔 행.",
		})
	}
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT id, topic, summary FROM active_knowledge") {
			return scanRows, nil
		}
		return nil, nil
	}}
	q := strings.Repeat("쿼", 51200)
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

func TestNormalizeGapQueryEquivalence(t *testing.T) {
	composed := "각"
	decomposed := "각"
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

func TestNormalizeGapQueryTruncateAfterNFKC(t *testing.T) {
	nfc := strings.Repeat("각", 27)
	nfd := norm.NFD.String(nfc)
	if got := len([]rune(nfd)); got != 81 {
		t.Fatalf("NFD 전제 룬수=%d, want 81(픽스처 가정 붕괴)", got)
	}
	hn, hf := GapQueryHash(NormalizeGapQuery(nfc)), GapQueryHash(NormalizeGapQuery(nfd))
	if hn != hf {
		t.Fatalf("NFKC 후 절단 동치 실패: NFC 27룬 %q vs NFD 81룬 — 해시 %s / %s",
			NormalizeGapQuery(nfc), hn, hf)
	}
}

func TestGapQueryHashShape(t *testing.T) {
	h := GapQueryHash(NormalizeGapQuery("데모 쿼리"))
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(h) {
		t.Fatalf("해시 형태=%q, want 64자 소문자 hex", h)
	}
}

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

	bad := &gapQueryRecorder{fakeStore: &fakeStore{}, fail: true, gapq: map[string]int{}}
	if n, ok := BumpGapQuery(ctx, bad, "데모 쿼리"); ok || n != 0 {
		t.Fatalf("오류 주입 결과=%d,%v — want 0,false(조용히)", n, ok)
	}
}

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

func TestRunReadPhraseHitTokenRerank(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM active_knowledge") {
			return [][]any{
				{"1", "설정 가이드", "백업 정책 안내 포함", ":b:", "", "", "0.95", "active", "0"},
				{"2", "백업 정책", "매일 실행", ":b:", "", "", "0.8", "active", "0"},
			}, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("출력 라인 수 = %d, want 2: %q", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], "2|백업 정책|") {
		t.Errorf("토큰 재배치 미적용 — 상위=%q, want 2|백업 정책(topic 접두 2토큰)", lines[0])
	}
	if !strings.HasPrefix(lines[1], "1|설정 가이드|") {
		t.Errorf("후순위=%q, want 1|설정 가이드(summary 포함)", lines[1])
	}

	if rank := FirstExactRank(
		[][]any{{"2", "백업 정책", "매일 실행"}, {"1", "설정 가이드", "백업 정책 안내 포함"}},
		"백업 정책"); rank != 1 {
		t.Errorf("재배치 후 FirstExactRank = %d, want 1", rank)
	}
}

func TestRunReadFallbackOrderNotRerankedByTokenScore(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			if strings.Contains(sql, " OR ") {
				return [][]any{
					{"1", "설정", "백업 절차 안내", ":d:", "", "", "0.9", "active", int32(100)},
					{"2", "백업", "메모 없음", ":d:", "", "", "0.8", "active", int32(0)},
				}, nil
			}
			return nil, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"백업 절차"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("출력 라인 수 = %d, want 2: %q", len(lines), out.String())
	}

	if !strings.HasPrefix(lines[0], "1|설정|") {
		t.Errorf("폴백 RRF 순서 침해 — 상위=%q, want 1|설정(이중 재배치 의심)", lines[0])
	}
	if !strings.HasPrefix(lines[1], "2|백업|") {
		t.Errorf("폴백 후순위=%q, want 2|백업", lines[1])
	}
}

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

var nearScoreRows = [][]any{
	{int32(1), "abxx", ""},
	{int32(2), "abqz", ""},
	{int32(3), "zzzz", ""},
}

func TestRunReadNearScoreScanMax(t *testing.T) {
	def := store.DefaultThresholds()
	query.SetSuggestThresholds(def.SuggestMinScore, def.SuggestScanCap)
	t.Cleanup(func() { query.SetSuggestThresholds(def.SuggestMinScore, def.SuggestScanCap) })

	readWith := func(t *testing.T, minOverride string, scanRows [][]any) *readEventRecorder {
		t.Helper()
		st := &readEventRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {

			if minOverride != "" && strings.Contains(sql, "FROM meta WHERE key IN") {
				return [][]any{{"suggest_min_score", minOverride}}, nil
			}
			if scanRows != nil && strings.Contains(sql, "SELECT id, topic, summary FROM active_knowledge") {
				return scanRows, nil
			}
			return nil, nil
		}}}
		var out strings.Builder
		if code := RunRead(context.Background(), st, []string{"abxy"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		return st
	}

	t.Run("제로히트·min 미만 — 스캔 최고치 적립(near=false 불변)", func(t *testing.T) {
		st := readWith(t, "0.9", nearScoreRows)
		if len(st.events) != 1 {
			t.Fatalf("read_events 기록 = %d건, want 1건", len(st.events))
		}
		ev := st.events[0]
		if !ev.zero || ev.near {
			t.Fatalf("zero=%v near=%v, want true·false(min 0.9 — near 플래그 의미 불변)", ev.zero, ev.near)
		}
		if math.Abs(ev.nearScore-2.0/3.0) > 1e-9 {
			t.Fatalf("near_score = %v, want 2/3(min 미만 zero 행의 스캔 최고치 — 0 마스킹 금지)", ev.nearScore)
		}
	})
	t.Run("제로히트·min 초과 — 동일 스캔 최고치", func(t *testing.T) {
		st := readWith(t, "0.2", nearScoreRows)
		ev := st.events[0]
		if !ev.near {
			t.Fatalf("near=%v, want true(min 0.2 — 후보 생존)", ev.near)
		}
		if math.Abs(ev.nearScore-2.0/3.0) > 1e-9 {
			t.Fatalf("near_score = %v, want 2/3", ev.nearScore)
		}
	})
	t.Run("히트 — 스캔 미실행 0", func(t *testing.T) {

		st := &readEventRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM active_knowledge") {
				return hitRowsFixture, nil
			}
			return nil, nil
		}}}
		var out strings.Builder
		if code := RunRead(context.Background(), st, []string{"abxy"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		if len(st.events) != 1 {
			t.Fatalf("read_events 기록 = %d건, want 1건", len(st.events))
		}
		ev := st.events[0]
		if ev.zero {
			t.Fatal("zero=true — 히트 행 전제 위반")
		}
		if ev.nearScore != 0 {
			t.Fatalf("히트 행 near_score = %v, want 0(스캔 미실행)", ev.nearScore)
		}
	})
	t.Run("제로히트·빈 스캔 — 0", func(t *testing.T) {
		st := readWith(t, "", nil)
		ev := st.events[0]
		if !ev.zero || ev.near {
			t.Fatalf("zero=%v near=%v, want true·false(빈 스캔)", ev.zero, ev.near)
		}
		if ev.nearScore != 0 {
			t.Fatalf("빈 스캔 near_score = %v, want 0", ev.nearScore)
		}
	})
}

func TestRunReadMinConfidence(t *testing.T) {
	valid := func(t *testing.T, spec string, want float64) {
		t.Helper()
		var gotSQL string
		var gotArgs []any
		st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {

			if !strings.Contains(sql, "FROM vault_files") {
				gotSQL, gotArgs = sql, args
			}
			if strings.Contains(sql, "FROM active_knowledge") {
				return hitRowsFixture, nil
			}
			return nil, nil
		}}
		var out strings.Builder
		if code := RunRead(context.Background(), st,
			[]string{"--min-confidence", spec, "백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d, want 0(경계값 %q): %s", code, spec, out.String())
		}
		if !strings.Contains(gotSQL, "confidence >= $") {
			t.Errorf("confidence >= $n 절 누락: %s", gotSQL)
		}
		found := false
		for _, a := range gotArgs {
			if fv, ok := a.(float64); ok && fv == want {
				found = true
			}
		}
		if !found {
			t.Errorf("값 %v가 args에 없음: %v", want, gotArgs)
		}
	}
	t.Run("경계 0·0.5·1 — 통과", func(t *testing.T) {
		valid(t, "0", 0)
		valid(t, "0.5", 0.5)
		valid(t, "1", 1)
	})
	for _, spec := range []string{"1.1", "-0.1", "abc", ""} {
		t.Run("거부 "+strconv.Quote(spec), func(t *testing.T) {
			var out strings.Builder
			if code := RunRead(context.Background(), &fakeStore{},
				[]string{"--min-confidence", spec, "q"}, strings.NewReader(""), &out); code != 3 {
				t.Fatalf("exit = %d, want 3(무효 min-confidence %q)", code, spec)
			}
		})
	}
}

func TestRunReadOrigin(t *testing.T) {
	for _, v := range []string{"agent", "human"} {
		t.Run("통과 "+v, func(t *testing.T) {
			var gotSQL string
			var gotArgs []any
			st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {

				if !strings.Contains(sql, "FROM vault_files") {
					gotSQL, gotArgs = sql, args
				}
				if strings.Contains(sql, "FROM active_knowledge") {
					return hitRowsFixture, nil
				}
				return nil, nil
			}}
			var out strings.Builder
			if code := RunRead(context.Background(), st,
				[]string{"--origin", v, "백업 정책"}, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("exit = %d, want 0: %s", code, out.String())
			}
			if !strings.Contains(gotSQL, "origin = $") {
				t.Errorf("origin = $n 절 누락: %s", gotSQL)
			}
			found := false
			for _, a := range gotArgs {
				if s, ok := a.(string); ok && s == v {
					found = true
				}
			}
			if !found {
				t.Errorf("값 %q가 args에 없음: %v", v, gotArgs)
			}
		})
	}
	for _, spec := range []string{"bogus", ""} {
		t.Run("거부 "+strconv.Quote(spec), func(t *testing.T) {
			var out strings.Builder
			if code := RunRead(context.Background(), &fakeStore{},
				[]string{"--origin", spec, "q"}, strings.NewReader(""), &out); code != 3 {
				t.Fatalf("exit = %d, want 3(무효 origin %q)", code, spec)
			}
		})
	}
}

func TestRunReadNewFiltersCombined(t *testing.T) {
	var gotSQL string
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {

		if !strings.Contains(sql, "FROM vault_files") {
			gotSQL = sql
		}
		if strings.Contains(sql, "FROM knowledge") {
			return hitRowsFixture, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"--tag", "b", "--entity", "user", "--domain", "game",
			"--min-confidence", "0.5", "--origin", "human",
			"--status", "active", "백업"},
		strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, out.String())
	}
	for _, frag := range []string{
		"FROM knowledge",
		"status = $1",
		"strpos(tags, ':' || $3 || ':') > 0",
		"entity = $4",
		"domain = $5",
		"confidence >= $6",
		"origin = $7",
	} {
		if !strings.Contains(gotSQL, frag) {
			t.Errorf("조합 필터 조각 %q 누락: %s", frag, gotSQL)
		}
	}
}

func TestRunReadNewFiltersZeroHit(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"--min-confidence", "0.9", "--origin", "agent", "없는토픽"},
		strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0(제로히트 exit 0)", code)
	}
	if out.String() != "" {
		t.Errorf("제로히트 텍스트 침묵 위반: %q", out.String())
	}
}

var vaultFixtureRows = [][]any{
	{"데모 노트 하나", "합성 데모 요약 하나", ":demo:", "/tmp/demo-vault/note-a.md"},
	{"데모 노트 둘", "합성 데모 요약 둘", ":demo:", "/tmp/demo-vault/note-b.md"},
}

func vaultQueryFn(knowledge, vault [][]any, vaultErr error) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM vault_files"):
			if vaultErr != nil {
				return nil, vaultErr
			}
			return vault, nil
		case strings.Contains(sql, "count(*) FROM knowledge"):
			return [][]any{{"0"}}, nil
		case strings.Contains(sql, "FROM active_knowledge"):
			return knowledge, nil
		}
		return nil, nil
	}
}

func TestRunReadVaultRowsAppendedAfterKnowledge(t *testing.T) {
	run := func(vault [][]any) string {
		st := &fakeStore{queryFn: vaultQueryFn(hitRowsFixture, vault, nil)}
		var out strings.Builder
		if code := RunRead(context.Background(), st,
			[]string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		return out.String()
	}
	without, with := run(nil), run(vaultFixtureRows)
	if !strings.HasPrefix(with, without) {
		t.Fatalf("지식 직렬화 불변 위반:\n없을 때=%q\n있을 때=%q", without, with)
	}
	if !strings.Contains(with, "vault|/tmp/demo-vault/note-a.md|데모 노트 하나|합성 데모 요약 하나") {
		t.Errorf("vault 행(path 식별·source 표기) 부재: %q", with)
	}
}

func TestRunReadVaultCapFive(t *testing.T) {
	var seven [][]any
	for i := 0; i < 7; i++ {
		seven = append(seven, []any{
			fmt.Sprintf("데모 노트 %d", i), "합성 요약", ":demo:", fmt.Sprintf("/tmp/demo-vault/n%d.md", i)})
	}
	st := &fakeStore{queryFn: vaultQueryFn(hitRowsFixture, seven, nil)}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	n := 0
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if strings.HasPrefix(line, "vault|") {
			n++
		}
	}
	if n != query.VaultMaxRows {
		t.Errorf("vault 행 = %d, want %d(상한 상수): %q", n, query.VaultMaxRows, out.String())
	}
}

func TestRunReadVaultZeroHitKnowledgeDomain(t *testing.T) {
	st := &readEventRecorder{fakeStore: &fakeStore{
		queryFn: vaultQueryFn(nil, vaultFixtureRows[:1], nil)}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"--json", "없는토픽"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, out.String())
	}
	got := out.String()
	if !strings.Contains(got, `"hits":0`) {
		t.Errorf("지식 hits=0 라인 누락: %q", got)
	}
	if !strings.Contains(got, `"vault_hits":1`) {
		t.Errorf("vault_hits 라인 누락: %q", got)
	}
	if !strings.Contains(got, `"source":"vault"`) {
		t.Errorf("vault json 행 source 표기 부재: %q", got)
	}
	if ev := st.events[0]; ev.hits != 0 || !ev.zero {
		t.Errorf("read_event = %+v, want hits=0·zero=true(지식 도메인 불변)", ev)
	}
}

func TestRunReadVaultAsOfCurrentState(t *testing.T) {
	const asOfHash = "he08irluqn3v2m1k9x7c4jw5z6b8d0f"
	injectAsOfSeams(t, asOfHash)
	st := &asOfStore{
		fakeStore: &fakeStore{queryFn: vaultQueryFn(nil, vaultFixtureRows[:1], nil)},
		asOfRows:  [][]any{{"3", "과거 토픽", "과거 요약", ":t:", "", "", "0.9", "active", "0"}},
	}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"--as-of", "7d", "토픽"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, out.String())
	}
	got := out.String()
	if !strings.HasPrefix(got, "3|과거 토픽|과거 요약\n") {
		t.Errorf("지식(as-of) 행 변형: %q", got)
	}
	if !strings.Contains(got, "vault|/tmp/demo-vault/note-a.md") {
		t.Errorf("vault 현재 상태 행 부재: %q", got)
	}
	if len(st.asOfCalls) != 1 || st.asOfCalls[0].Table != "active_knowledge" {
		t.Errorf("QueryAsOf = %v, want 1회 active_knowledge(시점여행은 지식만)", st.asOfCalls)
	}
}

func TestRunReadVaultSanitized(t *testing.T) {
	dirty := [][]any{{"데모 제목", "합성[flint]요약", ":demo:", "/tmp/demo-vault/x.md"}}
	st := &fakeStore{queryFn: vaultQueryFn(hitRowsFixture, dirty, nil)}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	if strings.Contains(out.String(), "[flint]") {
		t.Errorf("vault 출력 살균 위반: %q", out.String())
	}
	if !strings.Contains(out.String(), "합성 요약") {
		t.Errorf("살균 후 요약 잔존 확인 불가: %q", out.String())
	}
}

func TestRunReadVaultFiltersDoNotApply(t *testing.T) {
	var knowledgeSQL, vaultSQL string
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM vault_files") {
			vaultSQL = sql
			return vaultFixtureRows[:1], nil
		}
		if strings.Contains(sql, "count(*) FROM knowledge") {
			return [][]any{{"0"}}, nil
		}
		if (strings.Contains(sql, "FROM active_knowledge") || strings.Contains(sql, "FROM knowledge")) &&
			strings.Contains(sql, "WHERE") {
			knowledgeSQL = sql
		}
		return nil, nil
	}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"--min-confidence", "0.9", "--origin", "agent", "백업"},
		strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	if !strings.Contains(knowledgeSQL, "confidence >= $") || !strings.Contains(knowledgeSQL, "origin = $") {
		t.Errorf("지식 쿼리 필터 누락: %s", knowledgeSQL)
	}
	if strings.Contains(vaultSQL, "confidence") || strings.Contains(vaultSQL, "origin") {
		t.Errorf("vault 쿼리에 지식 필터 침투: %s", vaultSQL)
	}
	if !strings.Contains(out.String(), "vault|/tmp/demo-vault/note-a.md") {
		t.Errorf("필터 조합 시 vault 행 부재: %q", out.String())
	}
}

func TestRunReadVaultQueryErrorQuiet(t *testing.T) {
	st := &fakeStore{queryFn: vaultQueryFn(hitRowsFixture, nil, errors.New("vault boom"))}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"백업 정책"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, out.String())
	}
	if want := "1|백업 정책|매일\n2|백업 절차|매일\n"; out.String() != want {
		t.Errorf("vault 실패 시 출력 = %q, want %q(지식 결과 무왜곡)", out.String(), want)
	}
}

func seedSlowVaultRows(t *testing.T, st *writeRealStore, n int, term string) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("/tmp/demo-vault/slow-%d.md", i)
		_, err := st.pool.Exec(ctx,
			`INSERT INTO vault_files (path, title, tags, mtime, size, content_hash, summary)
			 VALUES ($1, $2, $3, now(), $4, $5, $6)`,
			path, fmt.Sprintf("%s 데모 노트 %d", term, i), ":demo:", 100+i,
			fmt.Sprintf("slowhash%d-%064d", i, i), fmt.Sprintf("%s 합성 데모 요약 %d", term, i))
		if err != nil {
			t.Fatalf("vault_files 적립 %d: %v", i, err)
		}
	}
}

func TestSlowReadVaultFusionV1210(t *testing.T) {
	st := t5ScratchStore(t)
	ctx := context.Background()
	var wout strings.Builder
	if code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(
		`{"topic":"v1210 데모 백업 정책","summary":"합성 데모 요약","confidence":0.9}`+"\n"+
			`{"topic":"v1210 데모 백업 절차","summary":"합성 데모 요약","confidence":0.9}`+"\n"), &wout); code != 0 {
		t.Fatalf("지식 적립 exit=%d: %s", code, wout.String())
	}
	var before strings.Builder
	if code := RunRead(ctx, st, []string{"v1210 데모 백업"}, strings.NewReader(""), &before); code != 0 {
		t.Fatalf("vault 적립 전 read exit=%d: %s", code, before.String())
	}
	if !strings.Contains(before.String(), "v1210 데모 백업 정책") {
		t.Fatalf("지식 히트 없음: %q", before.String())
	}

	seedSlowVaultRows(t, st, 7, "v1210 데모 백업")

	var after strings.Builder
	if code := RunRead(ctx, st, []string{"v1210 데모 백업"}, strings.NewReader(""), &after); code != 0 {
		t.Fatalf("vault 적립 후 read exit=%d: %s", code, after.String())
	}
	got := after.String()
	if !strings.HasPrefix(got, before.String()) {
		t.Fatalf("지식 직렬화 불변 위반(실DB):\n전=%q\n후=%q", before.String(), got)
	}
	vaultN := 0
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if strings.HasPrefix(line, "vault|") {
			vaultN++
		}
	}
	if vaultN != query.VaultMaxRows {
		t.Errorf("vault 행 = %d, want %d(7행 중 상한): %q", vaultN, query.VaultMaxRows, got)
	}
	if !strings.Contains(got, "vault|/tmp/demo-vault/slow-0.md|v1210 데모 백업 데모 노트 0|") {
		t.Errorf("vault 행 path 식별 부재: %q", got)
	}
}

func TestSlowReadVaultZeroHitV1210(t *testing.T) {
	st := t5ScratchStore(t)
	ctx := context.Background()
	seedSlowVaultRows(t, st, 2, "v1210 데모 회고")

	var out strings.Builder
	code := RunRead(ctx, st, []string{"--json", "v1210 데모 회고"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0: %s", code, out.String())
	}
	got := out.String()
	if !strings.Contains(got, `"hits":0`) {
		t.Errorf("지식 hits=0 라인 누락: %q", got)
	}
	if !strings.Contains(got, `"vault_hits":2`) {
		t.Errorf("vault_hits=2 라인 누락: %q", got)
	}
	if !strings.Contains(got, `"source":"vault"`) {
		t.Errorf("vault json 행 source 표기 부재: %q", got)
	}
}

var relatedFixtureRows = [][]any{
	{"2", "데모 이웃 정방향", "합성 데모 요약 정방향", "refines", "forward"},
	{"3", "데모 이웃 역방향", "합성 데모 요약 역방향", "supports", "reverse"},
}

func relatedQueryFn(knowledge, related [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM knowledge_links"):
			return related, nil
		case strings.Contains(sql, "FROM knowledge WHERE id = $1"):
			return [][]any{{args[0]}}, nil
		case strings.Contains(sql, "count(*) FROM knowledge"):
			return [][]any{{"0"}}, nil
		case strings.Contains(sql, "FROM active_knowledge"):
			return knowledge, nil
		}
		return nil, nil
	}
}

func TestRunReadRelatedAppendedAfterKnowledge(t *testing.T) {
	run := func(flag bool) string {
		args := []string{"백업 정책"}
		if flag {
			args = append(args, "--related", "1")
		}
		st := &fakeStore{queryFn: relatedQueryFn(hitRowsFixture, relatedFixtureRows)}
		var out strings.Builder
		if code := RunRead(context.Background(), st, args, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		return out.String()
	}
	without, with := run(false), run(true)
	if !strings.HasPrefix(with, without) {
		t.Fatalf("지식 직렬화 불변 위반:\n없을 때=%q\n있을 때=%q", without, with)
	}
	if !strings.Contains(with, "related|2|refines|forward|데모 이웃 정방향|합성 데모 요약 정방향") {
		t.Errorf("정방향 이웃 행(라벨·방향 표기) 부재: %q", with)
	}
	if !strings.Contains(with, "related|3|supports|reverse|데모 이웃 역방향|합성 데모 요약 역방향") {
		t.Errorf("역방향 이웃 행(라벨·역방향 표기) 부재: %q", with)
	}
}

func TestRunReadRelatedDefaultSeal(t *testing.T) {
	st := &fakeStore{queryFn: relatedQueryFn(hitRowsFixture, relatedFixtureRows)}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	if want := "1|백업 정책|매일\n2|백업 절차|매일\n"; out.String() != want {
		t.Errorf("기본 read 출력 = %q, want %q(바이트 불변 봉인)", out.String(), want)
	}
	for _, sql := range st.queries {
		if strings.Contains(sql, "knowledge_links") {
			t.Errorf("플래그 부재 시 knowledge_links 쿼리 발화: %s", sql)
		}
	}
}

func TestRunReadRelatedCapFive(t *testing.T) {
	var seven [][]any
	for i := 0; i < 7; i++ {
		seven = append(seven, []any{
			fmt.Sprintf("%d", 10+i), fmt.Sprintf("데모 이웃 %d", i), "합성 요약", "refines", "forward"})
	}
	st := &fakeStore{queryFn: relatedQueryFn(hitRowsFixture, seven)}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"백업 정책", "--related", "1"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	n := 0
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if strings.HasPrefix(line, "related|") {
			n++
		}
	}
	if n != query.RelatedMaxRows {
		t.Errorf("related 행 = %d, want %d(상한 상수): %q", n, query.RelatedMaxRows, out.String())
	}
}

func TestRunReadRelatedTargetMissingExit3(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge WHERE id = $1") {
			return nil, nil
		}
		return relatedQueryFn(hitRowsFixture, nil)(sql, args)
	}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"백업 정책", "--related", "999"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d, want 3: %s", code, out.String())
	}
	if strings.Contains(out.String(), "백업 정책|") {
		t.Errorf("부재 exit 3에 지식 결과 출력 혼입: %q", out.String())
	}
	if !strings.Contains(out.String(), "999") {
		t.Errorf("출력에 대상 id 부재: %q", out.String())
	}
}

func TestRunReadRelatedInt4CeilingV1241(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge WHERE id = $1") {
			return nil, errors.New("int8 encoding over int4")
		}
		return relatedQueryFn(hitRowsFixture, nil)(sql, args)
	}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"백업 정책", "--related", "2147483648"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d, want 3(int4 상한 사전 거부): %s", code, out.String())
	}
	if !strings.Contains(out.String(), "int4 ceiling") {
		t.Fatalf("출력에 int4 상한 안내 부재: %s", out.String())
	}

	var out2 strings.Builder
	st2 := &fakeStore{queryFn: relatedQueryFn(hitRowsFixture, nil)}
	code2 := RunRead(context.Background(), st2,
		[]string{"백업 정책", "--related", "2147483647"}, strings.NewReader(""), &out2)
	if code2 != 0 {
		t.Fatalf("경계값(2147483647) exit = %d, want 0(파싱 통과): %s", code2, out2.String())
	}
	if !strings.Contains(out2.String(), "백업 정책|") {
		t.Fatalf("경계값 read 결과 미출력: %q", out2.String())
	}
}

func TestRunReadRelatedZeroLinksEmpty(t *testing.T) {
	run := func(flag bool) string {
		args := []string{"백업 정책"}
		if flag {
			args = append(args, "--related", "1")
		}
		st := &fakeStore{queryFn: relatedQueryFn(hitRowsFixture, nil)}
		var out strings.Builder
		if code := RunRead(context.Background(), st, args, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		return out.String()
	}
	if without, with := run(false), run(true); without != with {
		t.Errorf("0건 링크 출력 = %q, want %q(빈 응답 — 바이트 동일)", with, without)
	}
}

func TestRunReadRelatedJSONStructure(t *testing.T) {
	st := &fakeStore{queryFn: relatedQueryFn(hitRowsFixture, relatedFixtureRows)}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"--json", "백업 정책", "--related", "1"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, out.String())
	}
	got := out.String()
	if !strings.Contains(got, `"source":"related"`) {
		t.Errorf("related json 행 source 표기 부재: %q", got)
	}
	if !strings.Contains(got, `"relation":"refines"`) || !strings.Contains(got, `"direction":"reverse"`) {
		t.Errorf("related json 행 라벨·방향 부재: %q", got)
	}
	if !strings.Contains(got, `"related_hits":2`) {
		t.Errorf("related_hits 라인 부재: %q", got)
	}
	if strings.Contains(got, `"hits":`) {
		t.Errorf("related 카운트의 hits 접힘(지식 도메인 불변): %q", got)
	}
}

func TestRunReadRelatedInvalidValue(t *testing.T) {
	for _, args := range [][]string{
		{"백업 정책", "--related", "abc"},
		{"백업 정책", "--related"},
	} {
		st := &fakeStore{queryFn: relatedQueryFn(hitRowsFixture, nil)}
		var out strings.Builder
		code := RunRead(context.Background(), st, args, strings.NewReader(""), &out)
		if code != 3 {
			t.Fatalf("args %v exit = %d, want 3: %s", args, code, out.String())
		}
	}
}

func whyQueryFn(knowledge, fallback [][]any, count string) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, " OR "):
			return fallback, nil
		case strings.Contains(sql, "count(*) FROM knowledge"):
			if count == "" {
				return nil, nil
			}
			return [][]any{{count}}, nil
		case strings.Contains(sql, "FROM active_knowledge"):
			return knowledge, nil
		}
		return nil, nil
	}
}

func TestRunReadWhyTokenEvidenceAppendix(t *testing.T) {
	st := &fakeStore{queryFn: whyQueryFn(hitRowsFixture, nil, "")}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"백업 정책", "--why"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	want := "1|백업 정책|매일\n2|백업 절차|매일\n" +
		"why|id=1|topic_prefix×4+topic_contain×2+cover×2|score=8\n" +
		"why|id=2|topic_prefix×4+cover×1|score=5\n"
	if out.String() != want {
		t.Errorf("--why 출력 = %q, want %q(근거 직접 대조)", out.String(), want)
	}
}

func TestRunReadWhyDefaultSeal(t *testing.T) {
	run := func(flag bool) string {
		args := []string{"백업 정책"}
		if flag {
			args = append(args, "--why")
		}
		st := &fakeStore{queryFn: whyQueryFn(hitRowsFixture, nil, "")}
		var out strings.Builder
		if code := RunRead(context.Background(), st, args, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		return out.String()
	}
	without, with := run(false), run(true)
	if want := "1|백업 정책|매일\n2|백업 절차|매일\n"; without != want {
		t.Errorf("기본 read 출력 = %q, want %q(바이트 불변 봉인)", without, want)
	}
	if !strings.HasPrefix(with, without) {
		t.Errorf("지식 직렬화 불변 위반:\n없을 때=%q\n있을 때=%q", without, with)
	}
}

func TestRunReadWhyFallbackRRFEvidence(t *testing.T) {
	fallbackRows := [][]any{
		{"1", "backup policy demo", "synthetic note", "", "", "", "0.9", "active", int32(5)},
		{"2", "demo subject", "backup method synthetic", "", "", "", "0.8", "active", int32(2)},
	}
	st := &fakeStore{queryFn: whyQueryFn(nil, fallbackRows, "0")}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"backup", "policy", "--why"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	wantPairs := map[string]string{
		"why|id=1|": "match=2/r1/", "why|id=2|": "match=1/r2/",
	}
	whyLines := 0
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "why|") {
			continue
		}
		whyLines++
		fields := strings.Split(line, "|")
		if len(fields) != 4 {
			t.Fatalf("why 행 필드 수 = %d: %q", len(fields), line)
		}
		sigStrs := strings.Split(fields[2], ";")
		if len(sigStrs) != 4 {
			t.Fatalf("신호 수 = %d, want 4: %q", len(sigStrs), line)
		}
		sum := 0.0
		for _, s := range sigStrs {
			parts := strings.Split(s, "=")
			sig := parts[0]
			vr := strings.Split(parts[1], "/")
			rank := vr[1]

			prefixOK := false
			for _, w := range []string{"match=2/r1/", "read_count=5/r1/", "jamo=0/r0/", "topic=2/r1/",
				"match=1/r2/", "read_count=2/r2/", "topic=0/r0/"} {
				if strings.HasPrefix(s, w) {
					prefixOK = true
				}
			}
			if !prefixOK {
				t.Errorf("신호 %q 값/rank 불일치: %q", sig, s)
			}
			if len(vr) != 3 {
				t.Fatalf("신호 인코딩 = %q: %q", s, line)
			}
			c, err := strconv.ParseFloat(vr[2], 64)
			if err != nil {
				t.Fatalf("기여 파싱 %q: %v", s, err)
			}
			if rank == "r0" && c != 0 {
				t.Errorf("무기여 신호의 기여 = %v: %q", c, s)
			}
			sum += c
		}
		score, err := strconv.ParseFloat(strings.TrimPrefix(fields[3], "score="), 64)
		if err != nil {
			t.Fatalf("score 파싱 %q: %v", fields[3], err)
		}
		if math.Abs(sum-score) > 1e-12 {
			t.Errorf("기여 합 %v ≠ score %v: %q", sum, score, line)
		}
		if _, ok := wantPairs[fields[0]+"|"+fields[1]+"|"]; !ok {
			t.Errorf("예상 밖 why 행: %q", line)
		}
	}
	if whyLines != 2 {
		t.Errorf("why 행 = %d, want 2: %q", whyLines, out.String())
	}
}

func TestRunReadWhyZeroHitEvidence(t *testing.T) {
	st := &fakeStore{queryFn: whyQueryFn(nil, nil, "3")}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"없는 토픽 데모", "--why"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	if want := "why|zero_reason=owned_miss|near_score=0\n"; out.String() != want {
		t.Errorf("zero 근거 = %q, want %q", out.String(), want)
	}

	var jout strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"--json", "없는 토픽 데모", "--why"}, strings.NewReader(""), &jout); code != 0 {
		t.Fatalf("json exit = %d: %s", code, jout.String())
	}
	got := jout.String()
	if !strings.Contains(got, `{"hits":0}`) || !strings.Contains(got, `{"why":{"zero_reason":"owned_miss","near_score":0}}`) {
		t.Errorf("json zero 근거 누락: %q", got)
	}
}

func TestRunReadWhyRelatedCombination(t *testing.T) {
	st := &fakeStore{queryFn: relatedQueryFn(hitRowsFixture, relatedFixtureRows)}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"백업 정책", "--related", "1", "--why"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	got := out.String()
	if !strings.HasPrefix(got, "1|백업 정책|매일\n2|백업 절차|매일\n") {
		t.Errorf("지식 직렬화 불변 위반: %q", got)
	}
	relatedIdx := strings.Index(got, "related|2|refines|forward|")
	whyIdx := strings.Index(got, "why|id=1|topic_prefix×4+topic_contain×2+cover×2|score=8")
	if relatedIdx < 0 {
		t.Fatalf("related 행 부재: %q", got)
	}
	if whyIdx < 0 {
		t.Fatalf("why 행 부재: %q", got)
	}
	if whyIdx < relatedIdx {
		t.Errorf("why 부록이 related 블록 앞에 위치: %q", got)
	}
}

func TestRunReadWhyAppendixCap(t *testing.T) {
	var twelve [][]any
	for i := 0; i < 12; i++ {
		twelve = append(twelve, []any{
			fmt.Sprintf("%d", i+1), fmt.Sprintf("백업 데모 주제 %d", i), "합성 요약",
			":d:", "", "", "0.9", "active", "0"})
	}
	st := &fakeStore{queryFn: whyQueryFn(twelve, nil, "")}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"백업", "--why"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	n := 0
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if strings.HasPrefix(line, "why|") {
			n++
		}
	}
	if n != query.WhyMaxRows {
		t.Errorf("why 행 = %d, want %d(부록 총 상한): %q", n, query.WhyMaxRows, out.String())
	}
}

func TestRunReadWhyIDNoPipe(t *testing.T) {
	rows := [][]any{{"a|b", "백업 데모", "합성", "", "", "", "0.9", "active", "0"}}
	st := &fakeStore{queryFn: whyQueryFn(rows, nil, "")}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"백업", "--why"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "why|id=a b|topic_prefix×4+cover×1|score=5") {
		t.Errorf("noPipe 인코딩 위반: %q", out.String())
	}
}

func slowWriteKnowledge(t *testing.T, st *writeRealStore, topic, relations string) int {
	t.Helper()
	ctx := context.Background()
	var wout strings.Builder
	if code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(
		relPayload(topic, "합성 데모 요약", relations)+"\n"), &wout); code != 0 {
		t.Fatalf("적립 exit=%d(%s): %s", code, topic, wout.String())
	}
	var id int
	if err := st.pool.QueryRow(ctx, `SELECT id FROM knowledge WHERE topic = $1`, topic).Scan(&id); err != nil {
		t.Fatalf("id 조회(%s): %v", topic, err)
	}
	return id
}

func TestSlowReadRelatedRoundtripV1240(t *testing.T) {
	st := t5ScratchStore(t)
	ctx := context.Background()
	const term = "v1240 데모 이웃"
	idB := slowWriteKnowledge(t, st, term+" 정방향 B", "")
	idC := slowWriteKnowledge(t, st, term+" 정방향 C", "")
	rels := fmt.Sprintf(`{"to":%d,"relation":"refines"},{"to":%d,"relation":"supports"}`, idB, idC)
	hubID := slowWriteKnowledge(t, st, term+" 허브", rels)
	slowWriteKnowledge(t, st, term+" 역방향 D", fmt.Sprintf(`{"to":%d,"relation":"duplicates"}`, hubID))

	var before, after strings.Builder
	if code := RunRead(ctx, st, []string{term}, strings.NewReader(""), &before); code != 0 {
		t.Fatalf("링크 조회 전 read exit=%d: %s", code, before.String())
	}
	if code := RunRead(ctx, st, []string{term, "--related", strconv.Itoa(hubID)}, strings.NewReader(""), &after); code != 0 {
		t.Fatalf("read --related exit=%d: %s", code, after.String())
	}
	got := after.String()
	if !strings.HasPrefix(got, before.String()) {
		t.Fatalf("지식 직렬화 불변 위반(실DB):\n전=%q\n후=%q", before.String(), got)
	}
	for _, frag := range []string{
		fmt.Sprintf("related|%d|refines|forward|%s 정방향 B|", idB, term),
		fmt.Sprintf("related|%d|supports|forward|%s 정방향 C|", idC, term),
		fmt.Sprintf("|duplicates|reverse|%s 역방향 D|", term),
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("이웃 행 %q 부재: %q", frag, got)
		}
	}

	var jout strings.Builder
	if code := RunRead(ctx, st, []string{"--json", term, "--related", strconv.Itoa(hubID)}, strings.NewReader(""), &jout); code != 0 {
		t.Fatalf("read --related --json exit=%d: %s", code, jout.String())
	}
	if !strings.Contains(jout.String(), `"related_hits":3`) {
		t.Errorf("related_hits=3 라인 누락: %q", jout.String())
	}
}

func TestSlowReadRelatedCapAndMissingV1240(t *testing.T) {
	st := t5ScratchStore(t)
	ctx := context.Background()
	const term = "v1240 데모 상한"
	hubID := slowWriteKnowledge(t, st, term+" 허브", "")
	for i := 0; i < 7; i++ {
		slowWriteKnowledge(t, st, fmt.Sprintf("%s 이웃 %d", term, i),
			fmt.Sprintf(`{"to":%d,"relation":"refines"}`, hubID))
	}
	var out strings.Builder
	if code := RunRead(ctx, st, []string{term, "--related", strconv.Itoa(hubID)}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("read --related exit=%d: %s", code, out.String())
	}
	n := 0
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if strings.HasPrefix(line, "related|") {
			n++
		}
	}
	if n != query.RelatedMaxRows {
		t.Errorf("related 행 = %d, want %d(7행 중 상한): %q", n, query.RelatedMaxRows, out.String())
	}

	var mout strings.Builder
	if code := RunRead(ctx, st, []string{term, "--related", "999999"}, strings.NewReader(""), &mout); code != 3 {
		t.Fatalf("부재 대상 exit = %d, want 3: %s", code, mout.String())
	}

	lonelyID := slowWriteKnowledge(t, st, term+" 고립 행", "")
	var lout strings.Builder
	if code := RunRead(ctx, st, []string{term, "--related", strconv.Itoa(lonelyID)}, strings.NewReader(""), &lout); code != 0 {
		t.Fatalf("0건 링크 exit = %d, want 0: %s", code, lout.String())
	}
	if strings.Contains(lout.String(), "related|") {
		t.Errorf("0건 링크에 related 행 출력: %q", lout.String())
	}
}
