package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

type resolveCall struct{ id, by int }

type stateStoreFake struct {
	fakeStore

	openCalls  []map[string]any
	openStates []string
	openErr    error

	listRows []map[string]any

	resolveCalls []resolveCall
	resolveErr   error
}

func (f *stateStoreFake) OpenWorkState(ctx context.Context, rec map[string]any) (int, string, error) {
	f.openCalls = append(f.openCalls, rec)
	if f.openErr != nil {
		return 0, "", f.openErr
	}
	status := "open"
	if len(f.openStates) > 0 {
		status = f.openStates[0]
		f.openStates = f.openStates[1:]
	}
	return len(f.openCalls), status, nil
}

func (f *stateStoreFake) ResolveWorkState(ctx context.Context, id int, byKnowledgeID int) error {
	f.resolveCalls = append(f.resolveCalls, resolveCall{id, byKnowledgeID})
	return f.resolveErr
}

func (f *stateStoreFake) ListOpenWorkState(ctx context.Context, projectID int, limit int) []map[string]any {
	return f.listRows
}

func stateLines(t *testing.T, out *bytes.Buffer) []string {
	t.Helper()
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestStateOpenListResolveCLI(t *testing.T) {
	f := &stateStoreFake{
		openStates: []string{"open"},
		listRows: []map[string]any{
			{"id": 1, "task": "fix path", "status": "open", "next_action": "rerun ci", "updated_at": "2026-11-26T00:00:00Z"},
		},
	}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		return [][]any{{int64(2), "old task", "resolved"}}, nil
	}
	ctx := context.Background()

	var out bytes.Buffer
	stdin := strings.NewReader(`{"agent":"claude\n"}`)
	if code := RunState(ctx, f, []string{"open", "--task", "  Fix: PATH! ", "--detail", "d", "--next", "rerun ci", "--project", "3"}, stdin, &out); code != 0 {
		t.Fatalf("open exit=%d, want 0; out=%s", code, out.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
		t.Fatalf("open 출력이 단일 JSON 아님: %q (%v)", out.String(), err)
	}
	if m["id"] != float64(1) || m["status"] != "open" {
		t.Fatalf("open out=%q, want {id:1,status:open}", out.String())
	}
	rec := f.openCalls[0]
	if rec["task"] != "  Fix: PATH! " {
		t.Errorf("open rec task=%v, want 원문 전달(정규화는 서버)", rec["task"])
	}
	if rec["detail"] != "d" || rec["next_action"] != "rerun ci" || rec["project_id"] != 3 {
		t.Errorf("open rec=%v", rec)
	}
	if rec["agent"] != "claude" {
		t.Errorf("open rec agent=%v, want sanitize 통과 claude", rec["agent"])
	}

	out.Reset()
	if code := RunState(ctx, f, []string{"list"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("list exit=%d, want 0; out=%s", code, out.String())
	}
	lines := stateLines(t, &out)
	if len(lines) != 1 || !strings.Contains(lines[0], "fix path") || strings.Contains(out.String(), "resolved") {
		t.Fatalf("list out=%q, want open 1행", out.String())
	}

	out.Reset()
	if code := RunState(ctx, f, []string{"list", "--all"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("list --all exit=%d, want 0; out=%s", code, out.String())
	}
	lines = stateLines(t, &out)
	if len(lines) != 2 || !strings.Contains(lines[1], "resolved") {
		t.Fatalf("list --all out=%q, want open+resolved 2행", out.String())
	}

	out.Reset()
	if code := RunState(ctx, f, []string{"resolve", "--id", "1", "--by", "9"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("resolve exit=%d, want 0; out=%s", code, out.String())
	}
	if got := f.resolveCalls[0]; got.id != 1 || got.by != 9 {
		t.Fatalf("resolve 호출=(%d,%d), want (1,9)", got.id, got.by)
	}
	var rm map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &rm); err != nil || rm["status"] != "resolved" || rm["id"] != float64(1) {
		t.Fatalf("resolve out=%q, want {id:1,status:resolved}", out.String())
	}

	h, ok := wire.Lookup("state")
	if !ok {
		t.Fatal("state 미등록")
	}
	out.Reset()
	if code := h(ctx, f, []string{"list"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("registry 경유 list exit=%d, want 0", code)
	}
}

func TestStateUsageAndNotFoundExits(t *testing.T) {
	f := &stateStoreFake{resolveErr: db.ErrNotFound}
	ctx := context.Background()
	var out bytes.Buffer

	if code := RunState(ctx, f, []string{"open"}, strings.NewReader(""), &out); code != 3 {
		t.Errorf("--task 부재 exit=%d, want 3; out=%s", code, out.String())
	}
	out.Reset()
	if code := RunState(ctx, f, []string{"open", "--task", ""}, strings.NewReader(""), &out); code != 3 {
		t.Errorf("빈 task exit=%d, want 3; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"error":"usage"`) {
		t.Errorf("빈 task usage 에러 JSON 부재: %s", out.String())
	}
	out.Reset()
	if code := RunState(ctx, f, []string{"open", "--task", "   "}, strings.NewReader(""), &out); code != 3 {
		t.Errorf("공백 task exit=%d, want 3; out=%s", code, out.String())
	}
	if code := RunState(ctx, f, nil, strings.NewReader(""), &out); code != 3 {
		t.Errorf("빈 인자 exit=%d, want 3", code)
	}
	if code := RunState(ctx, f, []string{"bogus"}, strings.NewReader(""), &out); code != 3 {
		t.Errorf("미지 subcommand exit=%d, want 3", code)
	}
	if code := RunState(ctx, f, []string{"resolve", "--id", "99"}, strings.NewReader(""), &out); code != 1 {
		t.Errorf("not found resolve exit=%d, want 1; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"error":"not_found"`) {
		t.Errorf("not_found JSON 부재: %s", out.String())
	}

	f2 := &stateStoreFake{openErr: errors.New("db down")}
	out.Reset()
	if code := RunState(ctx, f2, []string{"open", "--task", "t"}, strings.NewReader(""), &out); code != 1 {
		t.Errorf("스토어 오류 exit=%d, want 1", code)
	}
}

func TestUsageIncludesState(t *testing.T) {
	found := false
	for _, n := range wire.UsageOrder() {
		if n == "state" {
			found = true
		}
	}
	if !found {
		t.Fatal("usageOrder에 state 미포함")
	}
}
