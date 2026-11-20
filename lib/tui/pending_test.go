// pending_test.go — the pending review TUI contract tests. State transitions
// (list -> detail -> action) and action delegation (seam — asserting reuse of the
// existing RunApprove path), loaders (fake store injection), and the non-terminal
// fallback. SUPERLITE_REAL real-DB delegation is covered by the cli entry tests (pending_test.go, the withFake convention).
package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/wire"
)

// keyMsg — simulates a 1-rune key input.
func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func enterMsg() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func escMsg() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyEsc} }

// sizeMsg — simulates a terminal resize (in real runs, bubbletea always delivers
// 1 WindowSizeMsg at startup).
func sizeMsg(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

// demoItems — 3 synthetic demo pending items (no personal data — always synthetic).
func demoItems() []PendingItem {
	return []PendingItem{
		{ID: 7, Title: "demo topic alpha", Confidence: "0.80", Date: "2026-11-14", Body: "demo summary alpha", Origin: "hook"},
		{ID: 9, Title: "demo topic beta", Confidence: "0.40", Date: "2026-11-15", Body: "demo summary beta", Origin: "import:demo.jsonl"},
		{ID: 11, Title: "demo topic gamma", Confidence: "0.60", Date: "2026-11-15", Body: "demo summary gamma", Origin: "agent"},
	}
}

func demoTargets() []SupersedeTarget {
	return []SupersedeTarget{
		{ID: 3, Title: "demo active one"},
		{ID: 5, Title: "demo active two"},
	}
}

// recorderActions — a call-recording fake (the default fixture). The real
// implementation is defaultPendingActions wrapping knowledge.RunApprove.
type recorderActions struct {
	approves   []int
	supersedes [][2]int // {pendingID, oldID}
	expires    [][2]int // {pendingID, days} — the e action delegation record
}

func (r *recorderActions) actions() PendingActions {
	return PendingActions{
		Approve: func(ctx context.Context, id int) (int, string) {
			r.approves = append(r.approves, id)
			return 0, ""
		},
		Supersede: func(ctx context.Context, id, oldID int) (int, string) {
			r.supersedes = append(r.supersedes, [2]int{id, oldID})
			return 0, ""
		},
		Expire: func(ctx context.Context, id, days int) (int, string) {
			r.expires = append(r.expires, [2]int{id, days})
			return 0, ""
		},
	}
}

// staticTargets — a loader fake returning fixed candidates (the contract-3 lazy-load seam).
func staticTargets(ts []SupersedeTarget) func(context.Context) ([]SupersedeTarget, error) {
	return func(context.Context) ([]SupersedeTarget, error) { return ts, nil }
}

// update — a model-type-preserving helper (for test readability).
func update(m PendingModel, msg tea.Msg) PendingModel {
	m2, _ := m.Update(msg)
	return m2.(PendingModel)
}

// drive — returns the Update's model and cmd together (for running lazy-load cmds).
func drive(m PendingModel, msg tea.Msg) (PendingModel, tea.Cmd) {
	m2, cmd := m.Update(msg)
	return m2.(PendingModel), cmd
}

// RF1 — list: title, confidence, and date shown; enter -> detail (body, origin); esc -> back.
func TestPendingListShowsFieldsAndDetailTransition(t *testing.T) {
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), PendingActions{})
	m = update(m, sizeMsg(80, 24))
	v := m.View()
	for _, want := range []string{"demo topic alpha", "0.80", "2026-11-14"} {
		if !strings.Contains(v, want) {
			t.Errorf("list view missing %q\nview:\n%s", want, v)
		}
	}
	if m.mode != modeList {
		t.Fatalf("initial mode = %v, want modeList", m.mode)
	}
	m = update(m, enterMsg())
	if m.mode != modeDetail {
		t.Fatalf("after enter mode = %v, want modeDetail", m.mode)
	}
	d := m.View()
	if !strings.Contains(d, "demo summary alpha") {
		t.Errorf("detail view missing body\nview:\n%s", d)
	}
	if !strings.Contains(d, "hook") {
		t.Errorf("detail view missing origin\nview:\n%s", d)
	}
	m = update(m, escMsg())
	if m.mode != modeList {
		t.Fatalf("after esc mode = %v, want modeList", m.mode)
	}
}

// RF1 — the a-key approve delegation also works in the detail view (selection -> action transition).
func TestPendingDetailApproveKey(t *testing.T) {
	rec := &recorderActions{}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), rec.actions())
	m = update(m, enterMsg())
	m = update(m, keyMsg("a"))
	if len(rec.approves) != 1 || rec.approves[0] != 7 {
		t.Fatalf("approves = %v, want [7]", rec.approves)
	}
	if len(m.items) != 2 {
		t.Fatalf("items after approve = %d, want 2", len(m.items))
	}
}

// RF-state — a: approves the selected item by delegation + removes it from the list + a status message.
func TestPendingApproveDelegatesAndRemoves(t *testing.T) {
	rec := &recorderActions{}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), rec.actions())
	m = update(m, keyMsg("a"))
	if len(rec.approves) != 1 || rec.approves[0] != 7 {
		t.Fatalf("approves = %v, want [7] (first selected)", rec.approves)
	}
	if len(rec.supersedes) != 0 {
		t.Fatalf("supersedes = %v, want none", rec.supersedes)
	}
	if len(m.items) != 2 {
		t.Fatalf("items after approve = %d, want 2", len(m.items))
	}
	if !strings.Contains(m.View(), "approved") {
		t.Errorf("status after approve missing 'approved'\nview:\n%s", m.View())
	}
}

// RF-state — a failure (exit != 0): item kept + a failure status message (cause shown — contract 5).
func TestPendingApproveFailureKeepsItem(t *testing.T) {
	acts := PendingActions{
		Approve: func(ctx context.Context, id int) (int, string) { return 1, "not_found" },
	}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), acts)
	m = update(m, keyMsg("a"))
	if len(m.items) != 3 {
		t.Fatalf("items after failed approve = %d, want 3 (kept)", len(m.items))
	}
	if !strings.Contains(m.View(), "failed") {
		t.Errorf("status after failed approve missing 'failed'\nview:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "not_found") {
		t.Errorf("status after failed approve missing cause 'not_found'\nview:\n%s", m.View())
	}
}

// RF-cause — contract 5: if the seam returns no cause, the exit-code fallback display.
func TestPendingApproveFailureFallsBackToExitCode(t *testing.T) {
	acts := PendingActions{
		Approve: func(ctx context.Context, id int) (int, string) { return 3, "" },
	}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), acts)
	m = update(m, keyMsg("a"))
	if !strings.Contains(m.View(), "exit 3") {
		t.Errorf("status without cause should fall back to exit code\nview:\n%s", m.View())
	}
}

// RF-lazy — contract 3: s only schedules the load (loading targets...) -> the
// load-complete message switches to the candidate list -> enter confirms -> the (id, old) delegation.
func TestPendingSupersedeFlowDelegates(t *testing.T) {
	rec := &recorderActions{}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), rec.actions())
	m = update(m, sizeMsg(80, 24))
	m, cmd := drive(m, keyMsg("s"))
	if m.mode != modeList {
		t.Fatalf("after s mode = %v, want modeList until targets load", m.mode)
	}
	if !strings.Contains(m.View(), "loading targets...") {
		t.Errorf("status while loading missing 'loading targets...'\nview:\n%s", m.View())
	}
	if cmd == nil {
		t.Fatal("s must return a load cmd before targets are loaded")
	}
	m, _ = drive(m, cmd())
	if m.mode != modeSupersede {
		t.Fatalf("after load mode = %v, want modeSupersede", m.mode)
	}
	sv := m.View()
	if !strings.Contains(sv, "demo active one") {
		t.Errorf("supersede view missing candidate title\nview:\n%s", sv)
	}
	m = update(m, enterMsg())
	if len(rec.supersedes) != 1 || rec.supersedes[0] != [2]int{7, 3} {
		t.Fatalf("supersedes = %v, want [{7 3}] (pending 7 supersedes active 3)", rec.supersedes)
	}
	if m.mode != modeList {
		t.Fatalf("after supersede confirm mode = %v, want modeList", m.mode)
	}
	if len(m.items) != 2 {
		t.Fatalf("items after supersede = %d, want 2", len(m.items))
	}
	// contract 4: the superseded old (active 3) is removed from the candidates and targetList (no stale).
	if len(m.candidates) != 1 || m.candidates[0].ID != 5 {
		t.Fatalf("candidates after supersede = %+v, want only id 5", m.candidates)
	}
	if strings.Contains(m.targetList.View(), "demo active one") {
		t.Errorf("targetList still shows superseded candidate\nview:\n%s", m.targetList.View())
	}
}

// RF-lazy — contract 3: no candidates — after loading, a notice only; the list is kept.
func TestPendingSupersedeLazyLoadNoActive(t *testing.T) {
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(nil), PendingActions{})
	m, cmd := drive(m, keyMsg("s"))
	if cmd == nil {
		t.Fatal("s must return a load cmd")
	}
	m, _ = drive(m, cmd())
	if m.mode != modeList {
		t.Fatalf("mode after empty load = %v, want modeList", m.mode)
	}
	if !strings.Contains(m.View(), "no active items to supersede") {
		t.Errorf("status after empty load = %q\nview:\n%s", m.status, m.View())
	}
}

// RF-lazy — contract 3: load failure — the cause in status; the list kept (retry possible).
func TestPendingSupersedeLazyLoadError(t *testing.T) {
	loader := func(context.Context) ([]SupersedeTarget, error) {
		return nil, errors.New("demo store down")
	}
	m := NewPendingModel(context.Background(), demoItems(), loader, PendingActions{})
	m, cmd := drive(m, keyMsg("s"))
	m, _ = drive(m, cmd())
	if m.mode != modeList {
		t.Fatalf("mode after load error = %v, want modeList", m.mode)
	}
	if !strings.Contains(m.View(), "demo store down") {
		t.Errorf("status after load error missing cause\nview:\n%s", m.View())
	}
}

// RF-lazy — contract 3: re-entry — already-loaded candidates enter immediately without reloading.
func TestPendingSupersedeReenterSkipsReload(t *testing.T) {
	calls := 0
	loader := func(context.Context) ([]SupersedeTarget, error) {
		calls++
		return demoTargets(), nil
	}
	m := NewPendingModel(context.Background(), demoItems(), loader, PendingActions{})
	m, cmd := drive(m, keyMsg("s"))
	m, _ = drive(m, cmd())
	m = update(m, escMsg())
	m, cmd = drive(m, keyMsg("s"))
	if cmd != nil {
		t.Fatal("second s should not re-issue load cmd (cached)")
	}
	if m.mode != modeSupersede {
		t.Fatalf("mode after re-s = %v, want modeSupersede (cached)", m.mode)
	}
	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1 (cached after first load)", calls)
	}
}

// RF-state — s then esc: returns to the list without delegation (cancel).
func TestPendingSupersedeEscapeCancels(t *testing.T) {
	rec := &recorderActions{}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), rec.actions())
	m, cmd := drive(m, keyMsg("s"))
	m, _ = drive(m, cmd())
	m = update(m, escMsg())
	if m.mode != modeList {
		t.Fatalf("after esc mode = %v, want modeList", m.mode)
	}
	if len(rec.supersedes) != 0 {
		t.Fatalf("supersedes = %v, want none after cancel", rec.supersedes)
	}
}

// RF-state — d: skips to the next item without writing (selection moves; item kept).
func TestPendingSkipAdvancesWithoutAction(t *testing.T) {
	rec := &recorderActions{}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), rec.actions())
	m = update(m, keyMsg("d"))
	if len(rec.approves) != 0 || len(rec.supersedes) != 0 {
		t.Fatalf("actions after skip: approves=%v supersedes=%v, want none", rec.approves, rec.supersedes)
	}
	if len(m.items) != 3 {
		t.Fatalf("items after skip = %d, want 3 (kept)", len(m.items))
	}
	if got := m.items[m.list.Index()].ID; got != 9 {
		t.Fatalf("selected id after skip = %d, want 9 (second item)", got)
	}
}

// RF-state — q: returns the quit command.
func TestPendingQuitReturnsTeaQuit(t *testing.T) {
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), PendingActions{})
	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q returned nil cmd, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q cmd() = %T, want tea.QuitMsg", cmd())
	}
}

// On WindowSizeMsg, the list size fits the terminal, and on 12-row-or-fewer
// terminals and consecutive resizes the selection rendering
// is preserved (abolishing the fixed 80x20 — sized to the window bubbletea
// delivers at startup). Allocation: height >= 12 is height-6; low screens (< 12)
// subtract 1 chrome line, height-1 (the low-screen chrome-minimization contract).
func TestPendingResizeKeepsItemsRendered(t *testing.T) {
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), PendingActions{})
	for _, sz := range []tea.WindowSizeMsg{
		{Width: 60, Height: 24},
		{Width: 60, Height: 12}, // 12-row terminal (the threshold — the existing allocation)
		{Width: 40, Height: 8},  // low screen (chrome minimized)
		{Width: 100, Height: 30},
	} {
		want := sz.Height - 6
		if sz.Height < 12 {
			want = sz.Height - 1
		}
		m = update(m, sz)
		if m.list.Height() != want {
			t.Errorf("after %dx%d list height = %d, want %d", sz.Width, sz.Height, m.list.Height(), want)
		}
		if !strings.Contains(m.View(), "demo topic alpha") {
			t.Errorf("after %dx%d view lost item text\nview:\n%s", sz.Width, sz.Height, m.View())
		}
	}
}

// ── low-screen (height < 12) chrome minimization ──

// RF-numeric — heights 8 and 11: the list allocation grows to height-1 (subtracting
// only the 1 chrome line — key hints), and View omits the optional chrome (the
// non-interactive notice nonInteractiveHint, separator blanks, status). The q/ESC exit hint is kept even at 8 rows.
func TestPendingLowHeightMaximizesList(t *testing.T) {
	for _, sz := range []tea.WindowSizeMsg{{Width: 80, Height: 8}, {Width: 80, Height: 11}} {
		m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), PendingActions{})
		m = update(m, sz)
		if got, want := m.list.Height(), sz.Height-1; got != want {
			t.Errorf("%dx%d: list height = %d, want %d (low chrome 1)", sz.Width, sz.Height, got, want)
		}
		v := m.View()
		if !strings.Contains(stripANSI(v), "demo topic alpha") {
			t.Errorf("%dx%d: low view lost item text\nview:\n%s", sz.Width, sz.Height, v)
		}
		m = update(m, keyMsg("d")) // a status occurs — omitted on low screens
		v = m.View()
		if strings.Contains(stripANSI(v), nonInteractiveHint) {
			t.Errorf("%dx%d: low view must omit non-interactive hint\nview:\n%s", sz.Width, sz.Height, v)
		}
		if strings.Contains(stripANSI(v), "skipped") {
			t.Errorf("%dx%d: low view must omit status line\nview:\n%s", sz.Width, sz.Height, v)
		}
		if !strings.Contains(stripANSI(v), "q quit") {
			t.Errorf("%dx%d: low view must keep q/ESC escape hint\nview:\n%s", sz.Width, sz.Height, v)
		}
		if n := viewLines(v); n > sz.Height {
			t.Errorf("%dx%d: low view = %d lines > height %d\nview:\n%s", sz.Width, sz.Height, n, sz.Height, v)
		}
	}
}

// RF-numeric — the boundary 12 and a tall screen (24): the existing allocation
// (height-6) and chrome (non-interactive notice, status) kept — the above-threshold layout is equivalent.
func TestPendingHeightAtThresholdKeepsLayout(t *testing.T) {
	for _, sz := range []tea.WindowSizeMsg{{Width: 80, Height: 12}, {Width: 80, Height: 24}} {
		m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), PendingActions{})
		m = update(m, sz)
		if got, want := m.list.Height(), sz.Height-6; got != want {
			t.Errorf("%dx%d: list height = %d, want %d (full chrome 6)", sz.Width, sz.Height, got, want)
		}
		m = update(m, keyMsg("d"))
		v := stripANSI(m.View())
		for _, want := range []string{nonInteractiveHint, "skipped", "q quit"} {
			if !strings.Contains(v, want) {
				t.Errorf("%dx%d: view missing %q\nview:\n%s", sz.Width, sz.Height, want, v)
			}
		}
	}
}

// RF-wrap — A2: detail body wordwrap — a long single-line body splits into
// multiple lines at the stored window width - 2 (78 when unset).
func TestPendingDetailBodyWordWrap(t *testing.T) {
	items := []PendingItem{{
		ID: 7, Title: "demo topic alpha", Confidence: "0.80", Date: "2026-11-14",
		Body: strings.Repeat("word ", 40), Origin: "hook",
	}}
	cases := []struct {
		name   string
		resize bool
		width  int // the expected wrap width
	}{
		{name: "stored window width", resize: true, width: 40 - 2},
		{name: "default 78 without resize", resize: false, width: 78},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewPendingModel(context.Background(), items, nil, PendingActions{})
			if tc.resize {
				m = update(m, sizeMsg(40, 24))
			}
			m = update(m, enterMsg())
			v := m.View()
			var bodyLines int
			for _, ln := range strings.Split(v, "\n") {
				if !strings.Contains(ln, "word") {
					continue
				}
				bodyLines++
				if len(ln) > tc.width {
					t.Errorf("body line %d chars > wrap width %d: %q", len(ln), tc.width, ln)
				}
			}
			if bodyLines < 2 {
				t.Fatalf("body not wrapped (%d body lines, want multiple)\nview:\n%s", bodyLines, v)
			}
		})
	}
}

// RF-filter — contract 2: FilterValue includes the ID (fmt "#%d %s") — the "#9001"
// filter-matching regression. Unlike the list title display ("#%d  %s"), the filter value uses a single space.
func TestPendingRowFilterValueIncludesID(t *testing.T) {
	row := pendingRow{it: PendingItem{ID: 9001, Title: "demo topic omega"}}
	if got, want := row.FilterValue(), "#9001 demo topic omega"; got != want {
		t.Fatalf("FilterValue = %q, want %q", got, want)
	}
	if !strings.Contains(row.FilterValue(), "#9001") {
		t.Errorf("FilterValue %q must match filter token %q", row.FilterValue(), "#9001")
	}
}

// ansiRe — for removing ANSI escapes (SGR) — width assertions are by display width.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI — returns the display string with color codes removed.
func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// narrowViews — the Views of the 3 screens (list, detail, supersede) after a 40-column resize.
func narrowViews(t *testing.T) map[string]string {
	t.Helper()
	base := func() PendingModel {
		return NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), PendingActions{})
	}
	out := map[string]string{}
	m := update(base(), sizeMsg(40, 24))
	out["list"] = m.View()
	out["detail"] = update(m, enterMsg()).View()
	m2, cmd := drive(update(base(), sizeMsg(40, 24)), keyMsg("s"))
	if cmd == nil {
		t.Fatal("s must issue load cmd in narrow view test")
	}
	m2, _ = drive(m2, cmd())
	out["supersede"] = m2.View()
	return out
}

// RF-narrow — contract 1: 40 columns — all 3 screens keep the key-hint line within
// the width while keeping the "q quit" cue, and the exit guidance (nonInteractiveHint) is wrapped.
func TestPendingNarrowWidthClipping(t *testing.T) {
	for name, v := range narrowViews(t) {
		if !strings.Contains(v, "q quit") {
			t.Errorf("%s: 40-col view lost 'q quit'\nview:\n%s", name, v)
			continue
		}
		for _, ln := range strings.Split(stripANSI(v), "\n") {
			if strings.Contains(ln, "q quit") || strings.Contains(ln, "pipe output") {
				if n := len([]rune(ln)); n > 40 {
					t.Errorf("%s: hint line %d chars > 40: %q", name, n, ln)
				}
			}
		}
	}
}

// RF-narrow — contract 1: the detail origin line uses the wrapDetailBody wordwrap —
// a long origin splits into multiple lines within the window width (width - 2).
func TestPendingDetailOriginWrapped(t *testing.T) {
	items := []PendingItem{{
		ID: 7, Title: "demo topic alpha", Confidence: "0.80", Date: "2026-11-14",
		Body:   "demo summary alpha",
		Origin: "import: " + strings.Repeat("demo batch ", 10),
	}}
	m := NewPendingModel(context.Background(), items, nil, PendingActions{})
	m = update(m, sizeMsg(40, 24))
	m = update(m, enterMsg())
	originLines := 0
	for _, ln := range strings.Split(stripANSI(m.View()), "\n") {
		if !strings.Contains(ln, "batch") {
			continue
		}
		originLines++
		if n := len([]rune(ln)); n > 38 {
			t.Errorf("origin line %d chars > wrap width 38: %q", n, ln)
		}
	}
	if originLines < 2 {
		t.Fatalf("origin not wrapped (%d lines, want multiple)\nview:\n%s", originLines, m.View())
	}
}

// fakeStore — a minimal wire.Store fake (for the loader and entry tests).
// The HistoryStubs embedding absorbs the 5 history methods as unsupported defaults.
type fakeStore struct {
	wire.HistoryStubs
	pingErr  error
	queryErr error
	queryFn  func(sql string, args []any) ([][]any, error)
	queries  []string
}

func (f *fakeStore) Migrate(ctx context.Context) error { return nil }
func (f *fakeStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (f *fakeStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (f *fakeStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (f *fakeStore) Commit(ctx context.Context, msg string) (string, error) {
	return "abc123", nil
}
func (f *fakeStore) Ping(ctx context.Context) error { return f.pingErr }
func (f *fakeStore) Close()                         {}
func (f *fakeStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.queryFn != nil {
		return f.queryFn(sql, args)
	}
	return nil, nil
}

// pendingRows — a fake returning 1 pending (+1 active) row.
func pendingRowsStore() *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "status = 'pending'") {
			return [][]any{
				{7, "demo topic alpha", "demo summary alpha", "hook", "2026-11-14T10:00:00Z", 0.8},
			}, nil
		}
		if strings.Contains(sql, "status = 'active'") {
			return [][]any{{3, "demo active one"}}, nil
		}
		return nil, nil
	}}
}

// RF-protocol — the loader (via the real SQL, with a fake store injected — SQL and parsing verified).
func TestLoadPendingItemsAndTargets(t *testing.T) {
	st := pendingRowsStore()
	items, err := loadPendingItems(context.Background(), st)
	if err != nil {
		t.Fatalf("loadPendingItems: %v", err)
	}
	if len(items) != 1 || items[0].ID != 7 {
		t.Fatalf("items = %+v, want 1 item id 7", items)
	}
	if items[0].Title != "demo topic alpha" || items[0].Confidence != "0.80" {
		t.Errorf("item fields = %+v, want title/confidence parsed", items[0])
	}
	if !strings.HasPrefix(items[0].Date, "2026-11-14") {
		t.Errorf("date = %q, want 2026-11-14 prefix", items[0].Date)
	}
	if items[0].Body != "demo summary alpha" || items[0].Origin != "hook" {
		t.Errorf("item body/origin = %+v", items[0])
	}

	targets, err := loadSupersedeTargets(context.Background(), st)
	if err != nil {
		t.Fatalf("loadSupersedeTargets: %v", err)
	}
	if len(targets) != 1 || targets[0].ID != 3 || targets[0].Title != "demo active one" {
		t.Fatalf("targets = %+v, want 1 target id 3", targets)
	}
}

// RF-protocol — the action default real implementation: delegates the
// knowledge.RunApprove argument assembly (approve --id N / approve --id N
// --supersede OLD — reusing the same UPDATE path as maintain --supersede).
func TestDefaultActionsAssembleRunApproveArgs(t *testing.T) {
	if reflect.ValueOf(pendingApproveRunner).Pointer() != reflect.ValueOf(knowledge.RunApprove).Pointer() {
		t.Fatalf("pendingApproveRunner seam must default to knowledge.RunApprove")
	}
	orig := pendingApproveRunner
	var got [][]string
	pendingApproveRunner = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		got = append(got, args)
		return 0
	}
	defer func() { pendingApproveRunner = orig }()

	acts := defaultPendingActions(nil)
	if code, _ := acts.Approve(context.Background(), 7); code != 0 {
		t.Fatalf("Approve exit = %d, want 0", code)
	}
	if code, _ := acts.Supersede(context.Background(), 7, 3); code != 0 {
		t.Fatalf("Supersede exit = %d, want 0", code)
	}
	want := [][]string{{"--id", "7"}, {"--id", "7", "--supersede", "3"}}
	if len(got) != 2 || fmt.Sprint(got[0]) != fmt.Sprint(want[0]) || fmt.Sprint(got[1]) != fmt.Sprint(want[1]) {
		t.Fatalf("RunApprove args = %v, want %v", got, want)
	}
}

// RF-cause — contract 5: the default real implementation — on failure, parses the
// cause from the sink JSON (error/message). Success has no cause.
func TestDefaultActionsParseFailureCause(t *testing.T) {
	orig := pendingApproveRunner
	defer func() { pendingApproveRunner = orig }()

	pendingApproveRunner = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		fmt.Fprintln(stdout, `{"error":"not_found","id":9001}`)
		return 1
	}
	code, cause := defaultPendingActions(nil).Approve(context.Background(), 9001)
	if code != 1 || cause != "not_found" {
		t.Fatalf("Approve = (%d, %q), want (1, \"not_found\")", code, cause)
	}

	pendingApproveRunner = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		fmt.Fprintln(stdout, `{"error":"internal","message":"dial fail"}`)
		return 2
	}
	_, cause = defaultPendingActions(nil).Supersede(context.Background(), 9001, 3)
	if want := "internal: dial fail"; cause != want {
		t.Fatalf("Supersede cause = %q, want %q", cause, want)
	}

	pendingApproveRunner = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		fmt.Fprintln(stdout, `{"status":"approved","id":9001,"supersede_error":"supersede: id=3 is not active"}`)
		return 1
	}
	_, cause = defaultPendingActions(nil).Supersede(context.Background(), 9001, 3)
	if want := "supersede: id=3 is not active"; cause != want {
		t.Fatalf("Supersede supersede_error cause = %q, want %q", cause, want)
	}

	pendingApproveRunner = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		return 0
	}
	if code, cause := defaultPendingActions(nil).Approve(context.Background(), 9001); code != 0 || cause != "" {
		t.Fatalf("success Approve = (%d, %q), want (0, \"\")", code, cause)
	}
}

// RF-protocol — entry: 0 pending shows guidance without the TUI, then exit 0.
func TestRunPendingTUIEmpty(t *testing.T) {
	var stdout bytes.Buffer
	code := RunPendingTUI(context.Background(), &fakeStore{}, &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "no pending items") {
		t.Errorf("stdout = %q, want 'no pending items' notice", stdout.String())
	}
}

// RF-protocol — entry: non-terminal (pipe) falls back to exit 3 + guidance.
func TestRunPendingTUINonTerminalFallback(t *testing.T) {
	var stdout bytes.Buffer
	// go test stdout is a pipe — Run's non-terminal check is automatically true.
	code := RunPendingTUI(context.Background(), pendingRowsStore(), &stdout)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (stdout: %s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "TUI requires a terminal") {
		t.Errorf("stdout = %q, want terminal fallback notice", stdout.String())
	}
}

// pump — re-injects the cmd the list returned (filter application, etc.) into the model
// (bubbles reflects filter results asynchronously via cmd -> msg — the test pumps directly).
func pump(m PendingModel, cmd tea.Cmd) PendingModel {
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			return m
		}
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				m = pump(m, c)
			}
			return m
		}
		var next tea.Cmd
		m2, next := m.Update(msg)
		m = m2.(PendingModel)
		cmd = next
	}
	return m
}

// applyFilter — drives from '/' entry through typing the text to enter-commit.
func applyFilter(m PendingModel, text string) PendingModel {
	m, c := drive(m, keyMsg("/"))
	m = pump(m, c)
	for _, r := range text {
		var c tea.Cmd
		m, c = drive(m, keyMsg(string(r)))
		m = pump(m, c)
	}
	m, c = drive(m, enterMsg())
	return pump(m, c)
}

// During filter input (after '/')
// all keys are delegated to the list — typing 'a' does not run approve (consumed
// as filter text). After applying the filter, 'a' approves exactly the visible item.
func TestPendingFilterTypingDoesNotApprove(t *testing.T) {
	rec := &recorderActions{}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), rec.actions())
	m = update(m, sizeMsg(80, 24))
	// (1) typing 'a' during filter input — approve not run (consumed as filter text).
	m, c := drive(m, keyMsg("/")) // entering filter input
	m = pump(m, c)
	m, c = drive(m, keyMsg("a"))
	m = pump(m, c)
	if len(rec.approves) != 0 {
		t.Fatalf("필터 타이핑 중 approve 실행: %v", rec.approves)
	}
	m, c = drive(m, escMsg()) // canceling the filter (emptying the input)
	m = pump(m, c)
	// (2) applying the "#9" filter — 1 visible (demo topic beta).
	m = applyFilter(m, "#9")
	if m.list.FilterState() != list.FilterApplied {
		t.Fatalf("필터 미적용: state=%v", m.list.FilterState())
	}
	// with the "#9" filter (= demo topic beta) applied, a — approves the visible item id 9.
	m = update(m, keyMsg("a"))
	if len(rec.approves) != 1 || rec.approves[0] != 9 {
		t.Fatalf("필터 후 approve = %v, want [9]", rec.approves)
	}
	if len(m.items) != 2 {
		t.Fatalf("items after filtered approve = %d, want 2", len(m.items))
	}
}

// e — expires the selected item by delegation (via maintain --expire-pending).
// Only success (0) removes it from the list + a status message. days is computed from
// the item's valid_at elapsed time (a 10-day-old item -> 10 — the maintain cutoff includes the item).
func TestPendingExpireDelegatesAndRemoves(t *testing.T) {
	rec := &recorderActions{}
	items := demoItems()
	items[0].ValidAt = time.Now().Add(-10*24*time.Hour - time.Hour)
	m := NewPendingModel(context.Background(), items, staticTargets(demoTargets()), rec.actions())
	m = update(m, keyMsg("e"))
	if len(rec.expires) != 1 || rec.expires[0] != [2]int{7, 10} {
		t.Fatalf("expires = %v, want [{7 10}]", rec.expires)
	}
	if len(m.items) != 2 {
		t.Fatalf("items after expire = %d, want 2", len(m.items))
	}
	if !strings.Contains(m.View(), "expired") {
		t.Errorf("status after expire missing 'expired'\nview:\n%s", m.View())
	}
	// cutoff-effect guidance — the status line must state that the delegated command
	// expires not just this item but all pending at or below the cutoff (<= N days).
	if !strings.Contains(m.View(), "cutoff expires all pending items") {
		t.Errorf("status after expire missing cutoff effect note\nview:\n%s", m.View())
	}
	// help update — the list and status screens' key hints expose e expire.
	if !strings.Contains(m.View(), "e expire") {
		t.Errorf("list hint missing 'e expire'\nview:\n%s", m.View())
	}
	d := update(m, enterMsg())
	if !strings.Contains(d.View(), "e expire") {
		t.Errorf("detail hint missing 'e expire'\nview:\n%s", d.View())
	}
}

// e failure (exit != 0): item kept + failure status (cause shown).
func TestPendingExpireFailureKeepsItem(t *testing.T) {
	acts := PendingActions{
		Expire: func(ctx context.Context, id, days int) (int, string) {
			return 1, "usage: invalid number"
		},
	}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), acts)
	m = update(m, keyMsg("e"))
	if len(m.items) != 3 {
		t.Fatalf("items after failed expire = %d, want 3 (kept)", len(m.items))
	}
	if !strings.Contains(m.View(), "failed") || !strings.Contains(m.View(), "invalid number") {
		t.Errorf("status after failed expire missing cause\nview:\n%s", m.View())
	}
}

// expireDaysFor — computing the maintain --expire-pending delegation days: the
// largest integer with days*24h < elapsed such that the cutoff includes the item
// (boundary: exactly 10 days elapsed -> 9; 10.5 days -> 10; 12 hours -> 0).
func TestExpireDaysFor(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		age  time.Duration
		want int
	}{
		{10 * 24 * time.Hour, 9},
		{10*24*time.Hour + time.Hour, 10},
		{12 * time.Hour, 0},
		{36 * time.Hour, 1},
		{30 * 24 * time.Hour, 29},
	}
	for _, c := range cases {
		if got := expireDaysFor(now.Add(-c.age), now); got != c.want {
			t.Errorf("expireDaysFor(age %v) = %d, want %d", c.age, got, c.want)
		}
	}
}

// the action default real implementation delegates the
// --expire-pending N --commit argument assembly to ops.RunMaintain (reusing the
// existing path — the TUI itself performs no writes).
func TestDefaultActionsAssembleExpireArgs(t *testing.T) {
	if reflect.ValueOf(pendingMaintainRunner).Pointer() != reflect.ValueOf(ops.RunMaintain).Pointer() {
		t.Fatalf("pendingMaintainRunner seam must default to ops.RunMaintain")
	}
	orig := pendingMaintainRunner
	var got [][]string
	pendingMaintainRunner = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		got = append(got, args)
		return 0
	}
	defer func() { pendingMaintainRunner = orig }()

	if code, _ := defaultPendingActions(nil).Expire(context.Background(), 7, 10); code != 0 {
		t.Fatalf("Expire exit = %d, want 0", code)
	}
	want := []string{"--expire-pending", "10", "--commit"}
	if len(got) != 1 || fmt.Sprint(got[0]) != fmt.Sprint(want) {
		t.Fatalf("RunMaintain args = %v, want %v", got, want)
	}
}

// ── removal during filtering — the full SetItems reassembly ──

// Action removal under an applied filter abolishes RemoveItem:
// bubbles RemoveItem deletes items by global index but filteredItems by filter
// position, so filteredItem.index goes out of sync (a second action would target
// a different item). Unified via the full SetItems reassembly + clearing an empty
// refilter — the "#9" filter -> a (approves and removes 9) -> the second a targets the remaining #7.
func TestPendingFilteredApproveThenSecondActionTargetsRemaining(t *testing.T) {
	rec := &recorderActions{}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), rec.actions())
	m = update(m, sizeMsg(80, 24))
	m = applyFilter(m, "#9")
	if m.list.FilterState() != list.FilterApplied {
		t.Fatalf("filter not applied: %v", m.list.FilterState())
	}
	m, cmd := drive(m, keyMsg("a"))
	m = pump(m, cmd)
	if len(rec.approves) != 1 || rec.approves[0] != 9 {
		t.Fatalf("first approve = %v, want [9]", rec.approves)
	}
	if m.list.FilterState() != list.Unfiltered {
		t.Errorf("empty re-filter must reset filter, state = %v", m.list.FilterState())
	}
	if !strings.Contains(m.View(), "demo topic alpha") {
		t.Errorf("after removal view lost remaining item #7\nview:\n%s", m.View())
	}
	m, cmd = drive(m, keyMsg("a"))
	m = pump(m, cmd)
	if len(rec.approves) != 2 || rec.approves[1] != 7 {
		t.Fatalf("second approve = %v, want [9 7] (remaining first item)", rec.approves)
	}
}

// ── list height computation ──

// viewLines — the number of View render lines (the render without trailing newline).
func viewLines(v string) int { return strings.Count(v, "\n") + 1 }

// The list height h=height-4 was less than the actual non-list lines (key hints,
// status, non-interactive notice + separator blanks — up to 6 lines), so the screen
// exceeded the terminal height. Both the list screen (worst case, including
// status) and the supersede screen keep the View render line count within the
// terminal height. Terminal height >= 16: to include bubbles' internal chrome (title, status bar, pagination, help — 2 lines each) and at least 1 item, the list needs h >= 10.
func TestPendingViewFitsTerminalHeight(t *testing.T) {
	for _, sz := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 60, Height: 16}} {
		m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), PendingActions{})
		m = update(m, sz)
		if got, want := m.list.Height(), sz.Height-6; got != want {
			t.Errorf("%dx%d: list height = %d, want %d", sz.Width, sz.Height, got, want)
		}
		m = update(m, keyMsg("d")) // a status line — the worst line composition, including status
		if n := viewLines(m.View()); n > sz.Height {
			t.Errorf("%dx%d: list view = %d lines > height %d\nview:\n%s", sz.Width, sz.Height, n, sz.Height, m.View())
		}
		s, cmd := drive(m, keyMsg("s"))
		if cmd == nil {
			t.Fatalf("%dx%d: s must issue load cmd", sz.Width, sz.Height)
		}
		s, _ = drive(s, cmd())
		if s.mode != modeSupersede {
			t.Fatalf("%dx%d: after load mode = %v, want modeSupersede", sz.Width, sz.Height, s.mode)
		}
		if n := viewLines(s.View()); n > sz.Height {
			t.Errorf("%dx%d: supersede view = %d lines > height %d\nview:\n%s", sz.Width, sz.Height, n, sz.Height, s.View())
		}
	}
}

// the detail screen's displayed item under an applied filter is also converted via
// GlobalIndex: View's list.Index() is filter-relative (visible) and would diverge from
// m.items, showing the wrong item in the detail. The "be" filter (visible: #9) ->
// cursor move -> enter -> the detail body matches the selected #9.
func TestPendingFilteredDetailShowsSelectedBody(t *testing.T) {
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), PendingActions{})
	m = update(m, sizeMsg(80, 24))
	m = applyFilter(m, "be") // visible: #9 demo topic beta (#7 and #11 hidden by the filter)
	if m.list.FilterState() != list.FilterApplied {
		t.Fatalf("filter not applied: %v", m.list.FilterState())
	}
	m = update(m, keyMsg("down")) // cursor move (1 visible — the entry path is the same even without moving)
	m = update(m, enterMsg())
	if m.mode != modeDetail {
		t.Fatalf("after enter mode = %v, want modeDetail", m.mode)
	}
	d := stripANSI(m.View())
	for _, want := range []string{"demo topic beta", "demo summary beta", "import:demo.jsonl"} {
		if !strings.Contains(d, want) {
			t.Errorf("filtered detail missing %q (wrong item shown)\nview:\n%s", want, d)
		}
	}
	if strings.Contains(d, "demo summary alpha") || strings.Contains(d, "demo summary gamma") {
		t.Errorf("filtered detail shows unselected item\nview:\n%s", d)
	}
}

// RF-filter-state — the supersede confirmation under an applied filter converts
// the targetList visible index to the full index (GlobalIndex) — the delegated old
// is the selected candidate (#5), not the earlier candidate (#5?) hidden by the filter — the selected candidate (#5), not an earlier one hidden by the filter.
func TestPendingSupersedeFilteredTargetUsesGlobalIndex(t *testing.T) {
	rec := &recorderActions{}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), rec.actions())
	m = update(m, sizeMsg(80, 24))
	m, cmd := drive(m, keyMsg("s"))
	m, _ = drive(m, cmd()) // candidate load -> modeSupersede
	m = applyFilter(m, "two")
	m = update(m, enterMsg()) // supersede confirmation
	if len(rec.supersedes) != 1 || rec.supersedes[0] != [2]int{7, 5} {
		t.Fatalf("supersedes = %v, want [7 5](new=7·old=선택한 5)", rec.supersedes)
	}
	if len(m.candidates) != 1 || m.candidates[0].ID != 3 {
		t.Fatalf("candidates after supersede = %+v, want [3](stale 5 제거)", m.candidates)
	}
}

// the filtered detail display matches the selected item (GlobalIndex).
func TestPendingDetailMatchesSelectionUnderFilter(t *testing.T) {
	items := []PendingItem{{ID: 1, Title: "alpha"}, {ID: 2, Title: "beta"}, {ID: 9, Title: "gamma-zebra"}}
	m := NewPendingModel(context.Background(), items, nil, PendingActions{})
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.list.SetFilterText("gam")
	m = update(m, enterMsg())
	if !strings.Contains(m.View(), "#9") {
		t.Fatalf("detail should show filtered selection #9:\n%s", m.View())
	}
}
