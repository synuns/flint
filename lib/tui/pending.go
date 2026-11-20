// pending.go — the pending review TUI. Pending knowledge list (bubbles list)
// -> detail (body, origin) -> key actions (a approve, s pick the supersede
// target, d skip, e expire, q quit). Actions reuse the existing paths — approve/supersede both
// knowledge.RunApprove(approve --id N [--supersede OLD] — maintain
// delegate to knowledge.RunApprove (approve --id N [--supersede OLD] — the same UPDATE as maintain
// --supersede), and expire delegates to ops.RunMaintain (maintain --expire-pending N --commit);
// the TUI itself performs no writes. Data is a read-only pending-table query (verdict Why etc. are owned by the CLI paths).
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

// PendingItem — one review-list row (title, confidence, date + the detail body and origin).
type PendingItem struct {
	ID                      int
	Title, Confidence, Date string
	Body, Origin            string
	// ValidAt — the source for computing the expire delegation days of the e action (the raw valid_at).
	ValidAt time.Time
}

// SupersedeTarget — a supersede-target candidate (active knowledge).
type SupersedeTarget struct {
	ID    int
	Title string
}

// PendingActions — the action-delegation seam (test fake injection). Returns
// (the delegated path's exit code, the failure cause) — only 0 is success (the item-removal
// verdict). The cause is meaningful only when code != 0 and is a sink JSON parse product (contract 5 — abolishing the bare "exit N" display).
type PendingActions struct {
	Approve   func(ctx context.Context, id int) (int, string)
	Supersede func(ctx context.Context, id, oldID int) (int, string)
	// Expire — expires the selected item by delegation (id, delegation days). The
	// maintain --expire-pending N --commit path (the days such that the item falls inside the cutoff).
	Expire func(ctx context.Context, id, days int) (int, string)
}

// pendingApproveRunner — the RunApprove injection seam. Real implementation by default
// (no duplicate implementation — this package owns only the argument assembly); tests swap in a fake.
var pendingApproveRunner = knowledge.RunApprove

// pendingMaintainRunner — the RunMaintain injection seam (expire delegation —
// the same convention as pendingApproveRunner). Tests swap in a fake.
var pendingMaintainRunner = ops.RunMaintain

// defaultPendingActions — the real actions. RunApprove and RunMaintain's JSON output
// is absorbed into a buffer (keeping the TUI screen clean — the result is summarized
// in the status line). On failure, it parses the cause from the absorbed output and returns it alongside.
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

// actionFailureCause — extracts the cause from the delegated path's failure output
// (the sink). Tries each line as a JSON object and assembles error or message (the
// supersede failure path is supersede_error). With no JSON object, "" (the caller falls back to the exit-code display).
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

// actionFailedStatus — assembles the failure status line: the cause first, else the exit-code fallback.
func actionFailedStatus(action, subj string, code int, cause string) string {
	if cause == "" {
		cause = fmt.Sprintf("exit %d", code)
	}
	return fmt.Sprintf("%s %s failed: %s", action, subj, cause)
}

// loadPendingItems — the pending knowledge query (id, topic, summary, origin,
// valid_at, confidence). Stored values are re-sanitized (render.Text — the same
// convention as the pending CLI path).
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
			it.ValidAt = ts // the source for computing the e action's expire days
		}
		it.Confidence = confidenceLabel(r[5])
		items = append(items, it)
	}
	return items, nil
}

// confidenceLabel — a fixed 2-decimal display for the confidence cell (float or string).
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

// loadSupersedeTargets — the active list of supersede targets (1 full query).
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

// RunPendingTUI — the `flint pending --tui` entry. 0 pending shows guidance
// without the TUI, exit 0; non-terminal falls back to the Run wrapper, exit 3.
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
	// contract 3: supersede candidates load at the moment the s key is pressed
	// (abolishing the entry-time full query — excluded from the pending screen's first render cost).
	load := func(ctx context.Context) ([]SupersedeTarget, error) {
		return loadSupersedeTargets(ctx, st)
	}
	if err := Run(NewPendingModel(ctx, items, load, defaultPendingActions(st))); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	return 0
}

// pendingMode — the screen state (list -> detail / list -> supersede).
type pendingMode int

const (
	modeList pendingMode = iota
	modeDetail
	modeSupersede
)

// PendingModel — the pending review screen model. The action and candidate loaders
// are injected at construction (seams) — Update handles only state transitions and
// delegation calls. The candidate list is filled by the lazy load at s-key time (contract 3).
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
	width         int // the latest WindowSizeMsg width (the detail body's wordwrap basis)
	height        int // the latest WindowSizeMsg height (the low-screen chrome branching basis)
}

// targetsLoadedMsg — the lazy load finished (contract 3). If err != nil, targets are ignored.
type targetsLoadedMsg struct {
	targets []SupersedeTarget
	err     error
}

// pendingRow / targetRow — bubbles list rows.
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

// pendingRows / targetRows — full list reassembly. Removal during filtering does
// not use bubbles RemoveItem: RemoveItem deletes items by global index but
// filteredItems by filter position, so filteredItem.index goes out of sync (a
// second action would target a different item). Rows are rebuilt from the
// model's original slice and reflected via SetItems (an active filter is
// reapplied by SetItems — the returned cmd must be bubbled up for FilterMatchesMsg to apply).
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

// resetEmptyFilter — if the filtered result is an empty list after reassembly,
// clears the filter (following bubbles RemoveItem's resetFiltering — preventing
// accidents where an action leaks in on an invisible item's index on an empty filtered screen).
func resetEmptyFilter(l *list.Model) {
	if l.FilterState() == list.FilterApplied && len(l.VisibleItems()) == 0 {
		l.ResetFilter()
	}
}

// pendingListHeight — the height to allocate to the list: everything except
// View's non-list lines. Minimum 1.
//   - height >= 12: 6 chrome lines (key hints, status, the non-interactive notice, and separating blanks).
//   - low screen (< 12): 1 chrome line (key hints only — the non-interactive
//     notice, separator blanks, and status are omitted in View; the q/ESC exit hint is kept).
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

// NewPendingModel — assembles the model (list size 0x0 — the real size is set
// by the WindowSizeMsg bubbletea sends at startup: see Update). Candidates load
// at s-key time via the loadTargets seam.
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

// Init — no startup commands.
func (m PendingModel) Init() tea.Cmd { return nil }

// Update — window-size transitions (resize) and key-input state transitions.
// a/s/d/q follow the shared convention of the three screens.
func (m PendingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		h := pendingListHeight(msg.Height) // B2: excluding non-list lines (low-screen chrome is 1 line)
		m.list.SetSize(msg.Width, h)
		m.targetList.SetSize(msg.Width, h)
		// contract 1: at 40 columns or fewer, bubbles' built-in help (53 fixed chars) is
		// turned off and replaced by the screen's own abbreviated key hints. Also off
		// on low screens (< 12) for the same reason (the built-in help's 1 line exceeds
		// the list's minimum height — our own hint keeps the q/ESC exit guidance).
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
		// the bubbles filter-result reply — the model must re-inject it into the list
		// for filteredItems to update (without passing it through, the filter applies
		// only visually and the list never narrows). Routing: the supersede screen uses
		// targetList; everything else m.list (filter input happens only in the visible
		// list).
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

// updateList — the list screen: enter for detail, a promote, s supersede, d skip.
// During filter input (list.Filtering — entered via '/'), all keys are delegated
// to the list: a/s/d/enter/q must be consumed as filter text or filter commit,
// preventing accidents where an action key leaks through and approves/
// supersedes a different item than the filter's (#id filter regression guard).
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

// updateDetail — the detail screen: esc returns; a/s/d delegate as in the list.
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

// updateSupersede — the target-selection screen: enter confirms, esc cancels. On success,
// the superseded old is also removed from the candidates (contract 4 — no stale candidates
// left behind). The selection index uses GlobalIndex (with a filter applied, the visible
// index -> full index conversion — list.Index is filter-relative and would diverge from
// candidates). During filter input (targetList.Filtering), keys are delegated to the list
// (enter commits the filter; esc cancels — bubbles' domain).
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
		// B1: candidate removal also uses the full SetItems reassembly (confirming
		// while filtering, RemoveItem's asymmetry desyncs targetList's filteredItem.index).
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

// approveSelected — approves the selected item by delegation. Only success (0) removes
// it from the list. idx is GlobalIndex — with a filter applied, list.Index is the visible
// (filter) index and would diverge from m.items, potentially approving the wrong item (#id filter regression).
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

// expireSelected — expires the selected item by delegation. The delegation days are
// computed from the item's valid_at elapsed time (the largest integer day whose cutoff
// includes the item — expireDaysFor). Only success (0) removes it from the list. idx
// is GlobalIndex (inheriting the filter regression — the same contract as
// approveSelected). days < 1 (items under 1 day) surfaces as the delegated path's invalid-argument failure (inheriting the cause-parsing contract 5).
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

// expireDaysFor — the maintain --expire-pending delegation days: the cutoff (now -
// days*24h) must be later than the item's valid_at for the query (valid_at < cutoff)
// to include it, so the largest integer day with days*24h < elapsed (boundary:
// exactly 10 days elapsed -> 9). Under 1 day is 0 (the delegated path rejects it as an invalid argument — preventing accidental deletion).
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

// enterSupersede — entering target selection (contract 3: lazy load at s time).
// Already-loaded candidates enter immediately without re-querying; the first entry
// schedules the load command (status "loading targets..." -> the screen switches on the load-complete message).
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

// applyTargets — applying the lazy-load result: failure shows the cause (retry possible
// — loaded is not kept); 0 items shows a notice only; otherwise the candidate screen.
func (m PendingModel) applyTargets(msg targetsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("load targets failed: %v", msg.err)
		return m, nil
	}
	m.targetsLoaded = true
	m.candidates = msg.targets
	cmd := m.targetList.SetItems(targetRows(m.candidates)) // B1: the reassembly cmd is also bubbled up
	if len(m.candidates) == 0 {
		m.status = "no active items to supersede"
		return m, cmd
	}
	m.mode = modeSupersede
	m.targetList.Select(0)
	m.status = ""
	return m, cmd
}

// removeItem — removes an item from the list and exits when empty. Removal uses
// the full SetItems reassembly (B1): the caller must bubble the returned cmd
// (the refilter FilterMatchesMsg) up for the active filter to reapply.
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

// wrapDetailBody — wordwraps the detail body. The width is the stored window width -
// 2 (78 when unset) — so a single line wider than the window does not overflow horizontally.
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

// keyHint — the abbreviated key-hint display (contract 1): 40 columns or fewer uses short
// (keeping the "q quit" cue); otherwise full. No stored width (before resize) is full.
func keyHint(width int, full, short string) string {
	if width > 0 && width <= 40 {
		return short
	}
	return full
}

// wrapHint — wordwraps hint lines (contract 1 — nonInteractiveHint etc.). No
// stored width (before resize) passes through as-is.
func wrapHint(width int, s string) string {
	if width <= 0 {
		return s
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}

// View — renders the screen. The key hints are fixed English (plugin rule). On low
// screens (height < 12), the optional chrome (non-interactive notice, separator
// blanks, status) is omitted, keeping only the key hints (q/ESC exit) — the above-threshold layout is byte-identical.
func (m PendingModel) View() string {
	low := lowHeight(m.height)
	var b strings.Builder
	// writeHint — the key-hint line. lead distinguishes the mode (the list family's
	// list.View ends without a newline, so "\n"; detail uses ""). Low screens omit
	// only the trailing newline (a trailing newline adds 1 render line).
	writeHint := func(lead, full, short string) {
		suffix := "\n"
		if low {
			suffix = ""
		}
		fmt.Fprintf(&b, "%s%s%s", lead, Theme.Secondary.Render(keyHint(m.width, full, short)), suffix)
	}
	switch m.mode {
	case modeDetail:
		// the filtered detail display also uses GlobalIndex (matching the selected item).
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
		// the non-interactive exit guidance (the adaptive default-entry notice) — the bottom
		// of the screen. Contract 1: wordwrapped when the width is stored (preventing
		// horizontal overflow on narrow terminals). The trailing newline is removed — a final newline adds 1 render line.
		fmt.Fprintf(&b, "\n%s", Theme.Secondary.Render(wrapHint(m.width, nonInteractiveHint)))
	}
	return b.String()
}
