// pending.go — pending 검토 TUI. 대기 지식 목록(bubbles list)
// → 상세(본문·출처) → 키 액션(a approve·s supersede 대상 선택·d skip·
// e expire·q quit). 액션은 기존 경로 재사용 — approve/supersede 모두
// knowledge.RunApprove(approve --id N [--supersede OLD] — maintain
// --supersede와 동일 UPDATE)에, expire는 ops.RunMaintain(maintain
// --expire-pending N --commit)에 위임하고 TUI 자체는 쓰지 않는다.
// 데이터는 pending 테이블 읽기 조회(판정 Why 등은 CLI 경로 소유).
package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// PendingItem — 검토 목록 1행(제목·confidence·날짜 + 상세 본문·출처).
type PendingItem struct {
	ID                      int
	Title, Confidence, Date string
	Body, Origin            string
	// ValidAt — e 액션의 expire 위임 일수 산출 원천(valid_at 원값).
	ValidAt time.Time
}

// SupersedeTarget — supersede 대상 후보(active 지식).
type SupersedeTarget struct {
	ID    int
	Title string
}

// PendingActions — 액션 위임 seam(테스트 fake 주입). 반환은 (위임 경로
// exit code, 실패 원인) — 0만 성공(항목 제거 판정). 원인은 code != 0일
// 때만 의미가 있고 sink JSON 파싱 산물이다(계약 5 — "exit N" 단독 표기 폐지).
type PendingActions struct {
	Approve   func(ctx context.Context, id int) (int, string)
	Supersede func(ctx context.Context, id, oldID int) (int, string)
	// Expire — 선택 항목 만료 위임(id·위임 일수). maintain
	// --expire-pending N --commit 경로(해당 항목이 컷오프에 포함되는 일수).
	Expire func(ctx context.Context, id, days int) (int, string)
}

// pendingApproveRunner — RunApprove 주입 seam. 기본 실구현(중복 구현
// 금지 — 인자 조립만 이 패키지 소유), 테스트는 fake으로 교체.
var pendingApproveRunner = knowledge.RunApprove

// pendingMaintainRunner — RunMaintain 주입 seam(expire 위임 —
// pendingApproveRunner와 같은 관례). 테스트는 fake으로 교체.
var pendingMaintainRunner = ops.RunMaintain

// defaultPendingActions — 실구현 액션. RunApprove·RunMaintain의 JSON 출력은
// 버퍼로 흡수(TUI 화면 오염 방지 — 결과는 status 라인으로 요약). 실패 시
// 흡수된 출력에서 원인을 파싱해 함께 돌려준다.
func defaultPendingActions(st wire.Store) PendingActions {
	run := func(ctx context.Context, args []string) (int, string) {
		var sink bytes.Buffer
		code := pendingApproveRunner(ctx, st, args, strings.NewReader(""), &sink)
		if code != 0 {
			return code, actionFailureCause(sink.String())
		}
		return code, ""
	}
	runMaintain := func(ctx context.Context, args []string) (int, string) {
		var sink bytes.Buffer
		code := pendingMaintainRunner(ctx, st, args, strings.NewReader(""), &sink)
		if code != 0 {
			return code, actionFailureCause(sink.String())
		}
		return code, ""
	}
	return PendingActions{
		Approve: func(ctx context.Context, id int) (int, string) {
			return run(ctx, []string{"--id", strconv.Itoa(id)})
		},
		Supersede: func(ctx context.Context, id, oldID int) (int, string) {
			return run(ctx, []string{"--id", strconv.Itoa(id), "--supersede", strconv.Itoa(oldID)})
		},
		Expire: func(ctx context.Context, id, days int) (int, string) {
			return runMaintain(ctx, []string{"--expire-pending", strconv.Itoa(days), "--commit"})
		},
	}
}

// actionFailureCause — 위임 경로 실패 출력(sink)에서 원인 추출. 각 라인을
// JSON 객체로 시도해 error·message(supersede 실패 경로는 supersede_error)를
// 조립한다. JSON 객체가 없으면 ""(호출부는 exit code 폴백 표기).
func actionFailureCause(sink string) string {
	for _, line := range strings.Split(sink, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var v struct {
			Error          string `json:"error"`
			Message        string `json:"message"`
			SupersedeError string `json:"supersede_error"`
		}
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			continue
		}
		switch {
		case v.Error != "" && v.Message != "":
			return v.Error + ": " + v.Message
		case v.Message != "":
			return v.Message
		case v.Error != "":
			return v.Error
		case v.SupersedeError != "":
			return v.SupersedeError
		}
	}
	return ""
}

// actionFailedStatus — 실패 상태 라인 조립: 원인 우선, 없으면 exit code 폴백.
func actionFailedStatus(action, subj string, code int, cause string) string {
	if cause == "" {
		cause = fmt.Sprintf("exit %d", code)
	}
	return fmt.Sprintf("%s %s failed: %s", action, subj, cause)
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
			it.ValidAt = ts // e 액션 expire 일수 산출 원천
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
// exit 0, 비터미널은 Run 래퍼 폴백 exit 3.
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
	// 계약 3: supersede 후보는 s 키를 누른 시점에 로드한다(진입 시 1회
	// 전체 조회 폐지 — 대기 화면 첫 렌더 비용에서 제외).
	load := func(ctx context.Context) ([]SupersedeTarget, error) {
		return loadSupersedeTargets(ctx, st)
	}
	if err := Run(NewPendingModel(ctx, items, load, defaultPendingActions(st))); err != nil {
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

// PendingModel — pending 검토 화면 모델. 액션·후보 로더는 생성 시
// 주입(seam) — Update는 상태 전이와 위임 호출만 담당한다. 후보 목록은
// s 키 시점의 지연 로드(계약 3)로 채운다.
type PendingModel struct {
	ctx           context.Context
	items         []PendingItem
	candidates    []SupersedeTarget
	loadTargets   func(ctx context.Context) ([]SupersedeTarget, error)
	targetsLoaded bool
	acts          PendingActions
	mode          pendingMode
	list          list.Model
	targetList    list.Model
	status        string
	width         int // 마지막 WindowSizeMsg 폭(상세 본문 wordwrap 기준)
	height        int // 마지막 WindowSizeMsg 높이(저화면 크롬 분기 기준)
}

// targetsLoadedMsg — 지연 로드 완료(계약 3). err != nil이면 targets 무시.
type targetsLoadedMsg struct {
	targets []SupersedeTarget
	err     error
}

// pendingRow / targetRow — bubbles list 행.
type pendingRow struct{ it PendingItem }

func (r pendingRow) Title() string { return fmt.Sprintf("#%d  %s", r.it.ID, r.it.Title) }
func (r pendingRow) Description() string {
	return fmt.Sprintf("confidence %s · %s", r.it.Confidence, r.it.Date)
}
func (r pendingRow) FilterValue() string { return fmt.Sprintf("#%d %s", r.it.ID, r.it.Title) }

type targetRow struct{ t SupersedeTarget }

func (r targetRow) Title() string       { return fmt.Sprintf("#%d  %s", r.t.ID, r.t.Title) }
func (r targetRow) Description() string { return "" }
func (r targetRow) FilterValue() string { return r.t.Title }

// pendingRows / targetRows — 목록 전량 재조립. 필터 중
// 제거는 bubbles RemoveItem을 쓰지 않는다: RemoveItem은 items는 전역
// 인덱스로, filteredItems는 필터 위치 기준으로 지워 filteredItem.index가
// 어긋난다(두 번째 액션이 다른 항목을 지목). 모델 원본 슬라이스에서 행을
// 다시 만들어 SetItems로 반영한다(활성 필터는 SetItems가 재적용 — 반환
// cmd를 상위로 돌려야 FilterMatchesMsg가 반영된다).
func pendingRows(items []PendingItem) []list.Item {
	rows := make([]list.Item, len(items))
	for i, it := range items {
		rows[i] = pendingRow{it: it}
	}
	return rows
}

func targetRows(ts []SupersedeTarget) []list.Item {
	rows := make([]list.Item, len(ts))
	for i, t := range ts {
		rows[i] = targetRow{t: t}
	}
	return rows
}

// resetEmptyFilter — 재조립 후 필터 결과가 빈 목록이면
// 필터를 해제한다(bubbles RemoveItem의 resetFiltering 준거 — 빈 필터
// 화면에서 보이지 않는 항목 인덱스로 액션이 새어 들어가는 사고 방지).
func resetEmptyFilter(l *list.Model) {
	if l.FilterState() == list.FilterApplied && len(l.VisibleItems()) == 0 {
		l.ResetFilter()
	}
}

// pendingListHeight — 목록에 배분할 높이: View의 비목록
// 라인을 제외한 나머지. 최소 1.
//   - 높이 ≥ 12: 크롬 6행(키 안내·status·비대화 안내와 구분 공백).
//   - 저화면(< 12): 크롬 1행(키 안내만 — 비대화 안내·구분 여백·status는
//     View에서 생략, q/ESC 탈출 안내 유지).
func pendingListHeight(height int) int {
	chrome := 6
	if lowHeight(height) {
		chrome = 1
	}
	h := height - chrome
	if h < 1 {
		h = 1
	}
	return h
}

// NewPendingModel — 모델 조립(목록 크기 0x0 — 실제 크기는 시작 시
// bubbletea가 전달하는 WindowSizeMsg로 설정된다: Update 참조). 후보는
// loadTargets seam으로 s 키 시점에 로드한다.
func NewPendingModel(ctx context.Context, items []PendingItem, loadTargets func(context.Context) ([]SupersedeTarget, error), acts PendingActions) PendingModel {
	m := PendingModel{
		ctx: ctx, items: items, loadTargets: loadTargets, acts: acts, mode: modeList,
		list:       list.New(pendingRows(items), list.NewDefaultDelegate(), 0, 0),
		targetList: list.New(nil, list.NewDefaultDelegate(), 0, 0),
	}
	m.list.Title = "Pending review"
	m.targetList.Title = "Supersede target (pick the active item to replace)"
	return m
}

// Init — 시작 커맨드 없음.
func (m PendingModel) Init() tea.Cmd { return nil }

// Update — 창 크기 전이(리사이즈)와 키 입력 상태 전이. a/s/d/q는 세
// 화면 공통 관례.
func (m PendingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		h := pendingListHeight(msg.Height) // B2: 비목록 라인 제외(저화면 크롬 1행)
		m.list.SetSize(msg.Width, h)
		m.targetList.SetSize(msg.Width, h)
		// 계약 1: 40컬 이하에서는 bubbles 내장 help(53자 고정)를 끄고
		// 화면 자체 축약 키 안내로 대체한다. 저화면(< 12)에서도 같은
		// 이유로 끈다(내장 help 1행이 목록 최소 높이를 넘긴다 — 자체
		// 힌트가 q/ESC 탈출 안내를 유지한다).
		showHelp := msg.Width > 40 && !lowHeight(msg.Height)
		m.list.SetShowHelp(showHelp)
		m.targetList.SetShowHelp(showHelp)
		return m, nil
	case tea.KeyMsg:
		switch m.mode {
		case modeDetail:
			return m.updateDetail(msg)
		case modeSupersede:
			return m.updateSupersede(msg)
		default:
			return m.updateList(msg)
		}
	case targetsLoadedMsg:
		return m.applyTargets(msg)
	case list.FilterMatchesMsg:
		// bubbles 필터 결과 회신 — 모델이 list에 재주입해야
		// filteredItems가 갱신된다(전달하지 않으면 필터가 화면에만
		// 걸리고 목록은 좁혀지지 않는다). 라우팅: supersede 화면은
		// targetList, 그 외에는 m.list(필터 입력은 표시 중 list에서만
		// 발생한다).
		var cmd tea.Cmd
		if m.mode == modeSupersede {
			m.targetList, cmd = m.targetList.Update(msg)
			resetEmptyFilter(&m.targetList)
		} else {
			m.list, cmd = m.list.Update(msg)
			resetEmptyFilter(&m.list)
		}
		return m, cmd
	default:
		return m, nil
	}
}

// updateList — 목록 화면: enter 상세·a 승격·s 대체·d 건너뛰기.
// 필터 입력 중(list.Filtering — '/'로 진입)에는 모든 키를 list에 위임
// 한다: a/s/d/enter/q도 필터 텍스트·필터 확정으로 소비돼야 하며, 액션
// 키가 새어 들어가 필터와 다른 항목에 approve/supersede하는 사고를
// 막는다(#id 필터 회귀 방지).
func (m PendingModel) updateList(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.list.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(key)
		return m, cmd
	}
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "a":
		return m.approveSelected()
	case "s":
		return m.enterSupersede()
	case "e":
		return m.expireSelected()
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
	case "e":
		return m.expireSelected()
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

// updateSupersede — 대상 선택 화면: enter 확정·esc 취소. 성공 시 대체된
// old도 후보에서 제거한다(계약 4 — stale 후보 잔류 폐지). 선택 인덱스는
// GlobalIndex(필터 적용 시 visible 인덱스 → 전체 인덱스 변환 —
// list.Index는 필터 기준이라 candidates와 어긋난다)로 잡는다. 필터 입력
// 중(targetList.Filtering) 키는 list에 위임(enter 필터 확정·esc 취소는
// bubbles 소관).
func (m PendingModel) updateSupersede(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.targetList.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.targetList, cmd = m.targetList.Update(key)
		return m, cmd
	}
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeList
		m.status = ""
		return m, nil
	case "enter":
		idx := m.list.GlobalIndex()
		tidx := m.targetList.GlobalIndex()
		if idx >= len(m.items) || tidx >= len(m.candidates) {
			return m, nil
		}
		it, old := m.items[idx], m.candidates[tidx]
		code, cause := m.acts.Supersede(m.ctx, it.ID, old.ID)
		m.mode = modeList
		if code != 0 {
			m.status = actionFailedStatus("supersede", fmt.Sprintf("#%d with #%d", old.ID, it.ID), code, cause)
			return m, nil
		}
		m.status = fmt.Sprintf("superseded #%d with #%d", old.ID, it.ID)
		// B1: 대상 후보 제거도 SetItems 전량 재조립(필터 중 확정 시
		// RemoveItem 비대칭으로 targetList의 filteredItem.index가 어긋난다).
		m.candidates = append(m.candidates[:tidx], m.candidates[tidx+1:]...)
		tcmd := m.targetList.SetItems(targetRows(m.candidates))
		n, rcmd := m.removeItem(idx)
		return n, tea.Batch(tcmd, rcmd)
	default:
		var cmd tea.Cmd
		m.targetList, cmd = m.targetList.Update(key)
		return m, cmd
	}
}

// approveSelected — 선택 항목 approve 위임. 성공(0)만 목록에서 제거.
// idx는 GlobalIndex — 필터 적용 시 list.Index는 visible(필터) 인덱스라
// m.items와 어긋나 잘못된 항목을 승인할 수 있다(#id 필터 회귀).
func (m PendingModel) approveSelected() (tea.Model, tea.Cmd) {
	idx := m.list.GlobalIndex()
	if idx >= len(m.items) {
		return m, nil
	}
	it := m.items[idx]
	code, cause := m.acts.Approve(m.ctx, it.ID)
	if code != 0 {
		m.status = actionFailedStatus("approve", fmt.Sprintf("#%d", it.ID), code, cause)
		return m, nil
	}
	m.status = fmt.Sprintf("approved #%d", it.ID)
	return m.removeItem(idx)
}

// expireSelected — 선택 항목 만료 위임. 위임 일수는 항목
// valid_at 경과에서 산출(컷오프가 해당 항목을 포함하는 최대 정수일 —
// expireDaysFor). 성공(0)만 목록에서 제거. idx는 GlobalIndex(필터 회귀
// 승계 — approveSelected와 동일 계약). days < 1(1일 미만 항목)은 위임
// 경로의 무효 인자 오류로 실패 표시된다(원인 파싱 계약 5 승계).
func (m PendingModel) expireSelected() (tea.Model, tea.Cmd) {
	idx := m.list.GlobalIndex()
	if idx >= len(m.items) {
		return m, nil
	}
	if m.acts.Expire == nil {
		return m, nil
	}
	it := m.items[idx]
	days := expireDaysFor(it.ValidAt, time.Now())
	code, cause := m.acts.Expire(m.ctx, it.ID, days)
	if code != 0 {
		m.status = actionFailedStatus("expire", fmt.Sprintf("#%d", it.ID), code, cause)
		return m, nil
	}
	m.status = fmt.Sprintf("expired #%d (maintain --expire-pending %d --commit — cutoff expires all pending items ≤ %d days old)", it.ID, days, days)
	return m.removeItem(idx)
}

// expireDaysFor — maintain --expire-pending 위임 일수: 컷오프(now - days
// ·24h)가 해당 항목 valid_at보다 미래여야 조회(valid_at < 컷오프)가 항목을
// 포함하므로, days*24h < 경과를 만족하는 최대 정수일(경계: 경과 10일 정확히
// → 9). 1일 미만은 0(위임 경로가 무효 인자로 거부 — 오삭제 방지).
func expireDaysFor(validAt, now time.Time) int {
	if validAt.IsZero() || !now.After(validAt) {
		return 0
	}
	d := int(math.Ceil(now.Sub(validAt).Hours()/24) - 1)
	if d < 0 {
		return 0
	}
	return d
}

// enterSupersede — 대상 선택 진입(계약 3: s 시점 지연 로드). 이미 로드된
// 후보는 재조회 없이 즉시 진입하고, 첫 진입은 로드 커맨드를 예약한다
// (status "loading targets..." → 로드 완료 메시지로 화면 전환).
func (m PendingModel) enterSupersede() (tea.Model, tea.Cmd) {
	if m.targetsLoaded {
		return m.applyTargets(targetsLoadedMsg{targets: m.candidates})
	}
	m.status = "loading targets..."
	loader, ctx := m.loadTargets, m.ctx
	return m, func() tea.Msg {
		if loader == nil {
			return targetsLoadedMsg{}
		}
		targets, err := loader(ctx)
		return targetsLoadedMsg{targets: targets, err: err}
	}
}

// applyTargets — 지연 로드 완료 적용: 실패는 원인 안내(재시도 가능 —
// loaded 유지 안 함), 0건은 안내만, 그 외 후보 화면 전환.
func (m PendingModel) applyTargets(msg targetsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("load targets failed: %v", msg.err)
		return m, nil
	}
	m.targetsLoaded = true
	m.candidates = msg.targets
	cmd := m.targetList.SetItems(targetRows(m.candidates)) // B1: 재조립 cmd도 상위 반환
	if len(m.candidates) == 0 {
		m.status = "no active items to supersede"
		return m, cmd
	}
	m.mode = modeSupersede
	m.targetList.Select(0)
	m.status = ""
	return m, cmd
}

// removeItem — 목록에서 항목 제거 후 빈 목록이면 종료. 제거는 SetItems
// 전량 재조립(B1): 반환 cmd(재필터 FilterMatchesMsg)을 호출부가 상위로
// 돌려야 활성 필터가 다시 적용된다.
func (m PendingModel) removeItem(idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(m.items) {
		return m, nil
	}
	m.items = append(m.items[:idx], m.items[idx+1:]...)
	cmd := m.list.SetItems(pendingRows(m.items))
	if len(m.items) == 0 {
		return m, tea.Batch(cmd, tea.Quit)
	}
	return m, cmd
}

// wrapDetailBody — 상세 본문 wordwrap. 폭은 저장된 창 폭-2(저장 폭이
// 없으면 78) — 창보다 넓은 단일 라인이 가로로 넘치지 않게 한다.
func wrapDetailBody(width int, body string) string {
	w := 78
	if width > 0 {
		w = width - 2
	}
	if w < 1 {
		w = 1
	}
	return lipgloss.NewStyle().Width(w).Render(body)
}

// keyHint — 키 안내 축약 표기(계약 1): 40컬 이하는 short("q quit" 단서
// 유지), 그 외는 full. 폭 미저장(리사이즈 전)은 full.
func keyHint(width int, full, short string) string {
	if width > 0 && width <= 40 {
		return short
	}
	return full
}

// wrapHint — 안내 라인 wordwrap(계약 1 — nonInteractiveHint 등). 폭 미저장
// 리사이즈 전은 원문 그대로.
func wrapHint(width int, s string) string {
	if width <= 0 {
		return s
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}

// View — 화면 렌더. 키 안내는 영어 고정(플러그인 룰). 저화면(높이
// < 12)에서는 선택 크롬(비대화 안내·구분 여백·status)을 생략하고 키
// 안내(q/ESC 탈출)만 유지한다 — 임계 이상 레이아웃은 바이트 불변.
func (m PendingModel) View() string {
	low := lowHeight(m.height)
	var b strings.Builder
	// writeHint — 키 안내 라인. lead는 모드 구분(목록 계열은 list.View가
	// 개행 없이 끝나므로 "\n", 상세는 ""). 저화면은 접미 개행만 생략
	// (trailing newline이 렌더 라인 수를 1줄 늘린다).
	writeHint := func(lead, full, short string) {
		suffix := "\n"
		if low {
			suffix = ""
		}
		fmt.Fprintf(&b, "%s%s%s", lead, Theme.Secondary.Render(keyHint(m.width, full, short)), suffix)
	}
	switch m.mode {
	case modeDetail:
		// 필터 중 상세 표시도 GlobalIndex로(선택 항목 일치).
		idx := m.list.GlobalIndex()
		if idx < 0 || idx >= len(m.items) {
			return "no item selected"
		}
		it := m.items[idx]
		b.WriteString(Theme.Accent.Render(fmt.Sprintf("#%d  %s", it.ID, it.Title)))
		b.WriteString("\n\n")
		fmt.Fprintf(&b, "%s\n\n", wrapDetailBody(m.width, it.Body))
		fmt.Fprintf(&b, "%s\n", Theme.Neutral.Render(wrapDetailBody(m.width,
			fmt.Sprintf("origin: %s · confidence %s · date %s", it.Origin, it.Confidence, it.Date))))
		writeHint("", "a approve · s supersede · d skip · e expire · esc back · q quit",
			"a/s/d/e/esc · q quit")
	case modeSupersede:
		b.WriteString(m.targetList.View())
		writeHint("\n", "enter confirm · esc cancel · q quit", "enter/esc · q quit")
	default:
		b.WriteString(m.list.View())
		writeHint("\n", "enter detail · a approve · s supersede · d skip · e expire · q quit",
			"a/s/d/e/enter · q quit")
	}
	if m.status != "" && !low {
		fmt.Fprintf(&b, "\n%s\n", Theme.Accent.Render(m.status))
	}
	if !low {
		// 비대화 탈출 안내(적응형 기본 진입 안내) — 화면 최하단.
		// 계약 1: 폭 저장 시 wordwrap(협폭 단말 가로 넘침 방지).
		// trailing newline 제거 — 마지막 개행이 렌더 라인 수를 1줄 늘린다.
		fmt.Fprintf(&b, "\n%s", Theme.Secondary.Render(wrapHint(m.width, nonInteractiveHint)))
	}
	return b.String()
}
