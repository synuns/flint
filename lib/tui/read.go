package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var readIsTerminal = isTerminal

func readTerminalOK() bool {
	return !NoTUIForced() && readIsTerminal()
}

type ReadItem struct {
	ID    string
	Title string
	Score string
	Body  string
	Why   string
}

type ReadDeps struct {
	IsTerminal func() bool

	Form func(q *string) error

	Search func(ctx context.Context, q string) ([]ReadItem, error)

	Display func(tea.Model) error

	CancelNote io.Writer
}

var readRunner = knowledge.RunRead

func ReadEntry(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer, fallback wire.Handler, deps ReadDeps) int {
	if len(args) == 0 && readTerminalOK() {
		return RunReadTUI(ctx, st, stdout, deps)
	}
	return fallback(ctx, st, args, stdin, stdout)
}

var readFormFn = func(q *string) error {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title(i18n.T("tui.read.query")).Value(q),
		).Title(i18n.T("tui.read.title")).Description(nonInteractiveHint),
	).Run()
}

func readSinkArgs(q string) []string {
	return []string{q, "--json", "--why", "--fields", "id,topic,summary,confidence"}
}

func cellString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func parseReadSink(s string) []ReadItem {
	var items []ReadItem
	why := map[string]string{}
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "{") {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(ln), &m) != nil {
			continue
		}
		if _, ok := m["topic"]; ok {
			items = append(items, ReadItem{
				ID:    cellString(m["id"]),
				Title: cellString(m["topic"]),
				Score: cellString(m["confidence"]),
				Body:  cellString(m["summary"]),
			})
			continue
		}
		id := cellString(m["id"])
		if id == "" {
			continue
		}
		var parts []string
		if tok := cellString(m["tokens"]); tok != "" && tok != "none" {
			parts = append(parts, tok)
		}
		if ts := cellString(m["token_score"]); ts != "" && ts != "0" {
			parts = append(parts, "token_score="+ts)
		}
		if rs := cellString(m["rrf_score"]); rs != "" && rs != "0" {
			parts = append(parts, "rrf_score="+rs)
		}
		if len(parts) > 0 {
			why[id] = strings.Join(parts, " · ")
		}
	}
	for i := range items {
		items[i].Why = why[items[i].ID]
	}
	return items
}

func searchRead(ctx context.Context, st wire.Store, q string) ([]ReadItem, error) {
	var sink bytes.Buffer
	code := readRunner(ctx, st, readSinkArgs(q), strings.NewReader(""), &sink)
	if code != 0 {
		return nil, fmt.Errorf("read: exit %d", code)
	}
	return parseReadSink(sink.String()), nil
}

func RunReadTUI(ctx context.Context, st wire.Store, stdout io.Writer, deps ReadDeps) int {
	term := readIsTerminal
	if deps.IsTerminal != nil {
		term = deps.IsTerminal
	}
	if !term() {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%w", ErrTerminalRequired)))
		return 3
	}
	form := deps.Form
	if form == nil {
		form = readFormFn
	}
	q := ""
	if err := form(&q); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			w := deps.CancelNote
			if w == nil {
				w = stdout
			}
			fmt.Fprintln(w, i18n.T("tui.read.cancelled"))
			return 0
		}
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("read tui form failed: %w", err)))
		return 3
	}
	q = strings.TrimSpace(q)
	if q == "" {
		fmt.Fprintln(stdout, i18n.T("tui.read.empty_query"))
		return 0
	}
	search := deps.Search
	if search == nil {
		search = func(ctx context.Context, q string) ([]ReadItem, error) {
			return searchRead(ctx, st, q)
		}
	}
	items, err := search(ctx, q)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if len(items) == 0 {
		fmt.Fprintln(stdout, i18n.T("tui.read.no_results"))
		return 0
	}
	display := deps.Display
	if display == nil {
		display = Run
	}
	if err := display(NewReadModel(items)); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	return 0
}

type readMode int

const (
	readModeList readMode = iota
	readModeDetail
)

type ReadModel struct {
	items  []ReadItem
	mode   readMode
	list   list.Model
	width  int
	height int
}

type readRow struct{ it ReadItem }

func (r readRow) Title() string { return fmt.Sprintf("#%s  %s", r.it.ID, r.it.Title) }
func (r readRow) Description() string {
	return fmt.Sprintf(i18n.T("tui.read.score"), r.it.Score)
}
func (r readRow) FilterValue() string { return fmt.Sprintf("#%s %s", r.it.ID, r.it.Title) }

func readRows(items []ReadItem) []list.Item {
	rows := make([]list.Item, len(items))
	for i, it := range items {
		rows[i] = readRow{it: it}
	}
	return rows
}

func NewReadModel(items []ReadItem) ReadModel {
	m := ReadModel{
		items: items, mode: readModeList,
		list: list.New(readRows(items), list.NewDefaultDelegate(), 0, 0),
	}
	m.list.Title = i18n.T("tui.read.title")
	return m
}

func (m ReadModel) Init() tea.Cmd { return nil }

func (m ReadModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.SetSize(msg.Width, pendingListHeight(msg.Height))
		showHelp := msg.Width > 40 && !lowHeight(msg.Height)
		m.list.SetShowHelp(showHelp)
		return m, nil
	case tea.KeyMsg:
		switch m.mode {
		case readModeDetail:
			return m.updateDetail(msg)
		default:
			return m.updateList(msg)
		}
	case list.FilterMatchesMsg:
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		resetEmptyFilter(&m.list)
		return m, cmd
	default:
		return m, nil
	}
}

func (m ReadModel) updateList(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.list.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(key)
		return m, cmd
	}
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "enter":
		if len(m.items) > 0 {
			m.mode = readModeDetail
		}
		return m, nil
	default:
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(key)
		return m, cmd
	}
}

func (m ReadModel) updateDetail(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "enter", "left":
		m.mode = readModeList
		return m, nil
	default:
		return m, nil
	}
}

func (m ReadModel) View() string {
	low := lowHeight(m.height)
	var b strings.Builder
	writeHint := func(lead string, key string) {
		suffix := "\n"
		if low {
			suffix = ""
		}
		fmt.Fprintf(&b, "%s%s%s", lead, Theme.Secondary.Render(keyHint(m.width, i18n.T(key), i18n.T(key+".short"))), suffix)
	}
	switch m.mode {
	case readModeDetail:
		idx := m.list.GlobalIndex()
		if idx < 0 || idx >= len(m.items) {
			return i18n.T("tui.read.no_selection")
		}
		it := m.items[idx]
		b.WriteString(Theme.Accent.Render(fmt.Sprintf("#%s  %s", it.ID, it.Title)))
		b.WriteString("\n\n")
		fmt.Fprintf(&b, "%s\n\n", wrapDetailBody(m.width, it.Body))
		meta := fmt.Sprintf(i18n.T("tui.read.score"), it.Score)
		if it.Why != "" {
			meta += " · " + fmt.Sprintf(i18n.T("tui.read.why"), it.Why)
		}
		fmt.Fprintf(&b, "%s\n", Theme.Neutral.Render(wrapDetailBody(m.width, meta)))
		writeHint("", "tui.read.hint.detail")
	default:
		b.WriteString(m.list.View())
		writeHint("\n", "tui.read.hint.list")
	}
	if !low {
		fmt.Fprintf(&b, "\n%s", Theme.Secondary.Render(wrapHint(m.width, nonInteractiveHint)))
	}
	return b.String()
}
