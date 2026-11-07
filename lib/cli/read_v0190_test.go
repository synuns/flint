// read_v0190_test.go — v0.19.0 효과 계측 와이어링: read 1회가 read_events에
// 수치·플래그·세션 라벨로 기록된다(ReadEventStore seam, METRICS §4).
// fakeStore 질의 캡처 패턴은 read_v0150_test.go 승계.
package cli

import (
	"context"
	"strings"
	"testing"
)

// readEventRecorder — fakeStore에 ReadEventStore(v0.19.0 seam)를 얹은
// fake(readUsageRecorder 패턴 승계). RecordReadUsage도 흡수해 카운터
// 경로를 단순화한다. v0.20.0: ids(반환 id CSV) 녹음 추가.
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

func (r *readEventRecorder) RecordReadEvent(ctx context.Context, hits int, zero, near, fallback bool, session, ids string) error {
	r.events = append(r.events, readEventRecord{hits, zero, near, fallback, session, ids})
	return nil
}

// hitRowsFixture — 히트 read의 active_knowledge 행(9열, v0150 관례).
var hitRowsFixture = [][]any{
	{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"},
	{"2", "백업 절차", "매일", ":b:", "", "", "0.9", "active", "0"},
}

// TestReadEventRecordedOnHit — Review Focus: 히트 read는 결과 수·세션
// 라벨을 그대로 기록한다(hits=2, zero=false, fallback=false).
func TestReadEventRecordedOnHit(t *testing.T) {
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
	// v0.20.0: 반환 id CSV(상위 10) — 2행 히트이므로 "1,2".
	if ev.ids != "1,2" {
		t.Errorf("ids = %q, want 1,2", ev.ids)
	}
}

// TestReadEventRecordsFallback — Review Focus: 제로히트→폴백 재쿼리로
// 살아난 read는 fallback=true로 기록된다(폴백 플래그 전달).
func TestReadEventRecordsFallback(t *testing.T) {
	st := &readEventRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, " OR "):
			// 폴백 OR 재쿼리 — 1행 회수(제로히트 탈출).
			return [][]any{{"3", "백업 상태", "매일 점검", ":b:", "", "", "0.9", "active", "0"}}, nil
		case strings.Contains(sql, "SELECT value FROM meta"):
			return [][]any{{`{"calls":5,"zero":2}`}}, nil
		}
		return nil, nil // 본 쿼리 제로히트
	}}}
	var out strings.Builder
	// 다중 토큰 질의 — 단일 토큰은 폴백이 구문과 동질이라 미발동.
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

// TestReadSessionFlagValidation — Review Focus: 값 없는 trailing --session은
// 다른 값형 플래그와 동일 규칙 exit 3(N10/F1 관례). 플래그형 값
// (`--session --json` 흡수 오타)도 exit 3 — 라벨이 read_events에 14일
// 남는 만큼 DB 오염 방지가 조회 편의보다 우선(리뷰 #3).
func TestReadSessionFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--session"},           // 값 없음
		{"--session", "--json"}, // 플래그 흡수 오타
		{"--session", "-q"},     // 짧은 플래그형
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
