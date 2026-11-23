package cli

import (
	"context"
	"strings"
	"testing"
)

type sessionStartRecorder struct {
	*fakeStore
	starts []string
}

func (s *sessionStartRecorder) RecordSessionStart(ctx context.Context, session string) error {
	s.starts = append(s.starts, session)
	return nil
}

func TestSessionStartRecordsBoundary(t *testing.T) {
	st := &sessionStartRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {

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
	if code := runSessionEnd(context.Background(), st,
		[]string{"--session", "auto-label-1"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("session-end exit=%d: %s", code, out2.String())
	}
	if _, ok := ReadSessionLabel(); ok {
		t.Fatal("session-end 후 라벨 잔존(스테일)")
	}
}

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
