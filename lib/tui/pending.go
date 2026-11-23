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

type PendingItem struct {
	ID                      int
	Title, Confidence, Date string
	Body, Origin            string

	ValidAt time.Time
}

type SupersedeTarget struct {
	ID    int
	Title string
}

type PendingActions struct {
	Approve   func(ctx context.Context, id int) (int, string)
	Supersede func(ctx context.Context, id, oldID int) (int, string)

	Expire func(ctx context.Context, id, days int) (int, string)
}

var pendingApproveRunner = knowledge.RunApprove

var pendingMaintainRunner = ops.RunMaintain

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

func actionFailedStatus(action, subj string, code int, cause string) string {
	if cause == "" {
		cause = fmt.Sprintf("exit %d", code)
	}
	return fmt.Sprintf("%s %s failed: %s", action, subj, cause)
}

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
			it.ValidAt = ts
		}
		it.Confidence = confidenceLabel(r[5])
		items = append(items, it)
	}
	return items, nil
}

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

	load := func(ctx context.Context) ([]SupersedeTarget, error) {
		return loadSupersedeTargets(ctx, st)
	}
	if err := Run(NewPendingModel(ctx, items, load, defaultPendingActions(st))); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	return 0
}

type pendingMode int

const (
	modeList pendingMode = iota
	modeDetail
	modeSupersede
)

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
	width         int
	height        int
}

type targetsLoadedMsg struct {
	targets []SupersedeTarget
	err     error
}

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

func resetEmptyFilter(l *list.Model) {
	if l.FilterState() == list.FilterApplied && len(l.VisibleItems()) == 0 {
		l.ResetFilter()
	}
}

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

func (m PendingModel) Init() tea.Cmd { return nil }

func (m PendingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		h := pendingListHeight(msg.Height)
		m.list.SetSize(msg.Width, h)
		m.targetList.SetSize(msg.Width, h)

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

func (m PendingModel) applyTargets(msg targetsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("load targets failed: %v", msg.err)
		return m, nil
	}
	m.targetsLoaded = true
	m.candidates = msg.targets
	cmd := m.targetList.SetItems(targetRows(m.candidates))
	if len(m.candidates) == 0 {
		m.status = "no active items to supersede"
		return m, cmd
	}
	m.mode = modeSupersede
	m.targetList.Select(0)
	m.status = ""
	return m, cmd
}

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

func keyHint(width int, full, short string) string {
	if width > 0 && width <= 40 {
		return short
	}
	return full
}

func wrapHint(width int, s string) string {
	if width <= 0 {
		return s
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}

func (m PendingModel) View() string {
	low := lowHeight(m.height)
	var b strings.Builder

	writeHint := func(lead, full, short string) {
		suffix := "\n"
		if low {
			suffix = ""
		}
		fmt.Fprintf(&b, "%s%s%s", lead, Theme.Secondary.Render(keyHint(m.width, full, short)), suffix)
	}
	switch m.mode {
	case modeDetail:

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

		fmt.Fprintf(&b, "\n%s", Theme.Secondary.Render(wrapHint(m.width, nonInteractiveHint)))
	}
	return b.String()
}
