// pending_test.go — v1.5.0 T7: pending 검토 TUI 계약 테스트. 상태 전이
// (목록→상세→액션)와 액션 위임(seam — 기존 RunApprove 경로 재사용 단언),
// 로더(fake store 주입), 비터미널 폴백. SUPERLITE_REAL 실DB 위임은
// cli 진입 테스트(pending_test.go·withFake 관례)가 담당한다.
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

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// keyMsg — 룬 1자 키 입력 시뮬레이션.
func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func enterMsg() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func escMsg() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyEsc} }

// sizeMsg — 터미널 리사이즈 시뮬레이션(실제 실행 환경은 시작 시
// bubbletea가 항상 WindowSizeMsg 1건을 전달한다).
func sizeMsg(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

// demoItems — 합성 데모 대기 항목 3건(개인 데이터 금지 — 항상 합성).
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

// recorderActions — 호출 기록 fake(기본 fixture). 실구현은
// defaultPendingActions가 knowledge.RunApprove를 감싼 것이다.
type recorderActions struct {
	approves   []int
	supersedes [][2]int // {pendingID, oldID}
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
	}
}

// staticTargets — 고정 후보를 반환하는 로더 fake(계약 3 지연 로드 seam).
func staticTargets(ts []SupersedeTarget) func(context.Context) ([]SupersedeTarget, error) {
	return func(context.Context) ([]SupersedeTarget, error) { return ts, nil }
}

// update — 모델 타입 보존 헬퍼(테스트 가독용).
func update(m PendingModel, msg tea.Msg) PendingModel {
	m2, _ := m.Update(msg)
	return m2.(PendingModel)
}

// drive — Update 결과 모델과 cmd를 함께 반환(지연 로드 cmd 실행용).
func drive(m PendingModel, msg tea.Msg) (PendingModel, tea.Cmd) {
	m2, cmd := m.Update(msg)
	return m2.(PendingModel), cmd
}

// RF1 — 목록: 제목·confidence·날짜 노출, enter→상세(본문·출처), esc→복귀.
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

// RF1 — 상세 뷰에서도 a 키 approve 위임이 동작(선택→액션 전이).
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

// RF-state — a: 선택 항목 approve 위임 + 목록에서 제거 + 상태 메시지.
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

// RF-state — a 실패(exit != 0): 항목 유지 + 실패 상태 메시지(원인 표시 — 계약 5).
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

// RF-cause — 계약 5: seam이 원인을 돌려주지 않으면 exit code 폴백 표기.
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

// RF-lazy — 계약 3: s는 로드를 예약만(loading targets...) → 로드 완료 메시지로
// 후보 목록 전환 → enter로 확정 → (id, old) 위임.
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
	// 계약 4: 대체된 old(active 3)는 후보·targetList에서 제거(stale 폐지).
	if len(m.candidates) != 1 || m.candidates[0].ID != 5 {
		t.Fatalf("candidates after supersede = %+v, want only id 5", m.candidates)
	}
	if strings.Contains(m.targetList.View(), "demo active one") {
		t.Errorf("targetList still shows superseded candidate\nview:\n%s", m.targetList.View())
	}
}

// RF-lazy — 계약 3: 후보 없음 — 로드 후 안내만, 목록 유지.
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

// RF-lazy — 계약 3: 로드 실패 — status에 원인, 목록 유지(재시도 가능).
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

// RF-lazy — 계약 3: 재진입 — 이미 로드된 후보는 재로드 없이 즉시 진입.
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

// RF-state — s 후 esc: 위임 없이 목록 복귀(취소).
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

// RF-state — d: 쓰기 없이 다음 항목으로 건너뛴다(선택 이동·항목 유지).
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

// RF-state — q: 종료 커맨드 반환.
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

// RF-resize — A1: WindowSizeMsg 수신 시 목록 크기가 터미널(높이-4)에
// 맞고, 12행 이하 단말·연속 리사이즈에서도 선택 항목 렌더가 유지된다
// (고정 80x20 폐지 — 시작 시 bubbletea가 전달하는 창 크기로 조정).
func TestPendingResizeKeepsItemsRendered(t *testing.T) {
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), PendingActions{})
	for _, sz := range []tea.WindowSizeMsg{
		{Width: 60, Height: 24},
		{Width: 60, Height: 12}, // 12행 단말
		{Width: 40, Height: 8},  // 그 이하
		{Width: 100, Height: 30},
	} {
		m = update(m, sz)
		if m.list.Height() != sz.Height-4 {
			t.Errorf("after %dx%d list height = %d, want %d", sz.Width, sz.Height, m.list.Height(), sz.Height-4)
		}
		if !strings.Contains(m.View(), "demo topic alpha") {
			t.Errorf("after %dx%d view lost item text\nview:\n%s", sz.Width, sz.Height, m.View())
		}
	}
}

// RF-wrap — A2: 상세 본문 wordwrap — 저장된 창 폭 기준 폭-2(저장 폭
// 없으면 78)로 긴 단일 라인 본문이 여러 라인으로 나뉜다.
func TestPendingDetailBodyWordWrap(t *testing.T) {
	items := []PendingItem{{
		ID: 7, Title: "demo topic alpha", Confidence: "0.80", Date: "2026-11-14",
		Body: strings.Repeat("word ", 40), Origin: "hook",
	}}
	cases := []struct {
		name   string
		resize bool
		width  int // 기대 wrap 폭
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

// RF-filter — 계약 2: FilterValue에 ID 포함(fmt "#%d %s") — "#9001" 필터
// 매칭 회귀. 목록 제목 표기("#%d  %s")와 달리 필터값은 단일 공백.
func TestPendingRowFilterValueIncludesID(t *testing.T) {
	row := pendingRow{it: PendingItem{ID: 9001, Title: "demo topic omega"}}
	if got, want := row.FilterValue(), "#9001 demo topic omega"; got != want {
		t.Fatalf("FilterValue = %q, want %q", got, want)
	}
	if !strings.Contains(row.FilterValue(), "#9001") {
		t.Errorf("FilterValue %q must match filter token %q", row.FilterValue(), "#9001")
	}
}

// ansiRe — ANSI 이스케이프(SGR) 제거용(폭 단얩은 표시 폭 기준).
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI — 색상 코드 제거 후 표시 문자열 반환.
func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// narrowViews — 40컬 리사이즈 후 세 화면(list·detail·supersede)의 View.
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

// RF-narrow — 계약 1: 40컬 — 세 화면 모두 키 안내 라인이 폭 안에 들어오되
// "q quit" 단서는 유지되고, 탈출 안내(nonInteractiveHint)는 wrap된다.
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

// RF-narrow — 계약 1: 상세 origin 라인은 wrapDetailBody 재사용 wordwrap —
// 긴 origin이 창 폭(폭-2) 안에서 여러 라인으로 나뉜다.
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

// fakeStore — wire.Store 최소 fake(로더·진입 테스트용).
type fakeStore struct {
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

// pendingRows — 대기 1건(+active 1건)을 반환하는 fake.
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

// RF-protocol — 로더(실구현 SQL 경유, fake store 주입 — SQL·파식 검증).
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

// RF-protocol — 액션 기본 실구현: knowledge.RunApprove 인자 조립 위임.
// (approve --id N / approve --id N --supersede OLD — maintain --supersede와
// 동일 UPDATE 경로 재사용.)
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

// RF-cause — 계약 5: 기본 실구현 — 실패 시 sink JSON(error/message)에서
// 원인 파싱. 성공은 원인 없음.
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

// RF-protocol — 진입: 대기 0건이면 TUI 없이 안내 후 exit 0.
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

// RF-protocol — 진입: 비터미널(파이프)은 폴백 exit 3 + 안내.
func TestRunPendingTUINonTerminalFallback(t *testing.T) {
	var stdout bytes.Buffer
	// go test stdout은 파이프 — Run의 비터미널 판정이 자동으로 참.
	code := RunPendingTUI(context.Background(), pendingRowsStore(), &stdout)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (stdout: %s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "TUI requires a terminal") {
		t.Errorf("stdout = %q, want terminal fallback notice", stdout.String())
	}
}

// pump — list가 반환한 cmd(필터 적용 등)를 모델로 재주입(bubbles는 필터
// 결과를 cmd→msg로 비동기 반영한다 — 테스트는 직접 펌프).
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

// applyFilter — '/' 진입 → 텍스트 타이핑 → enter 확정까지 구동.
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

// RF-filter-state — Review Focus(state·v1.5.3 리뷰): 필터 입력 중('/' 후)
// 키는 전부 list에 위임 — 'a'를 타이핑해도 approve가 실행되지 않는다
// (필터 텍스트로 소비). 필터 적용 후 'a'는 visible 항목을 정확히 승인.
func TestPendingFilterTypingDoesNotApprove(t *testing.T) {
	rec := &recorderActions{}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), rec.actions())
	m = update(m, sizeMsg(80, 24))
	// ① 필터 입력 중 'a' 타이핑 — approve 미실행(필터 텍스트로 소비).
	m, c := drive(m, keyMsg("/")) // 필터 입력 진입
	m = pump(m, c)
	m, c = drive(m, keyMsg("a"))
	m = pump(m, c)
	if len(rec.approves) != 0 {
		t.Fatalf("필터 타이핑 중 approve 실행: %v", rec.approves)
	}
	m, c = drive(m, escMsg()) // 필터 취소(입력 비움)
	m = pump(m, c)
	// ② "#9" 필터 적용 — visible 1건(demo topic beta).
	m = applyFilter(m, "#9")
	if m.list.FilterState() != list.FilterApplied {
		t.Fatalf("필터 미적용: state=%v", m.list.FilterState())
	}
	// 필터 "#9"(=demo topic beta) 적용 상태에서 a — visible 항목 id 9 승인.
	m = update(m, keyMsg("a"))
	if len(rec.approves) != 1 || rec.approves[0] != 9 {
		t.Fatalf("필터 후 approve = %v, want [9]", rec.approves)
	}
	if len(m.items) != 2 {
		t.Fatalf("items after filtered approve = %d, want 2", len(m.items))
	}
}

// RF-filter-state — Review Focus(state): 필터 적용 상태의 supersede 확정은
// targetList visible 인덱스를 전체 인덱스로 환산(GlobalIndex) — 필터로
// 가려진 앞 후보(#3)가 아닌 선택한 후보(#5)가 old로 위임된다.
func TestPendingSupersedeFilteredTargetUsesGlobalIndex(t *testing.T) {
	rec := &recorderActions{}
	m := NewPendingModel(context.Background(), demoItems(), staticTargets(demoTargets()), rec.actions())
	m = update(m, sizeMsg(80, 24))
	m, cmd := drive(m, keyMsg("s"))
	m, _ = drive(m, cmd()) // 후보 로드 → modeSupersede
	m = applyFilter(m, "two")
	m = update(m, enterMsg()) // supersede 확정
	if len(rec.supersedes) != 1 || rec.supersedes[0] != [2]int{7, 5} {
		t.Fatalf("supersedes = %v, want [7 5](new=7·old=선택한 5)", rec.supersedes)
	}
	if len(m.candidates) != 1 || m.candidates[0].ID != 3 {
		t.Fatalf("candidates after supersede = %+v, want [3](stale 5 제거)", m.candidates)
	}
}
