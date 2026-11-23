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

type MaintainSource int

const (
	MaintainDup MaintainSource = iota
	MaintainConflict
	MaintainStale
)

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

type MaintainSuspect struct {
	Source    MaintainSource
	Topic     string
	IDs       []int
	Summaries []string
	Reason    string
	StaleDays int
}

type MaintainBody struct {
	ID              int
	Topic, Summary  string
	Origin, ValidAt string
}

type MaintainData struct {
	Dup, Conflict, Stale []MaintainSuspect
}

func (d MaintainData) Empty() bool {
	return len(d.Dup) == 0 && len(d.Conflict) == 0 && len(d.Stale) == 0
}

type MaintainActions struct {
	Merge      func(ctx context.Context, from, to int) (int, string)
	Invalidate func(ctx context.Context, id int) (int, string)
}

type MaintainLoaders struct {
	Suspects func(ctx context.Context) (MaintainData, error)
	Bodies   func(ctx context.Context, ids []int) (map[int]MaintainBody, error)
}

var maintainRunner = ops.RunMaintain

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

func loadMaintainSuspects(ctx context.Context, st wire.Store) (MaintainData, error) {
	var data MaintainData

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

type maintainMode int

const (
	maintainList maintainMode = iota
	maintainDetail
	maintainPick
)

type maintainBodiesMsg struct {
	bodies map[int]MaintainBody
	err    error
	want   string
}

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
	width      int
	height     int
	pick       list.Model
	pickIDs    []int
	pickWhat   string
}

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

type pickRow struct{ b MaintainBody }

func (r pickRow) Title() string       { return fmt.Sprintf("#%d  %s", r.b.ID, r.b.Summary) }
func (r pickRow) Description() string { return r.b.Origin }
func (r pickRow) FilterValue() string { return fmt.Sprintf("#%d %s", r.b.ID, r.b.Summary) }

func suspectRows(suspects []MaintainSuspect) []list.Item {
	rows := make([]list.Item, len(suspects))
	for i, s := range suspects {
		rows[i] = suspectRow{s: s}
	}
	return rows
}

func maintainListHeight(height int) int {
	chrome := 7
	if lowHeight(height) {
		chrome = 2
	}
	h := height - chrome
	if h < 1 {
		h = 1
	}
	return h
}

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

func (m MaintainModel) Init() tea.Cmd { return nil }

func (m MaintainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		h := maintainListHeight(msg.Height)

		showHelp := msg.Width > 40 && !lowHeight(msg.Height)
		for i := range m.lists {
			m.lists[i].SetSize(msg.Width, h)
			m.lists[i].SetShowHelp(showHelp)
		}
		m.pick.SetSize(msg.Width, h)
		m.pick.SetShowHelp(showHelp)
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

		var cmd tea.Cmd
		if m.mode == maintainPick {
			m.pick, cmd = m.pick.Update(msg)
		} else {
			m.lists[m.tab], cmd = m.lists[m.tab].Update(msg)
			resetEmptyFilter(&m.lists[m.tab])
		}
		return m, cmd
	default:
		return m, nil
	}
}

func (m MaintainModel) current() (MaintainSuspect, bool) {
	idx := m.lists[m.tab].GlobalIndex()
	if idx >= len(m.tabs[m.tab]) {
		return MaintainSuspect{}, false
	}
	return m.tabs[m.tab][idx], true
}

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

		h := maintainListHeight(m.height)
		m.pick.SetSize(m.width, h)
		m.pick.SetShowHelp(m.width > 40 && !lowHeight(m.height))
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

func (m MaintainModel) keepSelected() (tea.Model, tea.Cmd) {
	idx := m.lists[m.tab].GlobalIndex()
	if idx >= len(m.tabs[m.tab]) {
		return m, nil
	}
	m.status = "kept — no changes"
	return m.removeSuspect(m.tab, idx)
}

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
		return m, m.lists[m.tab].SetItems(suspectRows(m.tabs[m.tab]))
	}
	return m.removeSuspect(m.tab, idx)
}

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

const maintainGroupPreview = "merge preview (m): pick a survivor — the survivor is preserved as-is; the other item is invalidated (kept in history) and a [merged:from=N] marker is appended to the survivor's summary"

const maintainStalePreviewFmt = "invalidate preview (i): #%d is marked invalidated — the row is preserved in history, not deleted; undo: restore via dolt history (flint asof) before prune-invalidated"

const maintainMergePickHint = "survivor stays active · others invalidated (kept in history) · [merged:from=N] marker appended"

func (m MaintainModel) View() string {
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
		writeHint("", "m merge · i invalidate · k keep · esc back · q quit",
			"m/i/k/esc · q quit")
	case maintainPick:
		b.WriteString(m.pick.View())
		if m.pickWhat == "merge" {
			writeHint("\n", maintainMergePickHint, "enter pick · esc back · q quit")
		} else {
			writeHint("\n", "enter confirm · esc back · q quit", "enter/esc · q quit")
		}
	default:

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
		writeHint("\n", "tab source · enter detail · m merge · i invalidate · k keep · q quit",
			"tab/enter/m/i/k · q quit")
	}
	if m.status != "" && !low {
		fmt.Fprintf(&b, "\n%s\n", Theme.Accent.Render(m.status))
	}
	if !low {

		fmt.Fprintf(&b, "\n%s", Theme.Secondary.Render(wrapHint(m.width, nonInteractiveHint)))
	}
	return b.String()
}
