// maintain_test.go — v1.6.0 T6: maintain 검토 TUI 계약 테스트. 소스 탭
// 전이·상세(양쪽 본문·보존 미리보기)·위임 seam(merge/invalidate — 기존
// maintain --merge·--invalidate 경로 재사용 단언)·GlobalIndex 필터 회귀·
// 로더 fake·비터미널 폴백. 합성 데모 데이터만 사용(개인 데이터 금지).
package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/wire"
)

// ── 테스트 픽스처 ──

// demoMaintainData — 3소스 각 1건 합성 데모.
func demoMaintainData() MaintainData {
	return MaintainData{
		Dup:      []MaintainSuspect{{Source: MaintainDup, Topic: "demo topic dup", IDs: []int{3, 5}}},
		Conflict: []MaintainSuspect{{Source: MaintainConflict, Topic: "demo topic conflict", IDs: []int{8, 9}, Summaries: []string{"demo claim one", "demo claim two"}}},
		Stale:    []MaintainSuspect{{Source: MaintainStale, Topic: "demo topic stale", IDs: []int{7}, Reason: "stale", StaleDays: 120}},
	}
}

// demoMaintainBodies — 상세·선택 화면 본문 캐시용 합성 데모.
func demoMaintainBodies() map[int]MaintainBody {
	return map[int]MaintainBody{
		3:  {ID: 3, Topic: "demo topic dup", Summary: "demo body three", Origin: "hook", ValidAt: "2026-10-01"},
		5:  {ID: 5, Topic: "demo topic dup", Summary: "demo body five", Origin: "import:demo.jsonl", ValidAt: "2026-10-02"},
		7:  {ID: 7, Topic: "demo topic stale", Summary: "demo body seven", Origin: "hook", ValidAt: "2026-06-01"},
		8:  {ID: 8, Topic: "demo topic conflict", Summary: "demo body eight", Origin: "agent"},
		9:  {ID: 9, Topic: "demo topic conflict", Summary: "demo body nine", Origin: "agent"},
		12: {ID: 12, Topic: "demo topic stale two", Summary: "demo body twelve", Origin: "hook"},
	}
}

// staticMaintainBodies — 고정 본문 로더 fake.
func staticMaintainBodies(bs map[int]MaintainBody) func(context.Context, []int) (map[int]MaintainBody, error) {
	return func(context.Context, []int) (map[int]MaintainBody, error) { return bs, nil }
}

// recorderMaintainActions — 호출 기록 fake(기본 fixture).
type recorderMaintainActions struct {
	merges      [][2]int // {from, to}
	invalidates []int
}

func (r *recorderMaintainActions) actions() MaintainActions {
	return MaintainActions{
		Merge: func(ctx context.Context, from, to int) (int, string) {
			r.merges = append(r.merges, [2]int{from, to})
			return 0, ""
		},
		Invalidate: func(ctx context.Context, id int) (int, string) {
			r.invalidates = append(r.invalidates, id)
			return 0, ""
		},
	}
}

// mUpdate / mDrive / mPump / mApplyFilter — pending 헬퍼 관례의 maintain판
// (이름 충돌 회피 — keyMsg·sizeMsg·stripANSI는 pending_test 공유).
func mUpdate(m MaintainModel, msg tea.Msg) MaintainModel {
	m2, _ := m.Update(msg)
	return m2.(MaintainModel)
}

func mDrive(m MaintainModel, msg tea.Msg) (MaintainModel, tea.Cmd) {
	m2, cmd := m.Update(msg)
	return m2.(MaintainModel), cmd
}

func mPump(m MaintainModel, cmd tea.Cmd) MaintainModel {
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			return m
		}
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				m = mPump(m, c)
			}
			return m
		}
		var next tea.Cmd
		m2, next := m.Update(msg)
		m = m2.(MaintainModel)
		cmd = next
	}
	return m
}

func mApplyFilter(m MaintainModel, text string) MaintainModel {
	m, c := mDrive(m, keyMsg("/"))
	m = mPump(m, c)
	for _, r := range text {
		var c tea.Cmd
		m, c = mDrive(m, keyMsg(string(r)))
		m = mPump(m, c)
	}
	m, c = mDrive(m, enterMsg())
	return mPump(m, c)
}

// newDemoMaintainModel — 기본 fixture 조립.
func newDemoMaintainModel(acts MaintainActions) MaintainModel {
	return NewMaintainModel(context.Background(), demoMaintainData(),
		MaintainLoaders{Bodies: staticMaintainBodies(demoMaintainBodies())}, acts)
}

// ── RF 전이: 소스 탭·상세 ──

// RF1 — 목록: 소스 탭 헤더(활성 강조)·주제 노출, tab 키 소스 전환,
// enter→상세(양쪽 본문 + 보존 미리보기), esc→복귀.
func TestMaintainTabsAndDetailTransition(t *testing.T) {
	m := newDemoMaintainModel(MaintainActions{})
	m = mUpdate(m, sizeMsg(80, 24))
	v := m.View()
	for _, want := range []string{"dup-suspects", "conflict-suspects", "stale", "demo topic dup"} {
		if !strings.Contains(v, want) {
			t.Errorf("list view missing %q\nview:\n%s", want, v)
		}
	}
	if m.mode != maintainList {
		t.Fatalf("initial mode = %v, want maintainList", m.mode)
	}
	// tab 전환: dup → conflict → stale.
	m = mUpdate(m, keyMsg("tab"))
	if !strings.Contains(m.View(), "[conflict-suspects") {
		t.Errorf("after tab, conflict tab not active\nview:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "demo claim one") {
		t.Errorf("conflict tab missing summary preview\nview:\n%s", m.View())
	}
	m = mUpdate(m, keyMsg("tab"))
	if !strings.Contains(m.View(), "[stale") {
		t.Errorf("after tab x2, stale tab not active\nview:\n%s", m.View())
	}
	m = mUpdate(m, keyMsg("tab")) // stale → dup (빈 탭 스킵 — 전부 비어 있지 않으므로 순환)
	if !strings.Contains(m.View(), "[dup-suspects") {
		t.Errorf("after tab x3, dup tab not active\nview:\n%s", m.View())
	}
	// 상세 진입: 본문 지연 로드(계약 — enter 시점 로드).
	m, cmd := mDrive(m, enterMsg())
	if m.mode != maintainList || !strings.Contains(m.View(), "loading") {
		t.Fatalf("enter should defer detail until bodies load (mode=%v)\nview:\n%s", m.mode, m.View())
	}
	if cmd == nil {
		t.Fatal("enter must issue a bodies load cmd")
	}
	m, _ = mDrive(m, cmd())
	if m.mode != maintainDetail {
		t.Fatalf("after load mode = %v, want maintainDetail", m.mode)
	}
	d := m.View()
	for _, want := range []string{"demo body three", "demo body five", "demo topic dup"} {
		if !strings.Contains(d, want) {
			t.Errorf("detail view missing %q\nview:\n%s", want, d)
		}
	}
	// RF 미리보기 — merge UX 원칙: "무엇이 보존되는지" 명시.
	if !strings.Contains(stripANSI(d), "preserved") {
		t.Errorf("detail view missing preservation preview\nview:\n%s", d)
	}
	m = mUpdate(m, escMsg())
	if m.mode != maintainList {
		t.Fatalf("after esc mode = %v, want maintainList", m.mode)
	}
}

// RF1 — stale 상세: invalidate 미리보기 + 되돌림 경로(asof) 안내.
func TestMaintainStaleDetailPreview(t *testing.T) {
	m := newDemoMaintainModel(MaintainActions{})
	m = mUpdate(m, sizeMsg(80, 24))
	m = mUpdate(m, keyMsg("tab")) // conflict
	m = mUpdate(m, keyMsg("tab")) // stale
	m, cmd := mDrive(m, enterMsg())
	m, _ = mDrive(m, cmd())
	if m.mode != maintainDetail {
		t.Fatalf("mode = %v, want maintainDetail", m.mode)
	}
	d := stripANSI(m.View())
	for _, want := range []string{"demo body seven", "preserved", "asof"} {
		if !strings.Contains(d, want) {
			t.Errorf("stale detail missing %q\nview:\n%s", want, d)
		}
	}
}

// RF1 — 본문 로드 실패: 목록 유지 + 원인 status(재시도 가능).
func TestMaintainDetailBodyLoadError(t *testing.T) {
	ld := MaintainLoaders{Bodies: func(context.Context, []int) (map[int]MaintainBody, error) {
		return nil, errors.New("demo store down")
	}}
	m := NewMaintainModel(context.Background(), demoMaintainData(), ld, MaintainActions{})
	m = mUpdate(m, sizeMsg(80, 24))
	m, cmd := mDrive(m, enterMsg())
	m, _ = mDrive(m, cmd())
	if m.mode != maintainList {
		t.Fatalf("mode after load error = %v, want maintainList", m.mode)
	}
	if !strings.Contains(m.View(), "demo store down") {
		t.Errorf("status after load error missing cause\nview:\n%s", m.View())
	}
}

// ── RF 위임: merge ──

// RF2 — m: 생존자(survivor) 선택 화면 → enter 위임(from=나머지, to=선택)
// + 그룹 제거 + 보존 status. 위임은 maintain --merge --from N --to M 등가.
func TestMaintainMergePickFlowDelegates(t *testing.T) {
	rec := &recorderMaintainActions{}
	m := newDemoMaintainModel(rec.actions())
	m = mUpdate(m, sizeMsg(80, 24))
	m, cmd := mDrive(m, keyMsg("m"))
	if cmd == nil {
		t.Fatal("m must issue bodies load cmd before pick")
	}
	m, _ = mDrive(m, cmd())
	if m.mode != maintainPick || m.pickWhat != "merge" {
		t.Fatalf("after load mode=%v pickWhat=%q, want maintainPick/merge", m.mode, m.pickWhat)
	}
	pv := m.View()
	if !strings.Contains(pv, "survivor") {
		t.Errorf("pick view missing survivor guidance\nview:\n%s", pv)
	}
	m = mUpdate(m, enterMsg())
	if len(rec.merges) != 1 || rec.merges[0] != [2]int{5, 3} {
		t.Fatalf("merges = %v, want [{5 3}] (from=other 5, to=survivor 3)", rec.merges)
	}
	if m.mode != maintainList {
		t.Fatalf("after merge confirm mode = %v, want maintainList", m.mode)
	}
	if len(m.tabs[MaintainDup]) != 0 {
		t.Fatalf("dup tab after merge = %+v, want empty", m.tabs[MaintainDup])
	}
	sv := stripANSI(m.View())
	if !strings.Contains(sv, "merged #5 into #3") {
		t.Errorf("status after merge missing summary\nview:\n%s", sv)
	}
	if !strings.Contains(sv, "preserved") {
		t.Errorf("status after merge missing preservation note\nview:\n%s", sv)
	}
}

// RF2 — pick 화면에서 커서 이동 후 enter: 선택한 쪽이 survivor(to)가 된다.
func TestMaintainMergePickSecondSurvivor(t *testing.T) {
	rec := &recorderMaintainActions{}
	m := newDemoMaintainModel(rec.actions())
	m = mUpdate(m, sizeMsg(80, 24))
	m, cmd := mDrive(m, keyMsg("m"))
	m, _ = mDrive(m, cmd())
	m = mUpdate(m, keyMsg("down"))
	m = mUpdate(m, enterMsg())
	if len(rec.merges) != 1 || rec.merges[0] != [2]int{3, 5} {
		t.Fatalf("merges = %v, want [{3 5}] (survivor=5)", rec.merges)
	}
}

// RF2 — merge 실패(exit != 0): 그룹 유지 + 실패 status(원인 표기).
func TestMaintainMergeFailureKeepsGroup(t *testing.T) {
	acts := MaintainActions{Merge: func(ctx context.Context, from, to int) (int, string) {
		return 1, "not_found"
	}}
	m := newDemoMaintainModel(acts)
	m = mUpdate(m, sizeMsg(80, 24))
	m, cmd := mDrive(m, keyMsg("m"))
	m, _ = mDrive(m, cmd())
	m = mUpdate(m, enterMsg())
	if len(m.tabs[MaintainDup]) != 1 {
		t.Fatalf("dup tab after failed merge = %+v, want 1 (kept)", m.tabs[MaintainDup])
	}
	sv := m.View()
	if !strings.Contains(sv, "failed") || !strings.Contains(sv, "not_found") {
		t.Errorf("status after failed merge = %q\nview:\n%s", m.status, sv)
	}
}

// RF2 — 원인 없는 실패: exit code 폴백 표기(계약 5 승계).
func TestMaintainMergeFailureFallsBackToExitCode(t *testing.T) {
	acts := MaintainActions{Merge: func(ctx context.Context, from, to int) (int, string) {
		return 3, ""
	}}
	m := newDemoMaintainModel(acts)
	m = mUpdate(m, sizeMsg(80, 24))
	m, cmd := mDrive(m, keyMsg("m"))
	m, _ = mDrive(m, cmd())
	m = mUpdate(m, enterMsg())
	if !strings.Contains(m.View(), "exit 3") {
		t.Errorf("status without cause should fall back to exit code\nview:\n%s", m.View())
	}
}

// RF2 — merge는 dup/conflict 그룹 전용: stale 탭에서 m은 안내만.
func TestMaintainMergeRejectedOnStale(t *testing.T) {
	rec := &recorderMaintainActions{}
	m := newDemoMaintainModel(rec.actions())
	m = mUpdate(m, sizeMsg(80, 24))
	m = mUpdate(m, keyMsg("tab"))
	m = mUpdate(m, keyMsg("tab")) // stale
	m = mUpdate(m, keyMsg("m"))
	if len(rec.merges) != 0 {
		t.Fatalf("merges on stale = %v, want none", rec.merges)
	}
	if m.mode != maintainList {
		t.Fatalf("mode after m on stale = %v, want maintainList", m.mode)
	}
}

// ── RF 위임: invalidate ──

// RF3 — i(stale): 본문 로드·선택 없이 즉시 위임 + 되돌림 경로 안내.
func TestMaintainInvalidateStaleDirectWithUndoHint(t *testing.T) {
	rec := &recorderMaintainActions{}
	m := newDemoMaintainModel(rec.actions())
	m = mUpdate(m, sizeMsg(80, 24))
	m = mUpdate(m, keyMsg("tab"))
	m = mUpdate(m, keyMsg("tab")) // stale
	m, cmd := mDrive(m, keyMsg("i"))
	if cmd != nil {
		t.Fatal("i on stale must not issue a load cmd (direct delegation)")
	}
	if len(rec.invalidates) != 1 || rec.invalidates[0] != 7 {
		t.Fatalf("invalidates = %v, want [7]", rec.invalidates)
	}
	if len(m.tabs[MaintainStale]) != 0 {
		t.Fatalf("stale tab after invalidate = %+v, want empty", m.tabs[MaintainStale])
	}
	sv := stripANSI(m.View())
	if !strings.Contains(sv, "invalidated #7") {
		t.Errorf("status missing 'invalidated #7'\nview:\n%s", sv)
	}
	if !strings.Contains(sv, "asof") {
		t.Errorf("status missing undo guidance (asof)\nview:\n%s", sv)
	}
}

// RF3 — i(dup/conflict 그룹): 무효화할 id 선택 화면 → enter 위임.
func TestMaintainInvalidateGroupPickDelegates(t *testing.T) {
	rec := &recorderMaintainActions{}
	m := newDemoMaintainModel(rec.actions())
	m = mUpdate(m, sizeMsg(80, 24))
	m, cmd := mDrive(m, keyMsg("i"))
	if cmd == nil {
		t.Fatal("i on group must issue bodies load cmd before pick")
	}
	m, _ = mDrive(m, cmd())
	if m.mode != maintainPick || m.pickWhat != "invalidate" {
		t.Fatalf("after load mode=%v pickWhat=%q, want maintainPick/invalidate", m.mode, m.pickWhat)
	}
	m = mUpdate(m, enterMsg())
	if len(rec.invalidates) != 1 || rec.invalidates[0] != 3 {
		t.Fatalf("invalidates = %v, want [3] (picked)", rec.invalidates)
	}
	if len(m.tabs[MaintainDup]) != 0 {
		t.Fatalf("dup tab after invalidate = %+v, want empty", m.tabs[MaintainDup])
	}
}

// RF3 — invalidate 실패: 항목 유지 + 원인 status.
func TestMaintainInvalidateFailureKeepsItem(t *testing.T) {
	acts := MaintainActions{Invalidate: func(ctx context.Context, id int) (int, string) {
		return 1, "nonexistent ids"
	}}
	m := newDemoMaintainModel(acts)
	m = mUpdate(m, sizeMsg(80, 24))
	m = mUpdate(m, keyMsg("tab"))
	m = mUpdate(m, keyMsg("tab")) // stale
	m = mUpdate(m, keyMsg("i"))
	if len(m.tabs[MaintainStale]) != 1 {
		t.Fatalf("stale tab after failed invalidate = %+v, want 1 (kept)", m.tabs[MaintainStale])
	}
	sv := m.View()
	if !strings.Contains(sv, "failed") || !strings.Contains(sv, "nonexistent ids") {
		t.Errorf("status after failed invalidate = %q\nview:\n%s", m.status, sv)
	}
}

// ── RF keep·quit·종료 ──

// RF4 — k: 위임 없이 검토 목록에서 제거 + "kept" status.
func TestMaintainKeepRemovesWithoutDelegation(t *testing.T) {
	rec := &recorderMaintainActions{}
	m := newDemoMaintainModel(rec.actions())
	m = mUpdate(m, sizeMsg(80, 24))
	m = mUpdate(m, keyMsg("k"))
	if len(rec.merges) != 0 || len(rec.invalidates) != 0 {
		t.Fatalf("k delegated: merges=%v invalidates=%v", rec.merges, rec.invalidates)
	}
	if len(m.tabs[MaintainDup]) != 0 {
		t.Fatalf("dup tab after keep = %+v, want empty", m.tabs[MaintainDup])
	}
	if !strings.Contains(m.View(), "kept") {
		t.Errorf("status after keep missing 'kept'\nview:\n%s", m.View())
	}
}

// RF4 — q: 종료 커맨드 반환.
func TestMaintainQuitReturnsTeaQuit(t *testing.T) {
	m := newDemoMaintainModel(MaintainActions{})
	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q returned nil cmd, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q cmd() = %T, want tea.QuitMsg", cmd())
	}
}

// RF4 — 마지막 항목 처리 후 전소스 비면 자동 종료.
func TestMaintainAllSourcesEmptyQuits(t *testing.T) {
	data := MaintainData{Stale: demoMaintainData().Stale}
	rec := &recorderMaintainActions{}
	m := NewMaintainModel(context.Background(), data,
		MaintainLoaders{Bodies: staticMaintainBodies(demoMaintainBodies())}, rec.actions())
	m = mUpdate(m, sizeMsg(80, 24))
	_, cmd := m.Update(keyMsg("i"))
	if cmd == nil {
		t.Fatal("last action must return tea.Quit when all sources are empty")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("cmd() = %T, want tea.QuitMsg", cmd())
	}
}

// ── RF-state: GlobalIndex 필터 회귀 ──

// RF-state — 필터 적용 상태의 액션은 visible 인덱스 → 전체 인덱스
// 환산(GlobalIndex — pending #id 필터 회귀 승계). stale 2건 중 "#12"
// 필터 후 i는 12번을 무효화한다.
func TestMaintainFilteredInvalidateUsesGlobalIndex(t *testing.T) {
	data := demoMaintainData()
	data.Stale = append(data.Stale, MaintainSuspect{Source: MaintainStale, Topic: "demo topic stale two", IDs: []int{12}, Reason: "never_read", StaleDays: 200})
	rec := &recorderMaintainActions{}
	m := NewMaintainModel(context.Background(), data,
		MaintainLoaders{Bodies: staticMaintainBodies(demoMaintainBodies())}, rec.actions())
	m = mUpdate(m, sizeMsg(80, 24))
	m = mUpdate(m, keyMsg("tab")) // conflict
	m = mUpdate(m, keyMsg("tab")) // stale
	m = mApplyFilter(m, "#12")
	if m.lists[m.tab].FilterState() != list.FilterApplied {
		t.Fatalf("filter not applied: %v", m.lists[m.tab].FilterState())
	}
	m = mUpdate(m, keyMsg("i"))
	if len(rec.invalidates) != 1 || rec.invalidates[0] != 12 {
		t.Fatalf("invalidates after filter = %v, want [12]", rec.invalidates)
	}
}

// ── RF-protocol: 기본 실구현 위임 인자 ──

// RF5 — maintainRunner seam 기본값은 ops.RunMaintain(중복 구현 금지).
func TestMaintainRunnerSeamDefaultsToOps(t *testing.T) {
	if reflect.ValueOf(maintainRunner).Pointer() != reflect.ValueOf(ops.RunMaintain).Pointer() {
		t.Fatal("maintainRunner seam must default to ops.RunMaintain")
	}
}

// RF5 — 기본 액션: RunMaintain 인자 조립(merge --from N --to M /
// invalidate --ids N --yes) + sink JSON 원인 파싱 + exit 폴백.
func TestDefaultMaintainActionsAssembleRunnerArgs(t *testing.T) {
	orig := maintainRunner
	defer func() { maintainRunner = orig }()

	var got [][]string
	var out string
	var code int
	maintainRunner = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		got = append(got, args)
		fmt.Fprint(stdout, out)
		return code
	}

	acts := defaultMaintainActions(nil)
	code = 0
	if c, _ := acts.Merge(context.Background(), 5, 3); c != 0 {
		t.Fatalf("Merge exit = %d, want 0", c)
	}
	if c, _ := acts.Invalidate(context.Background(), 7); c != 0 {
		t.Fatalf("Invalidate exit = %d, want 0", c)
	}
	want := [][]string{
		{"--merge", "--from", "5", "--to", "3"},
		{"--invalidate", "--ids", "7", "--yes"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("RunMaintain args = %v, want %v", got, want)
	}

	// 실패 원인 파싱(actionFailureCause 재사용 — 계약 5).
	got = nil
	code, out = 1, `{"error":"not_found","id":9001}`+"\n"
	if _, cause := acts.Invalidate(context.Background(), 9001); cause != "not_found" {
		t.Fatalf("Invalidate cause = %q, want not_found", cause)
	}
	code, out = 1, ""
	if c, cause := acts.Invalidate(context.Background(), 9001); c != 1 || cause != "" {
		t.Fatalf("no-cause failure = (%d, %q), want (1, \"\")", c, cause)
	}
}

// ── RF-protocol: 기본 로더 ──

// fakeMaintainRunner — 인자별 고정 JSON을 내주는 maintainRunner fake.
func fakeMaintainRunner() (func(context.Context, wire.Store, []string, io.Reader, io.Writer) int, *[][]string) {
	var seen [][]string
	fn := func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		seen = append(seen, args)
		mode := ""
		for _, a := range args {
			mode = a
		}
		switch mode {
		case "--dup-suspects":
			fmt.Fprintln(stdout, `{"status":"dup-suspects","groups":[{"topic":"demo topic dup","ids":[3,5]}]}`)
		case "--conflict-suspects":
			fmt.Fprintln(stdout, `{"status":"conflict-suspects","groups":[{"topic":"demo topic conflict","ids":[8,9],"summaries":["demo claim one","demo claim two"]}]}`)
		case "--stale":
			fmt.Fprintln(stdout, `{"status":"ok","days":90,"count":1,"items":[{"id":7,"topic":"demo topic stale","never_read":false,"reason":"stale","stale_days":120}],"summary":"1 to invalidate"}`)
		}
		return 0
	}
	return fn, &seen
}

// RF6 — suspects 로더: 세 실행(--dup-suspects·--conflict-suspects·--stale)
// JSON 파싱 → MaintainData(소스·ids·요약·사유·경과일).
func TestMaintainLoadSuspectsParsesRunnerJSON(t *testing.T) {
	orig := maintainRunner
	defer func() { maintainRunner = orig }()
	fn, seen := fakeMaintainRunner()
	maintainRunner = fn

	data, err := defaultMaintainLoaders(nil).Suspects(context.Background())
	if err != nil {
		t.Fatalf("Suspects: %v", err)
	}
	if len(data.Dup) != 1 || data.Dup[0].Topic != "demo topic dup" || fmt.Sprint(data.Dup[0].IDs) != "[3 5]" || data.Dup[0].Source != MaintainDup {
		t.Fatalf("dup = %+v", data.Dup)
	}
	if len(data.Conflict) != 1 || data.Conflict[0].Source != MaintainConflict ||
		fmt.Sprint(data.Conflict[0].Summaries) != "[demo claim one demo claim two]" {
		t.Fatalf("conflict = %+v", data.Conflict)
	}
	if len(data.Stale) != 1 || data.Stale[0].Source != MaintainStale || data.Stale[0].IDs[0] != 7 ||
		data.Stale[0].Reason != "stale" || data.Stale[0].StaleDays != 120 {
		t.Fatalf("stale = %+v", data.Stale)
	}
	if len(*seen) != 3 {
		t.Fatalf("runner calls = %d, want 3", len(*seen))
	}
}

// RF6 — suspects 로더: 위임 경로 실패(exit != 0)는 원인 오류로 전파.
func TestMaintainLoadSuspectsRunnerError(t *testing.T) {
	orig := maintainRunner
	defer func() { maintainRunner = orig }()
	maintainRunner = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		fmt.Fprintln(stdout, `{"error":"maintain: unsupported store"}`)
		return 1
	}
	if _, err := defaultMaintainLoaders(nil).Suspects(context.Background()); err == nil {
		t.Fatal("Suspects must fail when delegated report fails")
	}
}

// RF6 — bodies 로더: id 배치 조회(ANY($1)) → 재살균 본문 맵.
func TestMaintainLoadBodies(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if !strings.Contains(sql, "ANY($1)") {
			return nil, nil
		}
		return [][]any{
			{3, "demo topic dup", "demo body three", "hook", "2026-10-01T00:00:00Z"},
			{5, "demo topic dup", "demo body five", "import:demo.jsonl", nil},
		}, nil
	}}
	bs, err := defaultMaintainLoaders(st).Bodies(context.Background(), []int{3, 5})
	if err != nil {
		t.Fatalf("Bodies: %v", err)
	}
	if len(bs) != 2 || bs[3].Summary != "demo body three" || bs[5].Summary != "demo body five" {
		t.Fatalf("bodies = %+v", bs)
	}
}

// ── RF-protocol: 진입 ──

// RF7 — 검토 대상 0건: TUI 없이 안내 후 exit 0.
func TestRunMaintainTUIEmpty(t *testing.T) {
	orig := maintainRunner
	defer func() { maintainRunner = orig }()
	maintainRunner = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		mode := ""
		for _, a := range args {
			mode = a
		}
		switch mode {
		case "--conflict-suspects":
			fmt.Fprintln(stdout, `{"status":"conflict-suspects","groups":[]}`)
		case "--stale":
			fmt.Fprintln(stdout, `{"status":"ok","days":90,"count":0,"items":[],"summary":"0 to invalidate"}`)
		default:
			fmt.Fprintln(stdout, `{"status":"dup-suspects","groups":[]}`)
		}
		return 0
	}
	var stdout bytes.Buffer
	code := RunMaintainTUI(context.Background(), nil, &stdout)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "no review suspects") {
		t.Errorf("stdout = %q, want 'no review suspects' notice", stdout.String())
	}
}

// RF7 — 비터미널(파이프): 폴백 exit 3 + 안내(Run 래퍼 계약 승계).
func TestRunMaintainTUINonTerminalFallback(t *testing.T) {
	orig := maintainRunner
	defer func() { maintainRunner = orig }()
	fn, _ := fakeMaintainRunner()
	maintainRunner = fn
	var stdout bytes.Buffer
	// go test stdout은 파이프 — Run의 비터미널 판정이 자동으로 참.
	code := RunMaintainTUI(context.Background(), nil, &stdout)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (stdout: %s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "TUI requires a terminal") {
		t.Errorf("stdout = %q, want terminal fallback notice", stdout.String())
	}
}
