package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

func readDemoItems() []ReadItem {
	return []ReadItem{
		{ID: "7", Title: "demo topic alpha", Score: "0.80", Body: "demo summary alpha", Why: "demo+query · score 5"},
		{ID: "9", Title: "demo topic beta", Score: "0.40", Body: "demo summary beta", Why: ""},
	}
}

func readDepsWith(form func(*string) error, search func(context.Context, string) ([]ReadItem, error)) ReadDeps {
	return ReadDeps{
		IsTerminal: func() bool { return true },
		Form:       form,
		Search:     search,
		Display:    func(tea.Model) error { return nil },
	}
}

func noopFallback(t *testing.T) wire.Handler {
	t.Helper()
	return func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		t.Fatalf("fallback must not run in TUI entry (args=%v)", args)
		return 1
	}
}

func TestReadEntryInteractiveOnTTYNoArgs(t *testing.T) {
	orig := readIsTerminal
	readIsTerminal = func() bool { return true }
	defer func() { readIsTerminal = orig }()

	var queried string
	deps := readDepsWith(
		func(q *string) error { *q = "demo query"; return nil },
		func(ctx context.Context, q string) ([]ReadItem, error) {
			queried = q
			return readDemoItems(), nil
		},
	)
	var stdout bytes.Buffer
	code := ReadEntry(context.Background(), &fakeStore{}, nil, strings.NewReader(""), &stdout, noopFallback(t), deps)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s)", code, stdout.String())
	}
	if queried != "demo query" {
		t.Fatalf("search query = %q, want %q", queried, "demo query")
	}
}

func TestReadEntryNonTTYFallsBackUnchanged(t *testing.T) {
	orig := readIsTerminal
	readIsTerminal = func() bool { return false }
	defer func() { readIsTerminal = orig }()

	var gotArgs []string
	var gotOut io.Writer
	fallback := func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		gotArgs, gotOut = args, stdout
		return 7
	}
	stdin := strings.NewReader("demo stdin")
	var stdout bytes.Buffer
	args := []string{"demo", "--limit", "5"}
	code := ReadEntry(context.Background(), &fakeStore{}, args, stdin, &stdout, fallback, ReadDeps{})
	if code != 7 {
		t.Fatalf("exit = %d, want 7 (fallback 코드 무변경 전달)", code)
	}
	if fmt.Sprint(gotArgs) != fmt.Sprint(args) {
		t.Fatalf("fallback args = %v, want %v", gotArgs, args)
	}
	if gotOut != io.Writer(&stdout) {
		t.Fatal("fallback stdout 미전달")
	}
}

func TestReadEntryNoTUIEnvFallsBack(t *testing.T) {
	t.Setenv("FLINT_NO_TUI", "1")
	orig := readIsTerminal
	readIsTerminal = func() bool { return true }
	defer func() { readIsTerminal = orig }()

	fallback := func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		return 5
	}
	var stdout bytes.Buffer
	code := ReadEntry(context.Background(), &fakeStore{}, nil, strings.NewReader(""), &stdout, fallback, ReadDeps{})
	if code != 5 {
		t.Fatalf("exit = %d, want 5 (FLINT_NO_TUI 폴백)", code)
	}
}

func TestReadEntryFlagCallFallsBack(t *testing.T) {
	orig := readIsTerminal
	readIsTerminal = func() bool { return true }
	defer func() { readIsTerminal = orig }()

	for _, args := range [][]string{{"demo"}, {"--limit", "5"}, {"--json"}} {
		fallback := func(ctx context.Context, st wire.Store, a []string, stdin io.Reader, stdout io.Writer) int {
			return 4
		}
		var stdout bytes.Buffer
		code := ReadEntry(context.Background(), &fakeStore{}, args, strings.NewReader(""), &stdout, fallback, ReadDeps{})
		if code != 4 {
			t.Fatalf("args=%v exit = %d, want 4 (플래그/쿼리 폴백)", args, code)
		}
	}
}

type readEventRecorder struct {
	*fakeStore
	events []db.ReadEvent
}

func (r *readEventRecorder) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	r.events = append(r.events, ev)
	return nil
}

func readRowsStore() *readEventRecorder {
	base := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM recall_knowledge") || strings.Contains(sql, "FROM active_knowledge") {
			return [][]any{
				{7, "demo topic alpha", "demo summary alpha", ":demo:", "demo-entity", "demo-domain", 0.8, "active", 3},
			}, nil
		}
		return nil, nil
	}}
	return &readEventRecorder{fakeStore: base}
}

func TestRunReadTUIPreservesReadEventInstrumentation(t *testing.T) {
	if reflect.ValueOf(readRunner).Pointer() != reflect.ValueOf(knowledge.RunRead).Pointer() {
		t.Fatalf("readRunner seam must default to knowledge.RunRead")
	}
	orig := readIsTerminal
	readIsTerminal = func() bool { return true }
	defer func() { readIsTerminal = orig }()

	st := readRowsStore()
	deps := ReadDeps{
		IsTerminal: func() bool { return true },
		Form:       func(q *string) error { *q = "demo query"; return nil },
		Display:    func(tea.Model) error { return nil },
	}
	var stdout bytes.Buffer
	code := RunReadTUI(context.Background(), st, &stdout, deps)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s)", code, stdout.String())
	}
	if len(st.events) != 1 {
		t.Fatalf("RecordReadEvent calls = %d, want 1 (계측 보존)", len(st.events))
	}
	if st.events[0].Hits != 1 || st.events[0].IDs != "7" {
		t.Errorf("event = %+v, want hits 1 · ids 7", st.events[0])
	}
}

func TestSearchReadWiresRunReadArgs(t *testing.T) {
	origRunner := readRunner
	defer func() { readRunner = origRunner }()
	var got [][]string
	var sink string
	readRunner = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		got = append(got, args)
		fmt.Fprint(stdout, sink)
		return 0
	}
	sink = `{"id":"7","topic":"demo topic alpha","summary":"demo summary alpha","confidence":"0.8"}` + "\n" +
		`{"id":"7","tokens":"demo+query","token_score":5}` + "\n" +
		`{"vault_hits":0}` + "\n"

	items, err := searchRead(context.Background(), &fakeStore{}, "demo query")
	if err != nil {
		t.Fatalf("searchRead: %v", err)
	}
	want := []string{"demo query", "--json", "--why", "--fields", "id,topic,summary,confidence"}
	if len(got) != 1 || fmt.Sprint(got[0]) != fmt.Sprint(want) {
		t.Fatalf("RunRead args = %v, want %v", got, want)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.ID != "7" || it.Title != "demo topic alpha" || it.Score != "0.8" {
		t.Errorf("item fields = %+v", it)
	}
	if !strings.Contains(it.Body, "demo summary alpha") {
		t.Errorf("body = %q", it.Body)
	}
	if !strings.Contains(it.Why, "demo+query") {
		t.Errorf("why = %q, want token evidence attached", it.Why)
	}
}

func TestRunReadTUINoResults(t *testing.T) {
	orig := readIsTerminal
	readIsTerminal = func() bool { return true }
	defer func() { readIsTerminal = orig }()

	deps := readDepsWith(
		func(q *string) error { *q = "demo query"; return nil },
		func(context.Context, string) ([]ReadItem, error) { return nil, nil },
	)
	var stdout bytes.Buffer
	code := RunReadTUI(context.Background(), &fakeStore{}, &stdout, deps)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "no results") {
		t.Errorf("stdout = %q, want no-results notice", stdout.String())
	}
}

func TestRunReadTUIEmptyQuery(t *testing.T) {
	orig := readIsTerminal
	readIsTerminal = func() bool { return true }
	defer func() { readIsTerminal = orig }()

	deps := readDepsWith(func(q *string) error { return nil }, nil)
	var stdout bytes.Buffer
	code := RunReadTUI(context.Background(), &fakeStore{}, &stdout, deps)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "empty query") {
		t.Errorf("stdout = %q, want empty-query notice", stdout.String())
	}
}

func TestRunReadTUINonTerminal(t *testing.T) {
	orig := readIsTerminal
	readIsTerminal = func() bool { return false }
	defer func() { readIsTerminal = orig }()

	var stdout bytes.Buffer
	code := RunReadTUI(context.Background(), &fakeStore{}, &stdout, ReadDeps{})
	if code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	if !strings.Contains(stdout.String(), "TUI requires a terminal") {
		t.Errorf("stdout = %q, want terminal fallback notice", stdout.String())
	}
}

func updateRead(m ReadModel, msg tea.Msg) ReadModel {
	m2, _ := m.Update(msg)
	return m2.(ReadModel)
}

func TestReadModelListDetailQuit(t *testing.T) {
	m := NewReadModel(readDemoItems())
	m = updateRead(m, sizeMsg(80, 24))
	v := m.View()
	for _, want := range []string{"demo topic alpha", "score 0.80"} {
		if !strings.Contains(v, want) {
			t.Errorf("list view missing %q\nview:\n%s", want, v)
		}
	}
	if m.mode != readModeList {
		t.Fatalf("initial mode = %v, want readModeList", m.mode)
	}
	m = updateRead(m, enterMsg())
	if m.mode != readModeDetail {
		t.Fatalf("after enter mode = %v, want readModeDetail", m.mode)
	}
	d := m.View()
	if !strings.Contains(d, "demo summary alpha") {
		t.Errorf("detail view missing body\nview:\n%s", d)
	}
	if !strings.Contains(d, "demo+query") {
		t.Errorf("detail view missing why evidence\nview:\n%s", d)
	}
	m = updateRead(m, escMsg())
	if m.mode != readModeList {
		t.Fatalf("after esc mode = %v, want readModeList", m.mode)
	}
	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q returned nil cmd, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q cmd() = %T, want tea.QuitMsg", cmd())
	}
}
