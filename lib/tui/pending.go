// pending.go — v1.5.0 T7: pending 검토 TUI. 대기 지식 목록(bubbles list)
// → 상세(본문·출처) → 키 액션(a approve·s supersede 대상 선택·d skip·
// q quit). 액션은 기존 경로 재사용 — approve/supersede 모두
// knowledge.RunApprove(approve --id N [--supersede OLD] — maintain
// --supersede와 동일 UPDATE)에 위임하고 TUI 자체는 쓰지 않는다.
// 데이터는 pending 테이블 읽기 조회(판정 Why 등은 CLI 경로 소유).
package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// PendingItem — 검토 목록 1행(제목·confidence·날짜 + 상세 본문·출처).
type PendingItem struct {
	ID                      int
	Title, Confidence, Date string
	Body, Origin            string
}

// SupersedeTarget — supersede 대상 후보(active 지식).
type SupersedeTarget struct {
	ID    int
	Title string
}

// PendingActions — 액션 위임 seam(테스트 fake 주입). 반환값은 위임 경로의
// exit code(0만 성공 — 항목 제거 판정).
type PendingActions struct {
	Approve   func(ctx context.Context, id int) int
	Supersede func(ctx context.Context, id, oldID int) int
}

// pendingApproveRunner — RunApprove 주입 seam. 기본 실구현(중복 구현
// 금지 — 인자 조립만 이 패키지 소유), 테스트는 fake으로 교체.
var pendingApproveRunner = knowledge.RunApprove

// defaultPendingActions — 실구현 액션. RunApprove의 JSON 출력은 버퍼로
// 흡수(TUI 화면 오염 방지 — 결과는 status 라인으로 요약).
func defaultPendingActions(st wire.Store) PendingActions {
	run := func(ctx context.Context, args []string) int {
		var sink bytes.Buffer
		return pendingApproveRunner(ctx, st, args, strings.NewReader(""), &sink)
	}
	return PendingActions{
		Approve: func(ctx context.Context, id int) int {
			return run(ctx, []string{"--id", strconv.Itoa(id)})
		},
		Supersede: func(ctx context.Context, id, oldID int) int {
			return run(ctx, []string{"--id", strconv.Itoa(id), "--supersede", strconv.Itoa(oldID)})
		},
	}
}

// loadPendingItems — 대기 지식 조회(id·topic·summary·origin·valid_at·
// confidence). 저장값은 재살귑(render.Text — pending CLI 경로와 동일
// 관례).
func loadPendingItems(ctx context.Context, st wire.Store) ([]PendingItem, error) {
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT id, topic, summary, origin, valid_at, confidence
		FROM knowledge WHERE status = 'pending' AND invalid_at IS NULL
		ORDER BY valid_at, id`, nil)
	if err != nil {
		return nil, err
	}
	items := make([]PendingItem, 0, len(rows))
	for _, r := range rows {
		if len(r) < 6 {
			continue
		}
		it := PendingItem{
			Title:  knowledge.OneLine(render.Text(fmt.Sprint(r[1])), 60),
			Body:   render.Text(fmt.Sprint(r[2])),
			Origin: knowledge.OneLine(render.Text(fmt.Sprint(r[3])), 60),
		}
		fmt.Sscan(fmt.Sprint(r[0]), &it.ID)
		if ts, ok := wire.CellToTime(r[4]); ok {
			it.Date = ts.UTC().Format("2006-01-02")
		}
		it.Confidence = confidenceLabel(r[5])
		items = append(items, it)
	}
	return items, nil
}

// confidenceLabel — confidence 셀(float·문자열 모두)의 고정 소수 2자 표기.
func confidenceLabel(v any) string {
	var f float64
	switch c := v.(type) {
	case float64:
		f = c
	case string:
		if _, err := fmt.Sscan(c, &f); err != nil {
			return ""
		}
	default:
		if _, err := fmt.Sscan(fmt.Sprint(v), &f); err != nil {
			return ""
		}
	}
	return fmt.Sprintf("%.2f", f)
}

// loadSupersedeTargets — supersede 대상 active 목록(전체 1회 조회).
func loadSupersedeTargets(ctx context.Context, st wire.Store) ([]SupersedeTarget, error) {
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT id, topic FROM knowledge
		WHERE status = 'active' AND invalid_at IS NULL
		ORDER BY id`, nil)
	if err != nil {
		return nil, err
	}
	out := make([]SupersedeTarget, 0, len(rows))
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		var t SupersedeTarget
		fmt.Sscan(fmt.Sprint(r[0]), &t.ID)
		t.Title = knowledge.OneLine(render.Text(fmt.Sprint(r[1])), 60)
		out = append(out, t)
	}
	return out, nil
}

// RunPendingTUI — `flint pending --tui` 진입. 대기 0건은 TUI 없이 안내
// exit 0, 비터미널은 Run 래퍼 폴백 exit 3(T5 계약 승계).
func RunPendingTUI(ctx context.Context, st wire.Store, stdout io.Writer) int {
	items, err := loadPendingItems(ctx, st)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(items) == 0 {
		fmt.Fprintln(stdout, "no pending items — session knowledge and import refill the queue")
		return 0
	}
	targets, err := loadSupersedeTargets(ctx, st)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if err := Run(NewPendingModel(ctx, items, targets, defaultPendingActions(st))); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	return 0
}

// pendingMode — 화면 상태(list → detail / list → supersede).
type pendingMode int

const (
	modeList pendingMode = iota
	modeDetail
	modeSupersede
)

// PendingModel — pending 검토 화면 모델. 액션·데이터 로드는 생성 시
// 주입(seam) — Update는 상태 전이와 위임 호출만 담당한다.
type PendingModel struct {
	ctx        context.Context
	items      []PendingItem
	candidates []SupersedeTarget
	acts       PendingActions
	mode       pendingMode
	list       list.Model
	targetList list.Model
	status     string
}

// pendingRow / targetRow — bubbles list 행.
type pendingRow struct{ it PendingItem }

func (r pendingRow) Title() string { return fmt.Sprintf("#%d  %s", r.it.ID, r.it.Title) }
func (r pendingRow) Description() string {
	return fmt.Sprintf("confidence %s · %s", r.it.Confidence, r.it.Date)
}
func (r pendingRow) FilterValue() string { return r.it.Title }

type targetRow struct{ t SupersedeTarget }

func (r targetRow) Title() string       { return fmt.Sprintf("#%d  %s", r.t.ID, r.t.Title) }
func (r targetRow) Description() string { return "" }
func (r targetRow) FilterValue() string { return r.t.Title }

// NewPendingModel — 모델 조립(목록 80x20 — 터미널 리사이즈 전 기본).
func NewPendingModel(ctx context.Context, items []PendingItem, candidates []SupersedeTarget, acts PendingActions) PendingModel {
	rows := make([]list.Item, len(items))
	for i, it := range items {
		rows[i] = pendingRow{it: it}
	}
	trows := make([]list.Item, len(candidates))
	for i, t := range candidates {
		trows[i] = targetRow{t: t}
	}
	m := PendingModel{
		ctx: ctx, items: items, candidates: candidates, acts: acts, mode: modeList,
		list:       list.New(rows, list.NewDefaultDelegate(), 80, 20),
		targetList: list.New(trows, list.NewDefaultDelegate(), 80, 20),
	}
	m.list.Title = "Pending review"
	m.targetList.Title = "Supersede target (pick the active item to replace)"
	return m
}

// Init — 시작 커맨드 없음.
func (m PendingModel) Init() tea.Cmd { return nil }

// Update — 키 입력 상태 전이. a/s/d/q는 세 화면 공통 관례.
func (m PendingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch m.mode {
	case modeDetail:
		return m.updateDetail(key)
	case modeSupersede:
		return m.updateSupersede(key)
	default:
		return m.updateList(key)
	}
}

// updateList — 목록 화면: enter 상세·a 승격·s 대체·d 건너뛰기.
func (m PendingModel) updateList(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "a":
		return m.approveSelected()
	case "s":
		return m.enterSupersede()
	case "d":
		m.list.CursorDown()
		m.status = "skipped"
		return m, nil
	case "enter":
		if len(m.items) > 0 {
			m.mode = modeDetail
			m.status = ""
		}
		return m, nil
	default:
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(key)
		return m, cmd
	}
}

// updateDetail — 상세 화면: esc 복귀, a/s/d는 목록과 동일 위임.
func (m PendingModel) updateDetail(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "a":
		return m.approveSelected()
	case "s":
		return m.enterSupersede()
	case "d":
		m.mode = modeList
		m.list.CursorDown()
		m.status = "skipped"
		return m, nil
	case "esc", "enter", "left":
		m.mode = modeList
		return m, nil
	default:
		return m, nil
	}
}

// updateSupersede — 대상 선택 화면: enter 확정·esc 취소.
func (m PendingModel) updateSupersede(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeList
		m.status = ""
		return m, nil
	case "enter":
		idx := m.list.Index()
		tidx := m.targetList.Index()
		if idx >= len(m.items) || tidx >= len(m.candidates) {
			return m, nil
		}
		it, old := m.items[idx], m.candidates[tidx]
		code := m.acts.Supersede(m.ctx, it.ID, old.ID)
		m.mode = modeList
		if code != 0 {
			m.status = fmt.Sprintf("supersede #%d with #%d failed (exit %d)", old.ID, it.ID, code)
			return m, nil
		}
		m.status = fmt.Sprintf("superseded #%d with #%d", old.ID, it.ID)
		return m.removeItem(idx)
	default:
		var cmd tea.Cmd
		m.targetList, cmd = m.targetList.Update(key)
		return m, cmd
	}
}

// approveSelected — 선택 항목 approve 위임. 성공(0)만 목록에서 제거.
func (m PendingModel) approveSelected() (tea.Model, tea.Cmd) {
	idx := m.list.Index()
	if idx >= len(m.items) {
		return m, nil
	}
	it := m.items[idx]
	code := m.acts.Approve(m.ctx, it.ID)
	if code != 0 {
		m.status = fmt.Sprintf("approve #%d failed (exit %d)", it.ID, code)
		return m, nil
	}
	m.status = fmt.Sprintf("approved #%d", it.ID)
	return m.removeItem(idx)
}

// enterSupersede — 대상 선택 화면 진입(후보 없으면 안내만).
func (m PendingModel) enterSupersede() (tea.Model, tea.Cmd) {
	if len(m.candidates) == 0 {
		m.status = "no active items to supersede"
		return m, nil
	}
	m.mode = modeSupersede
	m.targetList.Select(0)
	m.status = ""
	return m, nil
}

// removeItem — 목록에서 항목 제거 후 빈 목록이면 종료.
func (m PendingModel) removeItem(idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(m.items) {
		return m, nil
	}
	m.items = append(m.items[:idx], m.items[idx+1:]...)
	m.list.RemoveItem(idx)
	if len(m.items) == 0 {
		return m, tea.Quit
	}
	return m, nil
}

// View — 화면 렌더. 키 안내는 영어 고정(플러그인 룰).
func (m PendingModel) View() string {
	var b strings.Builder
	switch m.mode {
	case modeDetail:
		idx := m.list.Index()
		if idx >= len(m.items) {
			return "no item selected"
		}
		it := m.items[idx]
		b.WriteString(Theme.Accent.Render(fmt.Sprintf("#%d  %s", it.ID, it.Title)))
		b.WriteString("\n\n")
		fmt.Fprintf(&b, "%s\n\n", it.Body)
		fmt.Fprintf(&b, "%s\n", Theme.Neutral.Render(fmt.Sprintf("origin: %s · confidence %s · date %s", it.Origin, it.Confidence, it.Date)))
		fmt.Fprintf(&b, "%s\n", Theme.Secondary.Render("a approve · s supersede · d skip · esc back · q quit"))
	case modeSupersede:
		b.WriteString(m.targetList.View())
		fmt.Fprintf(&b, "\n%s\n", Theme.Secondary.Render("enter confirm · esc cancel · q quit"))
	default:
		b.WriteString(m.list.View())
		fmt.Fprintf(&b, "\n%s\n", Theme.Secondary.Render("enter detail · a approve · s supersede · d skip · q quit"))
	}
	if m.status != "" {
		fmt.Fprintf(&b, "\n%s\n", Theme.Accent.Render(m.status))
	}
	return b.String()
}
