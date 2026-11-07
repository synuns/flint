// sessionhook_v0200_test.go — v0.20.0 D: session-start 훅이 세션 시작
// 경계를 기록한다(출력·exit 불변 — 내부 계정만 추가).
package cli

import (
	"context"
	"strings"
	"testing"
)

// sessionStartRecorder — SessionStartRecorder seam을 녹음하는 fake.
type sessionStartRecorder struct {
	*fakeStore
	starts []string
}

func (s *sessionStartRecorder) RecordSessionStart(ctx context.Context, session string) error {
	s.starts = append(s.starts, session)
	return nil
}

// TestSessionStartRecordsBoundary — Review Focus: session-start가 --session
// 라벨로 경계를 1회 기록한다. 기록 실패가 없어도 hot·nudge 출력은 불변
// (기록은 출력 경로와 독립).
func TestSessionStartRecordsBoundary(t *testing.T) {
	st := &sessionStartRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		// hot 헤드라인 경로 — 카운트 0으로 최소 동작.
		return nil, nil
	}}}
	var out strings.Builder
	code := runSessionStart(context.Background(), st,
		[]string{"--session", "sess-42"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	if len(st.starts) != 1 || st.starts[0] != "sess-42" {
		t.Errorf("기록된 경계 = %v, want [sess-42]", st.starts)
	}
}

// TestSessionStartNoSessionLabel — 라벨 미지정(빈) 훅도 빈 라벨로 기록
// (경계 시각 자체가 원천 — 라벨은 보조).
func TestSessionStartNoSessionLabel(t *testing.T) {
	st := &sessionStartRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}}
	var out strings.Builder
	code := runSessionStart(context.Background(), st, nil, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out.String())
	}
	if len(st.starts) != 1 || st.starts[0] != "" {
		t.Errorf("기록된 경계 = %v, want 1건(빈 라벨)", st.starts)
	}
}

// TestSessionLabelLifecycle — v0.20.5(B②): session-start가 라벨 파일을
// 남기고 session-end가 지운다(스테일 방지).
func TestSessionLabelLifecycle(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &sessionStartRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}}
	var out strings.Builder
	if code := runSessionStart(context.Background(), st,
		[]string{"--session", "auto-label-1"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("session-start exit=%d: %s", code, out.String())
	}
	if got, ok := ReadSessionLabel(); !ok || got != "auto-label-1" {
		t.Fatalf("라벨 = (%q %v), want auto-label-1", got, ok)
	}
	var out2 strings.Builder
	if code := runSessionEnd(context.Background(), st, nil, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("session-end exit=%d: %s", code, out2.String())
	}
	if _, ok := ReadSessionLabel(); ok {
		t.Fatal("session-end 후 라벨 잔존(스테일)")
	}
}

// TestReadAutoSessionLabel — v0.20.5(B②): --session 미지정 read가 현재
// 세션 라벨을 자동 채택해 read_events에 기록한다.
func TestReadAutoSessionLabel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	if err := WriteSessionLabel("auto-sess-42"); err != nil {
		t.Fatalf("WriteSessionLabel: %v", err)
	}
	st := &readEventRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			return hitRowsFixture[:1], nil
		case strings.Contains(sql, "SELECT value FROM meta"):
			return [][]any{{`{"calls":1,"zero":0}`}}, nil
		}
		return nil, nil
	}}}
	var out strings.Builder
	if code := runRead(context.Background(), st, []string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("read exit=%d: %s", code, out.String())
	}
	if len(st.events) != 1 || st.events[0].session != "auto-sess-42" {
		t.Fatalf("이벤트 = %+v, want session auto-sess-42(자동 채택)", st.events)
	}
}
