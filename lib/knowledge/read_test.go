// read_test.go — read 계약
// Review Focus(제로히트 exit 0·--session 자동 라벨·as-of). 계약
// 불변: eligible(현재 시점·무상태필터) 제로히트는 텍스트 완전 무출력·
// --json은 {"hits":0} 한 줄, exit 0,
// --session 미지정은 wire.ReadSessionLabel 라벨 조용히 채택, --as-of는
// 결과 뒤 as_of 한 줄 JSON. asof 스펙 파서는 cli asof.go 소유라
// ParseAsOfSpecFn 주입 seam으로 대역 주입(실구현 경로는 cli 잔류
// read_v08_test.go가 SUPERLITE_REAL=1로 담당).
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// readEventRecorder — fakeStore에 ReadEventStore seam을 얹은 fake
// (cli read_v0190_test.go 관례 이동분). RecordReadUsage도 흡수.
type readEventRecorder struct {
	*fakeStore
	events []readEventRecord
}

// readEventRecord — RecordReadEvent 호출 인자 녹음.
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

// bytesRecorder — fakeStore에 BytesRecorder seam을 얹은 fake(G5 바이트
// 계측). RecordBytes 호출 인자를 녹음한다(패키지 공유 — hot·
// brief 회귀가 같은 fake를 쓴다).
type bytesRecorder struct {
	*fakeStore
	recorded []bytesCall
}

// bytesCall — RecordBytes 호출 인자 녹음.
type bytesCall struct {
	kind string
	n    int
}

func (b *bytesRecorder) RecordBytes(ctx context.Context, kind string, n int) error {
	b.recorded = append(b.recorded, bytesCall{kind, n})
	return nil
}

// TestRunReadRecordsBytes — Review Focus: read 출력 직후
// len(최종 출력)을 kind="read"로 기록 — 제로히트 0바이트도 기록한다
// (호출 수 분모 정합, spec §1).
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
			return nil, nil // 제로히트 — 폴백·제안 스캔도 전부 0행
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

// injectAsOfSeams — ParseAsOfSpecFn·ErrInvalidAsOf 주입 점에 고정 해시
// 대역을 주입(스펙 "7d" → 해시 1건 확정). 원복은 t.Cleanup.
func injectAsOfSeams(t *testing.T, hash string) {
	t.Helper()
	savedFn, savedErr := ParseAsOfSpecFn, ErrInvalidAsOf
	ParseAsOfSpecFn = func(ctx context.Context, st wire.Store, spec string) (string, error) {
		return hash, nil
	}
	ErrInvalidAsOf = errors.New("invalid as-of spec")
	t.Cleanup(func() { ParseAsOfSpecFn, ErrInvalidAsOf = savedFn, savedErr })
}

// hitRowsFixture — 히트 read의 active_knowledge 행(9열, cli 관례 승계).
var hitRowsFixture = [][]any{
	{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"},
	{"2", "백업 절차", "매일", ":b:", "", "", "0.9", "active", "0"},
}

// TestRunReadZeroHitExit0Silent — Review Focus: eligible
// (현재 시점·무상태 필터) 제로히트는 텍스트 완전 무출력(침묵 유지)에
// exit 0(기존 1) — 제로히트는 오류가 아니므로 에이전트 루프가 끊기지
// 않는다.
func TestRunReadZeroHitExit0Silent(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // 제로히트 — 폴백·제안 스캔도 전부 0행
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

// TestRunReadZeroHitJSONHits — Review Focus: --json 제로히트는
// hits:0 한 줄 JSON({"hits":0}) + exit 0 — 호출자가 히트 수를 기계적으로
// 판별한다. 텍스트 모드와 달리 침묵하지 않는다.
func TestRunReadZeroHitJSONHits(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // 제로히트 — 폴백·제안 스캔도 전부 0행
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

// TestRunReadSessionAutoLabel — Review Focus: --session 미지정 시 현재
// 세션 라벨 파일(wire.ReadSessionLabel)을 조용히 채택해 read_events에
// 기록. 라벨 파일은 wire.ConfigDir seam 임시 디렉터리로 세운다.
func TestRunReadSessionAutoLabel(t *testing.T) {
	dir := t.TempDir()
	// 라벨 원천은 세션별 파일
	// session-<id>.label 단일 경로다 — 구 session-current는 더 이상
	// 읽히지 않는다.
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

// TestRunReadAsOfAddsField — Review Focus: --as-of 지정 시 기존 행 출력
// 뒤 as_of{hash,commit_date} 한 줄 JSON 추가, 질의는 AS OF 인라인.
// dolt_log commit_date 조회는 queryFn 대역.
func TestRunReadAsOfAddsField(t *testing.T) {
	injectAsOfSeams(t, "he08irluqn3v2m1k9x7c4jw5z6b8d0f")
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			return [][]any{{"3", "토픽", "요약", ":t:", "", "", "0.9", "active", "0"}}, nil
		case strings.Contains(sql, "dolt_log"):
			return [][]any{{"2026-10-20T12:00:00Z"}}, nil
		case strings.Contains(sql, "SELECT value FROM meta"):
			return nil, nil
		}
		return nil, nil
	}}
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
	var inlined int
	for _, q := range st.queries {
		if strings.Contains(q, "FROM active_knowledge AS OF 'he08irluqn3v2m1k9x7c4jw5z6b8d0f'") {
			inlined++
		}
	}
	if inlined == 0 {
		t.Errorf("AS OF 인라인 질의 미관찰: %q", st.queries)
	}
}

// TestRunReadAsOfInvalidExit3 — 무효 스펙은 exit 3 한 줄 JSON
// invalid_as_of(ErrInvalidAsOf 주입점 경유 판정).
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

// TestReadFieldsValidation — --fields는 알려진 컬럼만
// 허용. 오타("sumary")는 exit 3, 정상 필드는 통과.
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
	// 정상 필드는 통과(회귀 — 기존 사용 형태).
	var out2 strings.Builder
	code2 := RunRead(context.Background(), st,
		[]string{"백업", "--fields", "topic,confidence"}, strings.NewReader(""), &out2)
	if code2 == 3 {
		t.Fatalf("정상 필드 exit 3: %s", out2.String())
	}
}

// TestReadRRFKInjectionBeforeReorder — rrf_k 주입
// (wire.LoadThresholdsFn seam)은 폴백 재랭킹 앞에서 호출된다.
func TestReadRRFKInjectionBeforeReorder(t *testing.T) {
	var order []string
	savedLT := wire.LoadThresholdsFn
	wire.LoadThresholdsFn = func(ctx context.Context, st wire.Store) (store.Thresholds, error) {
		order = append(order, "loadThresholds")
		return store.DefaultThresholds(), nil
	}
	t.Cleanup(func() { wire.LoadThresholdsFn = savedLT })
	// 폴백 경로 read(제로히트 구문 → OR 재쿼리 히트).
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

// TestReadUnknownFlagExit3 — 미인식 '-' 접두 인자는 쿼리로
// 흡수하지 않고 무효 인자 exit 3(read --jsonn이 조용히 제로히트로
// 흘러 에이전트가 오타를 못 알아채던 사각 폐지). positional 쿼리·"--"
// end-of-options 흡수는 불변.
func TestReadUnknownFlagExit3(t *testing.T) {
	var out strings.Builder
	if code := RunRead(context.Background(), &fakeStore{}, []string{"--jsonn"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out.String())
	}
	// unknown flag류는 error:"usage".
	if !strings.Contains(out.String(), `"error":"usage"`) {
		t.Fatalf("에러 JSON error:usage 아님: %q", out.String())
	}
	// positional 쿼리 불변 — '-' 비접두는 여전히 쿼리 텍스트로 흡수.
	out.Reset()
	if code := RunRead(context.Background(), &fakeStore{}, []string{"docker"}, strings.NewReader(""), &out); code == 3 {
		t.Fatalf("positional 쿼리가 exit 3: %s", out.String())
	}
	// "--" 이후는 전부 positional — '--jsonn'도 쿼리로 흡수(N9 불변).
	out.Reset()
	if code := RunRead(context.Background(), &fakeStore{}, []string{"--", "--jsonn"}, strings.NewReader(""), &out); code == 3 {
		t.Fatalf("'--' 이후 쿼리가 exit 3: %s", out.String())
	}
}

// TestReadLimitClampJSONLine — Review Focus: --json에서
// 요청 limit이 상한(50)을 초과하면 additive 필드 limit_requested·
// limit_applied를 한 줄 추가해 clamp 사실을 밝힌다(기존 키 불변).
// 상한 이하·텍스트 모드는 기존 형식 그대로 — clamp 라인 없음.
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
		if len(lines) != 3 { // 히트 2행 + clamp 라인 1
			t.Fatalf("출력 라인 수 = %d, want 3(히트 2 + clamp 1): %q", len(lines), out.String())
		}
		var clamp map[string]int
		if err := json.Unmarshal([]byte(lines[2]), &clamp); err != nil {
			t.Fatalf("clamp 라인 JSON 아님: %q", lines[2])
		}
		if clamp["limit_requested"] != 99 || clamp["limit_applied"] != 50 {
			t.Fatalf("clamp 필드 = %v, want limit_requested:99·limit_applied:50", clamp)
		}
		// 기존 키 불변 — 히트 행은 id·topic·summary 그대로.
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

// ── 제안(SuggestSimilar) 입력 상한 truncate. ──

// TestSuggestQueryTruncate — G4 Review Focus: 제안 입력은 상한(suggestQueryMaxRunes
// rune)으로 truncate된다(멀티바이트 안전 — TruncateRunes 단일 구현 승계).
// 본문 검색 쿼리 자체는 불변(상한은 제안 유사도 스캔에만 적용).
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

// ── 계측 분리 커밋 — read/hot 완료 경로의 telemetry 커밋. ──

// telemetryCommitter — fakeStore에 TelemetryCommitter seam을 얹은 fake
// CommitTelemetry 호출 인자(cmd)를 녹음한다.
type telemetryCommitter struct {
	*fakeStore
	commands []string
	err      error
}

func (tc *telemetryCommitter) CommitTelemetry(ctx context.Context, cmd string) error {
	tc.commands = append(tc.commands, cmd)
	return tc.err
}

// TestRunReadCommitsTelemetryOnce — Review Focus: read 완료
// 경로는 계측 커밋을 정확히 1회(cmd="read") 호출한다. 실패는 조용
// (§13 — exit 0·출력 불변). 미구현 Store(fakeStore 단독)은 스킵.
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

// TestRunHotCommitsTelemetryOnce — Review Focus: hot 완료 경로도
// 계측 커밋을 1회(cmd="hot") 호출한다(헤드라인·--full 양 경로 공통).
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

// TestRunDoneCommitsTelemetryOnce — Review Focus: done 완료
// 경로는 기존 감사 Commit(session-end) 후 계측 커밋을 1회(cmd="done")
// 호출한다. 실패는 조용(exit 0·출력 불변).
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

// telemetryDoneFake — doneFake에 TelemetryCommitter seam을 얹은 fake.
type telemetryDoneFake struct {
	*doneFake
	telemetryCmds []string
	telErr        error
}

func (d *telemetryDoneFake) CommitTelemetry(ctx context.Context, cmd string) error {
	d.telemetryCmds = append(d.telemetryCmds, cmd)
	return d.telErr
}

// TestRunReadSuggestHugeQueryPerf — G4 가용성 회귀: 100KB 쿼리 제안이
// 제안 스캔 행(2k)이 있어도 상한 truncate로 수 초 이내에 끝난다(수정 전
// 17s급 — bigramDice가 행마다 쿼리 bigram 집합을 재구축했다). 단위 테스트
// 상한 5s(환경 편차 흡수 — 실측은 t.Log).
func TestRunReadSuggestHugeQueryPerf(t *testing.T) {
	scanRows := make([][]any, 0, 2000)
	for i := 1; i <= 2000; i++ {
		scanRows = append(scanRows, []any{
			int32(i), fmt.Sprintf("demo suggest %04d", i), "합성 데모 요약 — 제안 스캔 행.",
		})
	}
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT id, topic, summary FROM active_knowledge") {
			return scanRows, nil // 제안 스캔 행 존재
		}
		return nil, nil // 본문 구문·폴백은 제로히트
	}}
	q := strings.Repeat("쿼", 51200) // 100KB(UTF-8 3바이트 × 51200 rune)
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

// ── 회상 강화 배선(recall_score+1·last_recalled_at=now) ──

// recallBumperRecorder — RecordKnowledgeReads·BumpRecallScores 호출을 녹음하는
// fake(KnowledgeReadsRecorder·RecallBumper seam 동시 구현). 실구현 경로
// (db.Repo.BumpRecallScores의 실DB 증분)는 lib/db reads_v1700_test가 든다.
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

// TestRunReadBumpsRecallScores — Review Focus: read 히트
// 행 전수가 회상 강화(RecallBumper seam)를 1회 통과한다 — RecordKnowledgeReads
// 경로에 결합(kind=read).
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

// TestRecordKnowledgeReadsHotNoBump — hot 주입(kind=hot)은 회상 강화에서
// 제외 — 노출→강화→재노출 순환(hot 랭킹이 recall_score를 소비하므로)을
// 끊는 룰링. 이력 기록(knowledge_reads)은 불변.
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
