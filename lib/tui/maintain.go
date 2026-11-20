// maintain.go — the maintain review TUI (`flint maintain --tui` — opt-in
// only). A 3-source tabbed list of dup-suspects/conflict-suspects/stale (bubbles
// list) -> detail (both bodies + a "what will be preserved" preview — the merge UX
// principle) -> key actions: m (merge — delegates to maintain --merge), k (keep —
// ignore), i (invalidate --ids N --yes delegation with recovery-path guidance), q.
// The TUI itself performs no writes; everything delegates to the existing maintain
// subcommands (ops.RunMaintain) (the subcommand arguments are complex, so no
// duplicate implementation). Inherits the pending TUI patterns: GlobalIndex (the
// filter regression), sink cause parsing (reusing actionFailureCause), lazy body
// loading, nonInteractiveHint. Data queries go through loader seams (decoupled from the pending repair — swapping the query implementation does not seep into the model).
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

// MaintainSource — the 3 review sources (tab order).
type MaintainSource int

const (
	MaintainDup MaintainSource = iota
	MaintainConflict
	MaintainStale
)

// label — the source tab label (the same notation as the maintain subcommand name).
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

// MaintainSuspect — one review-list row. dup/conflict are same-topic groups (2+
// ids; conflict carries id-corresponding summary prefixes); stale is a single item (reason, elapsed days).
type MaintainSuspect struct {
	Source    MaintainSource
	Topic     string
	IDs       []int
	Summaries []string // conflict: id-corresponding summary prefixes (inheriting the report output)
	Reason    string   // stale: never_read | stale
	StaleDays int
}

// MaintainBody — the knowledge bodies for the detail and pick screens (the id batch query result).
type MaintainBody struct {
	ID              int
	Topic, Summary  string
	Origin, ValidAt string
}

// MaintainData — the snapshot of the 3 sources' review targets (loaded once at entry).
type MaintainData struct {
	Dup, Conflict, Stale []MaintainSuspect
}

// Empty — whether all 3 sources are empty (the entry-notice verdict).
func (d MaintainData) Empty() bool {
	return len(d.Dup) == 0 && len(d.Conflict) == 0 && len(d.Stale) == 0
}

// MaintainActions — the action-delegation seam (test fake injection). Returns (the
// delegated path's exit code, the failure cause) — the cause is a sink JSON parse product (inheriting contract 5).
type MaintainActions struct {
	Merge      func(ctx context.Context, from, to int) (int, string)
	Invalidate func(ctx context.Context, id int) (int, string)
}

// MaintainLoaders — the data-loader seams. Suspects loads once at entry; Bodies
// lazy-loads at detail/pick entry (keeping large bodies out of the first render).
type MaintainLoaders struct {
	Suspects func(ctx context.Context) (MaintainData, error)
	Bodies   func(ctx context.Context, ids []int) (map[int]MaintainBody, error)
}

// maintainRunner — the ops.RunMaintain injection seam. Real implementation by default
// (no duplicate implementation — the report SQL and write paths are all owned by the maintain subcommands); tests use a fake.
var maintainRunner = ops.RunMaintain

// defaultMaintainActions — the real actions. RunMaintain's JSON output is
// absorbed into a buffer (keeping the TUI screen clean — the pending
// defaultPendingActions convention). On failure, it parses the cause from the absorbed output and returns it alongside.
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

// defaultMaintainLoaders — the real loaders. Suspects parses the JSON output of the
// 3 maintain report subcommands (a single SQL and classification source — reusing the ops
// conventions). Bodies is an id batch query (re-sanitized via render.Text — the pending convention).
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

// maintainReportJSON — runs the delegated report once + extracts the first JSON
// line. If exit != 0, returns the cause (sink-parsed, else the exit code) as an error.
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

// loadMaintainSuspects — parses the 3 sources' reports. The grouping formula, contradiction
// classification, and stale boundaries are already owned by the maintain subcommands (not reimplemented here).
func loadMaintainSuspects(ctx context.Context, st wire.Store) (MaintainData, error) {
	var data MaintainData

	// the 3 reports state their caps explicitly (the single source
	// ops.MaintainTUILoadLimit) — even when truncated at the defaults 10/20, the TUI screen
	// shows no truncation (abolishing invisible truncation). The argument order is parser-table-driven and irrelevant.
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

// loadMaintainBodies — the id batch body query (detail and pick screens).
// Stored values are re-sanitized (render.Text — the pending loadPendingItems convention).
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

// RunMaintainTUI — the `flint maintain --tui` entry. 0 review targets shows
// guidance without the TUI, exit 0; non-terminal falls back to the Run wrapper, exit 3.
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

// maintainMode — the screen state (list -> detail / list -> pick).
type maintainMode int

const (
	maintainList maintainMode = iota
	maintainDetail
	maintainPick
)

// maintainBodiesMsg — the lazy body load finished. If err != nil, bodies are ignored.
// want is the pending entry ("detail" | "merge" | "invalidate").
type maintainBodiesMsg struct {
	bodies map[int]MaintainBody
	err    error
	want   string
}

// MaintainModel — the maintain review screen model. The loaders and actions are injected
// at construction (seams) — Update handles only state transitions and delegation calls.
// Bodies lazy-load at detail/pick entry and accumulate in the model cache (m.bodies).
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
	width      int // the latest WindowSizeMsg width (the wordwrap basis)
	height     int // the latest WindowSizeMsg height (for pick list reassembly)
	pick       list.Model
	pickIDs    []int
	pickWhat   string // "merge" | "invalidate"
}

// suspectRow / pickRow — bubbles list rows.
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

// pickRow — a pick-screen row (survivor / invalidation-target candidates).
type pickRow struct{ b MaintainBody }

func (r pickRow) Title() string       { return fmt.Sprintf("#%d  %s", r.b.ID, r.b.Summary) }
func (r pickRow) Description() string { return r.b.Origin }
func (r pickRow) FilterValue() string { return fmt.Sprintf("#%d %s", r.b.ID, r.b.Summary) }

// suspectRows — the full review-list reassembly. Removal during filtering does
// not use bubbles RemoveItem: RemoveItem deletes items by global index but
// filteredItems by filter position, so filteredItem.index goes out of sync (a
// second action would target a different group). Rows are rebuilt from the
// model's original slice and reflected via SetItems (an active filter is
// reapplied by SetItems — the returned cmd must be bubbled up for FilterMatchesMsg to apply).
func suspectRows(suspects []MaintainSuspect) []list.Item {
	rows := make([]list.Item, len(suspects))
	for i, s := range suspects {
		rows[i] = suspectRow{s: s}
	}
	return rows
}

// maintainListHeight — the height to allocate to the list: everything except
// View's non-list lines. Minimum 1.
//   - height >= 12: 7 chrome lines (the source tab header + key hints, status,
//     the non-interactive notice, and separating blanks).
//   - low screen (< 12): 2 chrome lines (source tab header + key hints only —
//     the non-interactive notice, separator blanks, and status are omitted in View; the q/ESC exit hint is kept).
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

// NewMaintainModel — assembles the model (list size 0x0 — set via WindowSizeMsg).
// The initial tab is the first non-empty source. dup/stale without body summaries get a
// minimal body (ID, topic) for pick-screen row display (in case of pre-load fixtures).
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

// Init — no startup commands.
func (m MaintainModel) Init() tea.Cmd { return nil }

// Update — window-size transitions and key-input state transitions.
func (m MaintainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		h := maintainListHeight(msg.Height) // B2: excluding non-list lines (low-screen chrome is 2 lines)
		// on low screens (< 12), bubbles' built-in help is turned off (the built-in
		// help's 1 line exceeds the list's minimum height — our own hint keeps the q/ESC exit guidance).
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
		// filter-result reply routing: the pick screen uses pick; otherwise the active tab's list.
		var cmd tea.Cmd
		if m.mode == maintainPick {
			m.pick, cmd = m.pick.Update(msg)
		} else {
			m.lists[m.tab], cmd = m.lists[m.tab].Update(msg)
			resetEmptyFilter(&m.lists[m.tab]) // B1: clearing an empty refilter
		}
		return m, cmd
	default:
		return m, nil
	}
}

// current — the active tab's selected item (GlobalIndex — with a filter applied,
// converts the visible index to the full index. list.Index is filter-relative and would diverge from tabs).
func (m MaintainModel) current() (MaintainSuspect, bool) {
	idx := m.lists[m.tab].GlobalIndex()
	if idx >= len(m.tabs[m.tab]) {
		return MaintainSuspect{}, false
	}
	return m.tabs[m.tab][idx], true
}

// updateList — the list screen: tab source switching, enter for detail, m/i/k
// actions. During filter input (list.Filtering), all keys are delegated to the
// list (action keys must be consumed as filter text).
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

// cycleTab — switches to the next non-empty source tab (skipping empty tabs).
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

// updateDetail — the detail screen: esc returns; m/i/k delegate as in the list.
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

// updatePick — the pick screen: enter confirms, esc cancels. The selection index uses
// GlobalIndex (with a filter applied, visible -> full conversion — inheriting the pending
// convention). During filter input (pick.Filtering), keys are delegated to pick.
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

// startMerge — entering m: dup/conflict groups only (stale gets a notice). If
// the bodies are uncached, lazy-loads and then switches to the pick screen.
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

// startInvalidate — entering i: stale delegates immediately without body
// loading or picking; groups get an id pick screen (after lazy body loading).
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

// requestBodies — securing bodies for detail/pick entry. If uncached ids exist,
// schedules the load command (status "loading items..."); if all cached, switches immediately.
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

// applyBodies — applying the lazy-load result: failure shows the cause (staying
// on the current screen — retry possible); success caches and switches to the pending entry.
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

// transitionLoaded — the entry transition after securing bodies.
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

// buildPick — assembles the pick screen (merge: pick the survivor; invalidate:
// pick the invalidation target). Row bodies come from the cache (uncached ids fall back to topic display).
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
		// reapply the window size (a new list is assembled at 0x0).
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

// confirmPick — the pick confirmation. merge: from = the first unselected id -> to = the
// selected (survivor). invalidate: the selected id. On success, a group with 3+ remaining
// updates only the ids (for re-review); otherwise it is removed from the review list.
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

// invalidateID — the invalidation delegation (equivalent to --invalidate --ids N --yes).
// The success status explains the recovery path (the row is not deleted; history is
// preserved — recoverable before prune). On delegation failure, the item stays (retry possible).
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

// keepSelected — k: removes from the review list without writing (recording the "ignored" verdict).
func (m MaintainModel) keepSelected() (tea.Model, tea.Cmd) {
	idx := m.lists[m.tab].GlobalIndex()
	if idx >= len(m.tabs[m.tab]) {
		return m, nil
	}
	m.status = "kept — no changes"
	return m.removeSuspect(m.tab, idx)
}

// shrinkOrRemove — removes the processed id from the group. A 3+ group updates
// only the ids (awaiting the next judgment); a 2-item group with 1 remaining is removed as review-complete.
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
		return m, m.lists[m.tab].SetItems(suspectRows(m.tabs[m.tab])) // B1: full reassembly
	}
	return m.removeSuspect(m.tab, idx)
}

// removeSuspect — removes an item from the review list. Exits when all sources are
// empty. Removal uses the full SetItems reassembly (B1): the caller must bubble the
// returned cmd (the refilter FilterMatchesMsg) up for the active filter to reapply.
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

// maintainGroupPreview — the preservation preview of a (dup/conflict) group
// detail (the merge UX principle — stating what is preserved before the act).
const maintainGroupPreview = "merge preview (m): pick a survivor — the survivor is preserved as-is; the other item is invalidated (kept in history) and a [merged:from=N] marker is appended to the survivor's summary"

// maintainStalePreviewFmt — the invalidation preview of a stale detail (including the recovery path).
const maintainStalePreviewFmt = "invalidate preview (i): #%d is marked invalidated — the row is preserved in history, not deleted; undo: restore via dolt history (flint asof) before prune-invalidated"

// maintainPickHint — the pick screen's preservation preview.
const maintainMergePickHint = "survivor stays active · others invalidated (kept in history) · [merged:from=N] marker appended"

// View — renders the screen. The key hints are fixed English (plugin rule). On
// low screens (height < 12), the optional chrome (non-interactive notice,
// separator blanks, status) is omitted, keeping only the source tab header and
// key hints (q/ESC exit) — the above-threshold layout is byte-identical.
func (m MaintainModel) View() string {
	low := lowHeight(m.height)
	var b strings.Builder
	// writeHint — the key-hint line. lead distinguishes the mode (the list family's
	// list.View ends without a newline, so "\n"; detail uses ""). Low screens omit only the trailing newline.
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
		// the source tab header: the active tab bracketed + counts.
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
		// the trailing newline is removed — a final newline adds 1 render
		// line.
		fmt.Fprintf(&b, "\n%s", Theme.Secondary.Render(wrapHint(m.width, nonInteractiveHint)))
	}
	return b.String()
}
