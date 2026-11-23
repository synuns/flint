package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

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
