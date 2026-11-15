// pending_test.go — v1.5.0 T7: pending 검토 TUI 계약 테스트. 상태 전이
// (목록→상세→액션)와 액션 위임(seam — 기존 RunApprove 경로 재사용 단언),
// 로더(fake store 주입), 비터미널 폴백. SUPERLITE_REAL 실DB 위임은
// cli 진입 테스트(pending_test.go·withFake 관례)가 담당한다.
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

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// keyMsg — 룬 1자 키 입력 시뮬레이션.
func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func enterMsg() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func escMsg() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyEsc} }

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
		Approve: func(ctx context.Context, id int) int {
			r.approves = append(r.approves, id)
			return 0
		},
		Supersede: func(ctx context.Context, id, oldID int) int {
			r.supersedes = append(r.supersedes, [2]int{id, oldID})
			return 0
		},
	}
}

// update — 모델 타입 보존 헬퍼(테스트 가독용).
func update(m PendingModel, msg tea.Msg) PendingModel {
	m2, _ := m.Update(msg)
	return m2.(PendingModel)
}

// RF1 — 목록: 제목·confidence·날짜 노출, enter→상세(본문·출처), esc→복귀.
func TestPendingListShowsFieldsAndDetailTransition(t *testing.T) {
	m := NewPendingModel(context.Background(), demoItems(), demoTargets(), PendingActions{})
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
	m := NewPendingModel(context.Background(), demoItems(), demoTargets(), rec.actions())
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
	m := NewPendingModel(context.Background(), demoItems(), demoTargets(), rec.actions())
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

// RF-state — a 실패(exit != 0): 항목 유지 + 실패 상태 메시지.
func TestPendingApproveFailureKeepsItem(t *testing.T) {
	acts := PendingActions{
		Approve: func(ctx context.Context, id int) int { return 1 },
	}
	m := NewPendingModel(context.Background(), demoItems(), demoTargets(), acts)
	m = update(m, keyMsg("a"))
	if len(m.items) != 3 {
		t.Fatalf("items after failed approve = %d, want 3 (kept)", len(m.items))
	}
	if !strings.Contains(m.View(), "failed") {
		t.Errorf("status after failed approve missing 'failed'\nview:\n%s", m.View())
	}
}

// RF-state — s: 대체 대상 후보 목록 진입 → enter로 확정 → (id, old) 위임.
func TestPendingSupersedeFlowDelegates(t *testing.T) {
	rec := &recorderActions{}
	m := NewPendingModel(context.Background(), demoItems(), demoTargets(), rec.actions())
	m = update(m, keyMsg("s"))
	if m.mode != modeSupersede {
		t.Fatalf("after s mode = %v, want modeSupersede", m.mode)
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
}

// RF-state — s 후 esc: 위임 없이 목록 복귀(취소).
func TestPendingSupersedeEscapeCancels(t *testing.T) {
	rec := &recorderActions{}
	m := NewPendingModel(context.Background(), demoItems(), demoTargets(), rec.actions())
	m = update(m, keyMsg("s"))
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
	m := NewPendingModel(context.Background(), demoItems(), demoTargets(), rec.actions())
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
	m := NewPendingModel(context.Background(), demoItems(), demoTargets(), PendingActions{})
	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q returned nil cmd, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q cmd() = %T, want tea.QuitMsg", cmd())
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
	if code := acts.Approve(context.Background(), 7); code != 0 {
		t.Fatalf("Approve exit = %d, want 0", code)
	}
	if code := acts.Supersede(context.Background(), 7, 3); code != 0 {
		t.Fatalf("Supersede exit = %d, want 0", code)
	}
	want := [][]string{{"--id", "7"}, {"--id", "7", "--supersede", "3"}}
	if len(got) != 2 || fmt.Sprint(got[0]) != fmt.Sprint(want[0]) || fmt.Sprint(got[1]) != fmt.Sprint(want[1]) {
		t.Fatalf("RunApprove args = %v, want %v", got, want)
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
