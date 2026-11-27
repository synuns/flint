package knowledge

import (
	"context"
	"strings"
	"testing"
)

type sessionTasksFake struct {
	*fakeStore
	summaryID    int
	summaryTasks string
}

func (f *sessionTasksFake) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	f.summaryID = id
	f.summaryTasks = tasks
	return 1, nil
}

func TestDoneStoresTasksFromStdin(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	stubAutoBackup(t)
	st := &fakeStore{}
	var out strings.Builder
	stdin := `{"hook_event_name":"Stop","compact_summary":"합성 데모 세션 요약 본문","tasks":"[{\"task\":\"a\"}]"}`
	code := RunDone(context.Background(), st, []string{"--session", "claude"},
		strings.NewReader(stdin), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	sessions := 0
	for _, rec := range st.inserted {
		if rec["kind"] != "session" {
			continue
		}
		sessions++
		if rec["tasks"] != `[{"task":"a"}]` {
			t.Errorf("sessions.tasks = %v, want 실제 tasks 수신분", rec["tasks"])
		}
	}
	if sessions != 1 {
		t.Fatalf("세션 행 = %d건, want 1", sessions)
	}
	for _, rec := range st.inserted {
		if rec["kind"] == "knowledge" {
			t.Errorf("done 경로 지식 적립 발생: %v", rec)
		}
	}
}

func TestDoneStoresTasksViaUpdateSummary(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	stubAutoBackup(t)
	base := &fakeStore{}
	base.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM sessions") {
			return [][]any{{int32(42)}}, nil
		}
		return nil, nil
	}
	d := &sessionTasksFake{fakeStore: base, summaryID: -1}
	var out strings.Builder
	stdin := `{"tasks":"[{\"task\":\"합성 데모 과업\"}]"}`
	code := RunDone(context.Background(), d, []string{"--session", "claude"},
		strings.NewReader(stdin), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if d.summaryID != 42 {
		t.Fatalf("UpdateSessionSummary id = %d, want 42", d.summaryID)
	}
	if d.summaryTasks != `[{"task":"합성 데모 과업"}]` {
		t.Errorf("UpdateSessionSummary tasks = %q, want 수신분", d.summaryTasks)
	}
}

func TestCompactNoKnowledgeDeposit(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &fakeStore{}
	var out strings.Builder
	code := RunCompact(context.Background(), st, nil,
		strings.NewReader(`{"hook_event_name":"PostCompact","session_id":"s-demo","compact_summary":"합성 데모용 컴팩트 요약 본문 — 세션 행만 적립된다"}`), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	sessions, knowledge := 0, 0
	for _, rec := range st.inserted {
		switch rec["kind"] {
		case "session":
			sessions++
		case "knowledge":
			knowledge++
		}
	}
	if knowledge != 0 {
		t.Fatalf("compact 지식 적립 = %d건, want 0", knowledge)
	}
	if sessions != 1 {
		t.Fatalf("세션 행 = %d건, want 1", sessions)
	}
}
