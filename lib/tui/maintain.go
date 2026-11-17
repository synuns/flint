// maintain.go — v1.6.0 T6: maintain 검토 TUI(`flint maintain --tui` —
// 옵트인만). dup-suspects/conflict-suspects/stale 3소스 탭 목록(bubbles
// list) → 상세(양쪽 본문 + "무엇이 보존되는지" 미리보기 — merge UX 원칙)
// → 키 액션: m(merge — maintain --merge 위임)·k(keep — 무시)·i(invalidate
// --ids N --yes 위임·되돌림 경로 안내)·q. TUI 자체는 쓰지 않고 전부
// 기존 maintain 서브커맨드(ops.RunMaintain)에 위임한다(서브커맨드 인자가
// 복잡하므로 중복 구현 금지). pending TUI 패턴 승계: GlobalIndex(필터
// 회귀)·sink 원인 파싱(actionFailureCause 재사용)·지연 본문 로드·
// nonInteractiveHint. 데이터 조회는 로더 seam(T1 pending 수리와 결합도
// 분리 — 조회 구현 교체가 모델에 스며들지 않는다).
package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// MaintainSource — 검토 소스 3종(탭 순서).
type MaintainSource int

const (
	MaintainDup MaintainSource = iota
	MaintainConflict
	MaintainStale
)

// label — 소스 탭 라벨(maintain 서브커맨드명과 동일 표기).
func (s MaintainSource) label() string {
	switch s {
	case MaintainConflict:
		return "conflict-suspects"
	case MaintainStale:
		return "stale"
	default:
		return "dup-suspects"
	}
}

// MaintainSuspect — 검토 목록 1행. dup/conflict는 동일 topic 그룹(ids
// 2개+·conflict는 ids 대응 요약 접두), stale는 단일 항목(사유·경과일).
type MaintainSuspect struct {
	Source    MaintainSource
	Topic     string
	IDs       []int
	Summaries []string // conflict: ids 대응 요약 접두(보고 출력 승계)
	Reason    string   // stale: never_read | stale
	StaleDays int
}

// MaintainBody — 상세·선택 화면용 지식 본문(id 배치 조회 산물).
type MaintainBody struct {
	ID              int
	Topic, Summary  string
	Origin, ValidAt string
}

// MaintainData — 3소스 검토 대상 스냅샷(진입 시 1회 로드).
type MaintainData struct {
	Dup, Conflict, Stale []MaintainSuspect
}

// Empty — 3소스 전부 비었는지(진입 안내 판정).
func (d MaintainData) Empty() bool {
	return len(d.Dup) == 0 && len(d.Conflict) == 0 && len(d.Stale) == 0
}

// MaintainActions — 액션 위임 seam(테스트 fake 주입). 반환은 (위임 경로
// exit code, 실패 원인) — 원인은 sink JSON 파싱 산물(계약 5 승계).
type MaintainActions struct {
	Merge      func(ctx context.Context, from, to int) (int, string)
	Invalidate func(ctx context.Context, id int) (int, string)
}

// MaintainLoaders — 데이터 로더 seam. Suspects는 진입 시 1회, Bodies는
// 상세·선택 진입 시점 지연 로드(대량 본문을 첫 렌더에서 제외).
type MaintainLoaders struct {
	Suspects func(ctx context.Context) (MaintainData, error)
	Bodies   func(ctx context.Context, ids []int) (map[int]MaintainBody, error)
}

// maintainRunner — ops.RunMaintain 주입 seam. 기본 실구현(중복 구현
// 금지 — 보고 SQL·쓰기 경로 전부 maintain 서브커맨드 소유), 테스트 fake.
var maintainRunner = ops.RunMaintain

// defaultMaintainActions — 실구현 액션. RunMaintain의 JSON 출력은 버퍼로
// 흡수(TUI 화면 오염 방지 — pending defaultPendingActions 관례). 실패 시
// 흡수 출력에서 원인을 파싱해 함께 돌려준다.
func defaultMaintainActions(st wire.Store) MaintainActions {
	run := func(ctx context.Context, args []string) (int, string) {
		var sink bytes.Buffer
		code := maintainRunner(ctx, st, args, strings.NewReader(""), &sink)
		if code != 0 {
			return code, actionFailureCause(sink.String())
		}
		return code, ""
	}
	return MaintainActions{
		Merge: func(ctx context.Context, from, to int) (int, string) {
			return run(ctx, []string{"--merge", "--from", strconv.Itoa(from), "--to", strconv.Itoa(to)})
		},
		Invalidate: func(ctx context.Context, id int) (int, string) {
			return run(ctx, []string{"--invalidate", "--ids", strconv.Itoa(id), "--yes"})
		},
	}
}

// defaultMaintainLoaders — 실구현 로더. Suspects는 maintain 보고
// 서브커맨드 3종의 JSON 출력을 파싱한다(SQL·분류 단일 소스 — ops 관례
// 재사용). Bodies는 id 배치 조회(재살귑 render.Text 경유 — pending 관례).
func defaultMaintainLoaders(st wire.Store) MaintainLoaders {
	return MaintainLoaders{
		Suspects: func(ctx context.Context) (MaintainData, error) {
			return loadMaintainSuspects(ctx, st)
		},
		Bodies: func(ctx context.Context, ids []int) (map[int]MaintainBody, error) {
			return loadMaintainBodies(ctx, st, ids)
		},
	}
}

// maintainReportJSON — 위임 보고 1회 실행 + 첫 JSON 라인 추출. exit != 0
// 이면 원인(sink 파싱·없으면 exit code)을 오류로 돌린다.
func maintainReportJSON(ctx context.Context, st wire.Store, args []string) ([]byte, error) {
	var sink bytes.Buffer
	code := maintainRunner(ctx, st, args, strings.NewReader(""), &sink)
	var line string
	for _, l := range strings.Split(sink.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "{") {
			line = strings.TrimSpace(l)
			break
		}
	}
	if code != 0 {
		cause := actionFailureCause(sink.String())
		if cause == "" {
			cause = fmt.Sprintf("exit %d", code)
		}
		return nil, fmt.Errorf("maintain %s failed: %s", args[0], cause)
	}
	if line == "" {
		return nil, fmt.Errorf("maintain %s: no report output", args[0])
	}
	return []byte(line), nil
}

// loadMaintainSuspects — 3소스 보고 파싱. 그룹 산식·모순 분류·stale
// 경계는 maintain 서브커맨드가 이미 소유한다(여기서 재구현하지 않는다).
func loadMaintainSuspects(ctx context.Context, st wire.Store) (MaintainData, error) {
	var data MaintainData

	// v1.6.1 T3(B3): 보고 3종은 상한을 명시한다(ops.MaintainTUILoadLimit
	// 단일 소스) — 기본 10/20으로 잘려도 TUI 화면에는 잘림이 표시되지
	// 않는다(잘림 무표시 폐지). 인자 순서는 파서 표 기반이라 무관하다.
	dupRaw, err := maintainReportJSON(ctx, st,
		[]string{"--limit", strconv.Itoa(ops.MaintainTUILoadLimit), "--dup-suspects"})
	if err != nil {
		return data, err
	}
	var dupOut struct {
		Groups []struct {
			Topic string `json:"topic"`
			IDs   []int  `json:"ids"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(dupRaw, &dupOut); err != nil {
		return data, fmt.Errorf("dup-suspects parse: %w", err)
	}
	for _, g := range dupOut.Groups {
		data.Dup = append(data.Dup, MaintainSuspect{
			Source: MaintainDup, Topic: render.Text(g.Topic), IDs: g.IDs,
		})
	}

	conflRaw, err := maintainReportJSON(ctx, st,
		[]string{"--limit", strconv.Itoa(ops.MaintainTUILoadLimit), "--conflict-suspects"})
	if err != nil {
		return data, err
	}
	var conflOut struct {
		Groups []struct {
			Topic     string   `json:"topic"`
			IDs       []int    `json:"ids"`
			Summaries []string `json:"summaries"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(conflRaw, &conflOut); err != nil {
		return data, fmt.Errorf("conflict-suspects parse: %w", err)
	}
	for _, g := range conflOut.Groups {
		sums := make([]string, 0, len(g.Summaries))
		for _, s := range g.Summaries {
			sums = append(sums, render.Text(s))
		}
		data.Conflict = append(data.Conflict, MaintainSuspect{
			Source: MaintainConflict, Topic: render.Text(g.Topic), IDs: g.IDs, Summaries: sums,
		})
	}

	staleRaw, err := maintainReportJSON(ctx, st,
		[]string{"--limit", strconv.Itoa(ops.MaintainTUILoadLimit), "--stale"})
	if err != nil {
		return data, err
	}
	var staleOut struct {
		Items []struct {
			ID        int    `json:"id"`
			Topic     string `json:"topic"`
			NeverRead bool   `json:"never_read"`
			Reason    string `json:"reason"`
			StaleDays int    `json:"stale_days"`
		} `json:"items"`
	}
	if err := json.Unmarshal(staleRaw, &staleOut); err != nil {
		return data, fmt.Errorf("stale parse: %w", err)
	}
	for _, it := range staleOut.Items {
		data.Stale = append(data.Stale, MaintainSuspect{
			Source: MaintainStale, Topic: render.Text(it.Topic), IDs: []int{it.ID},
			Reason: it.Reason, StaleDays: it.StaleDays,
		})
	}
	return data, nil
}

// loadMaintainBodies — id 배치 본문 조회(상세·선택 화면). 저장값은 재살귑
// (render.Text — pending loadPendingItems 관례).
func loadMaintainBodies(ctx context.Context, st wire.Store, ids []int) (map[int]MaintainBody, error) {
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT id, topic, summary, origin, valid_at
		FROM knowledge WHERE id = ANY($1)`, []any{ids})
	if err != nil {
		return nil, err
	}
	out := make(map[int]MaintainBody, len(rows))
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		b := MaintainBody{
			ID:      id,
			Topic:   render.Text(fmt.Sprint(r[1])),
			Summary: render.Text(fmt.Sprint(r[2])),
			Origin:  knowledge.OneLine(render.Text(fmt.Sprint(r[3])), 60),
		}
		if ts, ok := wire.CellToTime(r[4]); ok {
			b.ValidAt = ts.UTC().Format("2006-01-02")
		}
		out[id] = b
	}
	return out, nil
}

// RunMaintainTUI — `flint maintain --tui` 진입. 검토 대상 0건은 TUI 없이
// 안내 exit 0, 비터미널은 Run 래퍼 폴백 exit 3(T5 계약 승계).
func RunMaintainTUI(ctx context.Context, st wire.Store, stdout io.Writer) int {
	ld := defaultMaintainLoaders(st)
	data, err := ld.Suspects(ctx)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if data.Empty() {
		fmt.Fprintln(stdout, "no review suspects — dup, conflict and stale lists are clean")
		return 0
	}
	if err := Run(NewMaintainModel(ctx, data, ld, defaultMaintainActions(st))); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	return 0
}

// maintainMode — 화면 상태(list → detail / list → pick).
type maintainMode int

const (
	maintainList maintainMode = iota
	maintainDetail
	maintainPick
)

// maintainBodiesMsg — 지연 본문 로드 완료. err != nil이면 bodies 무시.
// want는 대기 중 진입("detail" | "merge" | "invalidate").
type maintainBodiesMsg struct {
	bodies map[int]MaintainBody
	err    error
	want   string
}

// MaintainModel — maintain 검토 화면 모델. 로더·액션은 생성 시 주입
// (seam) — Update는 상태 전이와 위임 호출만 담당한다. 본문은 상세·선택
// 진입 시점 지연 로드 후 모델 캐시(m.bodies)에 적립한다.
type MaintainModel struct {
	ctx        context.Context
	tabs       [3][]MaintainSuspect
	lists      [3]list.Model
	tab        int
	bodies     map[int]MaintainBody
	loadBodies func(context.Context, []int) (map[int]MaintainBody, error)
	acts       MaintainActions
	mode       maintainMode
	status     string
	width      int // 마지막 WindowSizeMsg 폭(wordwrap 기준)
	height     int // 마지막 WindowSizeMsg 높이(선택 list 재조립용)
	pick       list.Model
	pickIDs    []int
	pickWhat   string // "merge" | "invalidate"
}

// suspectRow / pickRow — bubbles list 행.
type suspectRow struct{ s MaintainSuspect }

func (r suspectRow) Title() string { return r.s.Topic }
func (r suspectRow) Description() string {
	switch r.s.Source {
	case MaintainConflict:
		return strings.Join(r.s.Summaries, " · ")
	case MaintainStale:
		return fmt.Sprintf("%s · %dd", r.s.Reason, r.s.StaleDays)
	default:
		return fmt.Sprintf("%d items", len(r.s.IDs))
	}
}
func (r suspectRow) FilterValue() string {
	ids := make([]string, len(r.s.IDs))
	for i, id := range r.s.IDs {
		ids[i] = "#" + strconv.Itoa(id)
	}
	return strings.Join(ids, " ") + " " + r.s.Topic
}

// pickRow — 선택 화면 행(생존자/무효화 대상 후보).
type pickRow struct{ b MaintainBody }

func (r pickRow) Title() string       { return fmt.Sprintf("#%d  %s", r.b.ID, r.b.Summary) }
func (r pickRow) Description() string { return r.b.Origin }
func (r pickRow) FilterValue() string { return fmt.Sprintf("#%d %s", r.b.ID, r.b.Summary) }

// suspectRows — 검토 목록 전량 재조립(B1 — v1.6.1 T1). 필터 중 제거는
// bubbles RemoveItem을 쓰지 않는다: RemoveItem은 items는 전역 인덱스로,
// filteredItems는 필터 위치 기준으로 지워 filteredItem.index가 어긋난다
// (두 번째 액션이 다른 그룹을 지목). 모델 원본 슬라이스에서 행을 다시
// 만들어 SetItems로 반영한다(활성 필터는 SetItems가 재적용 — 반환 cmd를
// 상위로 돌려야 FilterMatchesMsg가 반영된다).
func suspectRows(suspects []MaintainSuspect) []list.Item {
	rows := make([]list.Item, len(suspects))
	for i, s := range suspects {
		rows[i] = suspectRow{s: s}
	}
	return rows
}

// maintainListHeight — 목록에 배분할 높이(B2 — v1.6.1 T1): View의 비목록
// 라인(소스 탭 헤더 + 키 안내·status·비대화 안내와 구분 공백 — 최대
// 7행)을 제외한 나머지. 최소 1.
func maintainListHeight(height int) int {
	h := height - 7
	if h < 1 {
		h = 1
	}
	return h
}

// NewMaintainModel — 모델 조립(목록 크기 0x0 — WindowSizeMsg로 설정).
// 초기 탭은 첫 비어 있지 않은 소스. 본문 요약이 없는 dup/stale도 선택
// 화면 행 표기용 최소 본문(ID·topic)으로 채운다(로드 전 픽스처 대비).
func NewMaintainModel(ctx context.Context, data MaintainData, ld MaintainLoaders, acts MaintainActions) MaintainModel {
	m := MaintainModel{
		ctx:        ctx,
		tabs:       [3][]MaintainSuspect{data.Dup, data.Conflict, data.Stale},
		bodies:     map[int]MaintainBody{},
		loadBodies: ld.Bodies,
		acts:       acts,
		mode:       maintainList,
	}
	for i := range m.lists {
		m.lists[i] = list.New(suspectRows(m.tabs[i]), list.NewDefaultDelegate(), 0, 0)
		m.lists[i].Title = "Maintain review"
	}
	m.pick = list.New(nil, list.NewDefaultDelegate(), 0, 0)
	m.tab = 0
	for i := 1; i < 3 && len(m.tabs[m.tab]) == 0; i++ {
		m.tab = i
	}
	return m
}

// Init — 시작 커맨드 없음.
func (m MaintainModel) Init() tea.Cmd { return nil }

// Update — 창 크기 전이와 키 입력 상태 전이.
func (m MaintainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		h := maintainListHeight(msg.Height) // B2: 비목록 라인 7행 제외
		for i := range m.lists {
			m.lists[i].SetSize(msg.Width, h)
			m.lists[i].SetShowHelp(msg.Width > 40)
		}
		m.pick.SetSize(msg.Width, h)
		m.pick.SetShowHelp(msg.Width > 40)
		return m, nil
	case tea.KeyMsg:
		switch m.mode {
		case maintainDetail:
			return m.updateDetail(msg)
		case maintainPick:
			return m.updatePick(msg)
		default:
			return m.updateList(msg)
		}
	case maintainBodiesMsg:
		return m.applyBodies(msg)
	case list.FilterMatchesMsg:
		// 필터 결과 회신 라우팅: 선택 화면은 pick, 그 외 활성 탭 list.
		var cmd tea.Cmd
		if m.mode == maintainPick {
			m.pick, cmd = m.pick.Update(msg)
		} else {
			m.lists[m.tab], cmd = m.lists[m.tab].Update(msg)
			resetEmptyFilter(&m.lists[m.tab]) // B1: 빈 재필터 해제
		}
		return m, cmd
	default:
		return m, nil
	}
}

// current — 활성 탭 선택 항목(GlobalIndex — 필터 적용 시 visible 인덱스를
// 전체 인덱스로 환산. list.Index는 필터 기준이라 tabs와 어긋난다).
func (m MaintainModel) current() (MaintainSuspect, bool) {
	idx := m.lists[m.tab].GlobalIndex()
	if idx >= len(m.tabs[m.tab]) {
		return MaintainSuspect{}, false
	}
	return m.tabs[m.tab][idx], true
}

// updateList — 목록 화면: tab 소스 전환·enter 상세·m/i/k 액션. 필터 입력
// 중(list.Filtering)에는 모든 키를 list에 위임한다(액션 키가 필터
// 텍스트로 소비돼야 한다 — pending v1.5.3 #id 필터 회귀 승계).
func (m MaintainModel) updateList(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.lists[m.tab].FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.lists[m.tab], cmd = m.lists[m.tab].Update(key)
		return m, cmd
	}
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab":
		return m.cycleTab(), nil
	case "enter":
		return m.requestBodies("detail")
	case "m":
		return m.startMerge()
	case "i":
		return m.startInvalidate()
	case "k":
		return m.keepSelected()
	default:
		var cmd tea.Cmd
		m.lists[m.tab], cmd = m.lists[m.tab].Update(key)
		return m, cmd
	}
}

// cycleTab — 다음 비어 있지 않은 소스 탭으로 전환(빈 탭 스킵).
func (m MaintainModel) cycleTab() MaintainModel {
	for i := 1; i < 3; i++ {
		next := (m.tab + i) % 3
		if len(m.tabs[next]) > 0 {
			m.tab = next
			m.status = ""
			return m
		}
	}
	return m
}

// updateDetail — 상세 화면: esc 복귀, m/i/k는 목록과 동일 위임.
func (m MaintainModel) updateDetail(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "m":
		return m.startMerge()
	case "i":
		return m.startInvalidate()
	case "k":
		return m.keepSelected()
	case "esc", "left":
		m.mode = maintainList
		m.status = ""
		return m, nil
	default:
		return m, nil
	}
}

// updatePick — 선택 화면: enter 확정·esc 취소. 선택 인덱스는
// GlobalIndex(필터 적용 시 visible → 전체 환산 — pending 관례 승계).
// 필터 입력 중(pick.Filtering) 키는 pick에 위임.
func (m MaintainModel) updatePick(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pick.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.pick, cmd = m.pick.Update(key)
		return m, cmd
	}
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = maintainList
		m.status = ""
		return m, nil
	case "enter":
		return m.confirmPick()
	default:
		var cmd tea.Cmd
		m.pick, cmd = m.pick.Update(key)
		return m, cmd
	}
}

// startMerge — m 진입: dup/conflict 그룹만(stale은 안내). 본문 미캐시면
// 지연 로드 후 선택 화면 전환.
func (m MaintainModel) startMerge() (tea.Model, tea.Cmd) {
	cur, ok := m.current()
	if !ok {
		return m, nil
	}
	if cur.Source == MaintainStale {
		m.status = "merge is for dup/conflict groups — press i to invalidate"
		return m, nil
	}
	return m.requestBodies("merge")
}

// startInvalidate — i 진입: stale은 본문 로드·선택 없이 즉시 위임,
// 그룹은 무효화할 id 선택 화면(본문 지연 로드 후).
func (m MaintainModel) startInvalidate() (tea.Model, tea.Cmd) {
	cur, ok := m.current()
	if !ok {
		return m, nil
	}
	if cur.Source == MaintainStale {
		return m.invalidateID(cur.IDs[0])
	}
	return m.requestBodies("invalidate")
}

// requestBodies — 상세·선택 진입의 본문 확보. 미캐시 id가 있으면 로드
// cmd를 예약(status "loading items..."), 전부 캐시되면 즉시 전환.
func (m MaintainModel) requestBodies(want string) (tea.Model, tea.Cmd) {
	cur, ok := m.current()
	if !ok {
		return m, nil
	}
	var missing []int
	for _, id := range cur.IDs {
		if _, cached := m.bodies[id]; !cached {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return m.transitionLoaded(want)
	}
	m.status = "loading items..."
	loader, ids, ctx := m.loadBodies, missing, m.ctx
	return m, func() tea.Msg {
		if loader == nil {
			return maintainBodiesMsg{want: want}
		}
		bs, err := loader(ctx, ids)
		return maintainBodiesMsg{bodies: bs, err: err, want: want}
	}
}

// applyBodies — 지연 로드 완료 적용: 실패는 원인 안내(현 화면 유지 —
// 재시도 가능), 성공은 캐시 적립 후 대기 중 진입으로 전환.
func (m MaintainModel) applyBodies(msg maintainBodiesMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("load items failed: %v", msg.err)
		return m, nil
	}
	for id, b := range msg.bodies {
		m.bodies[id] = b
	}
	return m.transitionLoaded(msg.want)
}

// transitionLoaded — 본문 확보 후 진입 전환.
func (m MaintainModel) transitionLoaded(want string) (tea.Model, tea.Cmd) {
	switch want {
	case "merge", "invalidate":
		return m.buildPick(want), nil
	default:
		m.mode = maintainDetail
		m.status = ""
		return m, nil
	}
}

// buildPick — 선택 화면 조립(merge: 생존자 선택 · invalidate: 무효화
// 대상 선택). 행 본문은 캐시에서(미캐시 id는 topic 폴백 표기).
func (m MaintainModel) buildPick(what string) MaintainModel {
	cur, ok := m.current()
	if !ok {
		return m
	}
	rows := make([]list.Item, len(cur.IDs))
	for i, id := range cur.IDs {
		b, cached := m.bodies[id]
		if !cached {
			b = MaintainBody{ID: id, Topic: cur.Topic}
			if i < len(cur.Summaries) {
				b.Summary = cur.Summaries[i]
			} else {
				b.Summary = cur.Topic
			}
		}
		rows[i] = pickRow{b: b}
	}
	m.pick = list.New(rows, list.NewDefaultDelegate(), 0, 0)
	if m.width > 0 {
		// 창 크기 재적용(새 list는 0x0에서 조립된다).
		h := maintainListHeight(m.height)
		m.pick.SetSize(m.width, h)
		m.pick.SetShowHelp(m.width > 40)
	}
	if what == "merge" {
		m.pick.Title = "Merge — pick the survivor (stays active)"
	} else {
		m.pick.Title = "Invalidate — pick the id to invalidate"
	}
	m.pickIDs = cur.IDs
	m.pickWhat = what
	m.mode = maintainPick
	m.pick.Select(0)
	m.status = ""
	return m
}

// confirmPick — 선택 확정. merge: from=첫 비선택 id → to=선택(생존자).
// invalidate: 선택 id. 성공 후 그룹에 3건+ 남으면 id만 갱신(재검토), 그
// 외는 검토 목록에서 제거한다.
func (m MaintainModel) confirmPick() (tea.Model, tea.Cmd) {
	pidx := m.pick.GlobalIndex()
	if pidx >= len(m.pickIDs) {
		return m, nil
	}
	picked := m.pickIDs[pidx]
	if m.pickWhat == "merge" {
		from := 0
		cur, ok := m.current()
		if !ok {
			return m, nil
		}
		for _, id := range cur.IDs {
			if id != picked {
				from = id
				break
			}
		}
		if from == 0 {
			return m, nil
		}
		code, cause := m.acts.Merge(m.ctx, from, picked)
		m.mode = maintainList
		if code != 0 {
			m.status = actionFailedStatus("merge", fmt.Sprintf("#%d into #%d", from, picked), code, cause)
			return m, nil
		}
		m.status = fmt.Sprintf(
			"merged #%d into #%d — #%d preserved, #%d invalidated (kept in history)",
			from, picked, picked, from)
		return m.shrinkOrRemove(from)
	}
	return m.invalidateID(picked)
}

// invalidateID — 무효화 위임(--invalidate --ids N --yes 등가). 성공 status는
// 되돌림 경로를 안내한다(행은 삭제되지 않고 이력 보존 — prune 전 복구
// 가능). 위임 실패 시 현 항목 유지(재시도 가능).
func (m MaintainModel) invalidateID(id int) (tea.Model, tea.Cmd) {
	code, cause := m.acts.Invalidate(m.ctx, id)
	m.mode = maintainList
	if code != 0 {
		m.status = actionFailedStatus("invalidate", fmt.Sprintf("#%d", id), code, cause)
		return m, nil
	}
	m.status = fmt.Sprintf(
		"invalidated #%d — kept in history; undo: restore via dolt history (flint asof) before prune-invalidated", id)
	return m.shrinkOrRemove(id)
}

// keepSelected — k: 쓰기 없이 검토 목록에서 제거("무시" 판정 기록).
func (m MaintainModel) keepSelected() (tea.Model, tea.Cmd) {
	idx := m.lists[m.tab].GlobalIndex()
	if idx >= len(m.tabs[m.tab]) {
		return m, nil
	}
	m.status = "kept — no changes"
	return m.removeSuspect(m.tab, idx)
}

// shrinkOrRemove — 처리된 id를 그룹에서 뺀다. 3건+ 그룹은 id만 갱신(다음
// 판단 대기), 2건 그룹은 남은 1건이면 검토 완료로 제거.
func (m MaintainModel) shrinkOrRemove(id int) (tea.Model, tea.Cmd) {
	idx := m.lists[m.tab].GlobalIndex()
	if idx >= len(m.tabs[m.tab]) {
		return m, nil
	}
	cur := m.tabs[m.tab][idx]
	remaining := make([]int, 0, len(cur.IDs))
	for _, x := range cur.IDs {
		if x != id {
			remaining = append(remaining, x)
		}
	}
	if len(remaining) > 1 {
		cur.IDs = remaining
		m.tabs[m.tab][idx] = cur
		return m, m.lists[m.tab].SetItems(suspectRows(m.tabs[m.tab])) // B1: 전량 재조립
	}
	return m.removeSuspect(m.tab, idx)
}

// removeSuspect — 검토 목록에서 항목 제거. 전소스 비면 종료. 제거는
// SetItems 전량 재조립(B1): 반환 cmd(재필터 FilterMatchesMsg)을 호출부가
// 상위로 돌려야 활성 필터가 다시 적용된다.
func (m MaintainModel) removeSuspect(tab, idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(m.tabs[tab]) {
		return m, nil
	}
	m.tabs[tab] = append(m.tabs[tab][:idx], m.tabs[tab][idx+1:]...)
	cmd := m.lists[tab].SetItems(suspectRows(m.tabs[tab]))
	for i := 0; i < 3; i++ {
		if len(m.tabs[i]) > 0 {
			return m, cmd
		}
	}
	return m, tea.Batch(cmd, tea.Quit)
}

// maintainGroupPreview — 그룹(dup/conflict) 상세의 보존 미리보기(merge
// UX 원칙 — 무엇이 보존되는지 행위 전에 명시).
const maintainGroupPreview = "merge preview (m): pick a survivor — the survivor is preserved as-is; the other item is invalidated (kept in history) and a [merged:from=N] marker is appended to the survivor's summary"

// maintainStalePreviewFmt — stale 상세의 무효화 미리보기(되돌림 경로 포함).
const maintainStalePreviewFmt = "invalidate preview (i): #%d is marked invalidated — the row is preserved in history, not deleted; undo: restore via dolt history (flint asof) before prune-invalidated"

// maintainPickHint — 선택 화면 보존 미리보기.
const maintainMergePickHint = "survivor stays active · others invalidated (kept in history) · [merged:from=N] marker appended"

// View — 화면 렌더. 키 안내는 영어 고정(플러그인 룰).
func (m MaintainModel) View() string {
	var b strings.Builder
	switch m.mode {
	case maintainDetail:
		cur, ok := m.current()
		if !ok {
			return "no item selected"
		}
		ids := make([]string, len(cur.IDs))
		for i, id := range cur.IDs {
			ids[i] = "#" + strconv.Itoa(id)
		}
		b.WriteString(Theme.Accent.Render(fmt.Sprintf("%s — %s", strings.Join(ids, " · "), cur.Topic)))
		fmt.Fprintf(&b, "\n%s\n\n", Theme.Neutral.Render(cur.Source.label()))
		for _, id := range cur.IDs {
			body, cached := m.bodies[id]
			if !cached {
				continue
			}
			fmt.Fprintf(&b, "%s\n%s\n\n",
				Theme.Accent.Render(fmt.Sprintf("#%d", id)),
				wrapDetailBody(m.width, body.Summary))
			fmt.Fprintf(&b, "%s\n\n", Theme.Neutral.Render(wrapDetailBody(m.width,
				fmt.Sprintf("origin: %s · date %s", body.Origin, body.ValidAt))))
		}
		preview := maintainGroupPreview
		if cur.Source == MaintainStale {
			preview = fmt.Sprintf(maintainStalePreviewFmt, cur.IDs[0])
		}
		fmt.Fprintf(&b, "%s\n", Theme.Secondary.Render(wrapDetailBody(m.width, preview)))
		fmt.Fprintf(&b, "%s\n", Theme.Secondary.Render(keyHint(m.width,
			"m merge · i invalidate · k keep · esc back · q quit",
			"m/i/k/esc · q quit")))
	case maintainPick:
		b.WriteString(m.pick.View())
		if m.pickWhat == "merge" {
			fmt.Fprintf(&b, "\n%s\n", Theme.Secondary.Render(keyHint(m.width,
				maintainMergePickHint, "enter pick · esc back · q quit")))
		} else {
			fmt.Fprintf(&b, "\n%s\n", Theme.Secondary.Render(keyHint(m.width,
				"enter confirm · esc back · q quit",
				"enter/esc · q quit")))
		}
	default:
		// 소스 탭 헤더: 활성 탭 대괄호 강조 + 건수.
		parts := make([]string, 3)
		for i := 0; i < 3; i++ {
			label := fmt.Sprintf("%s %d", MaintainSource(i).label(), len(m.tabs[i]))
			if i == m.tab {
				label = "[" + label + "]"
			}
			parts[i] = label
		}
		fmt.Fprintf(&b, "%s\n", Theme.Accent.Render(strings.Join(parts, " · ")))
		b.WriteString(m.lists[m.tab].View())
		fmt.Fprintf(&b, "\n%s\n", Theme.Secondary.Render(keyHint(m.width,
			"tab source · enter detail · m merge · i invalidate · k keep · q quit",
			"tab/enter/m/i/k · q quit")))
	}
	if m.status != "" {
		fmt.Fprintf(&b, "\n%s\n", Theme.Accent.Render(m.status))
	}
	// B2(v1.6.1 T1): trailing newline 제거 — 마지막 개행이 렌더 라인 수를
	// 1줄 늘린다.
	fmt.Fprintf(&b, "\n%s", Theme.Secondary.Render(wrapHint(m.width, nonInteractiveHint)))
	return b.String()
}
