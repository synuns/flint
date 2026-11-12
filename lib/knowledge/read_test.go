// read_test.go — v0.39.0 T3: lib/cli/read_test.go 이동 + read 계약
// Review Focus(제로히트 침묵 exit 1·--session 자동 라벨·as-of). 계약
// 불변: eligible(현재 시점·무상태필터) 제로히트는 완전 무출력 exit 1,
// --session 미지정은 wire.ReadSessionLabel 라벨 조용히 채택, --as-of는
// 결과 뒤 as_of 한 줄 JSON. asof 스펙 파서는 cli asof.go 소유라
// ParseAsOfSpecFn 주입 seam으로 대역 주입(실구현 경로는 cli 잔류
// read_v08_test.go가 SUPERLITE_REAL=1로 담당).
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

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

// TestRunReadZeroHitSilentExit1 — Review Focus: eligible(현재 시점·무상태
// 필터) 제로히트는 완전 무출력(침묵 계약) exit 1 — flint가 에이전트에게
// 말을 걸지 않는다.
func TestRunReadZeroHitSilentExit1(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // 제로히트 — 폴백·제안 스캔도 전부 0행
	}}
	var out strings.Builder
	code := RunRead(context.Background(), st,
		[]string{"없는토픽"}, strings.NewReader(""), &out)
	if code != 1 {
		t.Fatalf("exit = %d, want 1(제로히트 침묵)", code)
	}
	if out.String() != "" {
		t.Errorf("제로히트 침묵 위반: %q", out.String())
	}
}

// TestRunReadSessionAutoLabel — Review Focus: --session 미지정 시 현재
// 세션 라벨 파일(wire.ReadSessionLabel)을 조용히 채택해 read_events에
// 기록. 라벨 파일은 wire.ConfigDir seam 임시 디렉터리로 세운다.
func TestRunReadSessionAutoLabel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/session-current", []byte("auto-label-7\n"), 0o644); err != nil {
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

// TestReadFieldsValidation — v0.20.2 F5 승계: --fields는 알려진 컬럼만
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

// TestReadRRFKInjectionBeforeReorder — v0.25.1 승계: rrf_k 주입
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
