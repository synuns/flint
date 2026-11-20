// report.go — the `flint report --tui` screen model. A diagnostic card list
// (severity-colored), selection detail (Basis, Action, tuning key), and a metrics
// summary navigation view. View-only — no writes. The data comes as-is from the
// collection output of the caller's (the cli entry branch's) existing ReportOutput
// path (the same source as the JSON view) — the TUI creates no metrics of its own
// (the 3 views + TUI = 4 views, one source principle). Keys: up/k, down/j to move; enter to expand/collapse the detail; tab to switch diagnostic <-> metrics; q/esc to quit.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/synuns/flint/lib/ops"
)

// the severity styles — inheriting the theme philosophy (only ANSI base-color
// numbers — respecting the terminal palette). DiagnosisItem has no severity field, so it derives from the signal id (severityOf).
var (
	sevWarn = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")) // warn: yellow
	sevInfo = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))            // info: cyan
)

// severityOf — diagnostic signal id -> severity label. Only hot_budget_slack,
// defined as 'informational', is info; the rest are warn (derived from the diagnostic wording contracts).
func severityOf(signal string) string {
	if signal == "hot_budget_slack" {
		return "info"
	}
	return "warn"
}

// ReportModel — the report TUI screen. mode 0 = diagnostic cards, 1 = the metrics summary.
type ReportModel struct {
	rep    ops.ReportOutput
	days   int
	cursor int
	mode   int // 0 diagnosis · 1 metrics
	detail bool
	width  int // the latest WindowSizeMsg width (the key-hint abbreviation and exit-guidance wrap basis)
}

// NewReportModel — assembles the screen model from the collection output (ReportOutput) and window days.
func NewReportModel(rep ops.ReportOutput, days int) ReportModel {
	return ReportModel{rep: rep, days: days}
}

// Init — no startup commands.
func (m ReportModel) Init() tea.Cmd { return nil }

// Update — stores the window width (resize) and handles key input (view-only — no other messages).
func (m ReportModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if sz, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = sz.Width
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyRunes:
		switch string(key.Runes) {
		case "q":
			return m, tea.Quit
		case "j":
			if m.mode == 0 && m.cursor < len(m.rep.Diagnosis)-1 {
				m.cursor++
			}
		case "k":
			if m.mode == 0 && m.cursor > 0 {
				m.cursor--
			}
		}
	case tea.KeyDown:
		if m.mode == 0 && m.cursor < len(m.rep.Diagnosis)-1 {
			m.cursor++
		}
	case tea.KeyUp:
		if m.mode == 0 && m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyEnter:
		m.detail = !m.detail
	case tea.KeyTab:
		m.mode = 1 - m.mode
	}
	return m, nil
}

// View — renders the current mode.
func (m ReportModel) View() string {
	var b strings.Builder
	title := fmt.Sprintf("flint report — Diagnosis (%d-day window)", m.days)
	if m.mode == 1 {
		title = fmt.Sprintf("flint report — Metrics (%d-day window)", m.days)
	}
	b.WriteString(Theme.Accent.Render(title) + "\n\n")
	if m.mode == 1 {
		b.WriteString(m.metricsView())
		b.WriteString("\n" + Theme.Secondary.Render(keyHint(m.width, "tab: diagnosis · q: quit", "tab · q quit")))
		b.WriteString("\n" + Theme.Secondary.Render(wrapHint(m.width, nonInteractiveHint))) // the non-interactive escape guidance (the contract-1 wrap)
		return b.String()
	}
	b.WriteString(m.diagnosisView())
	b.WriteString("\n" + Theme.Secondary.Render(keyHint(m.width,
		"↑/k ↓/j: move · enter: detail · tab: metrics · q: quit",
		"j/k·enter·tab · q quit")))
	b.WriteString("\n" + Theme.Secondary.Render(wrapHint(m.width, nonInteractiveHint))) // the non-interactive escape guidance (the contract-1 wrap)
	return b.String()
}

// diagnosisView — renders the diagnostic card list. 0 items gets the normal-silence response sentence.
func (m ReportModel) diagnosisView() string {
	if len(m.rep.Diagnosis) == 0 {
		return Theme.Neutral.Render("No diagnosis findings — all quiet.") + "\n"
	}
	var b strings.Builder
	for i, d := range m.rep.Diagnosis {
		marker, style := "  ", sevWarn
		if severityOf(d.Signal) == "info" {
			style = sevInfo
		}
		if i == m.cursor {
			marker = "▸ "
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%s", marker, d.Signal)))
		b.WriteString("  " + d.Value + "\n")
		if i == m.cursor && m.detail {
			for _, line := range []struct{ label, val string }{
				{"Basis:", d.Basis},
				{"Action:", d.Action},
				{"Tuning key:", d.TuningKey},
			} {
				if line.val == "" {
					continue // rules without a tuning_key omit the row (the omitempty counterpart)
				}
				b.WriteString(fmt.Sprintf("    %s %s\n", Theme.Neutral.Render(line.label), line.val))
			}
		}
	}
	return b.String()
}

// metricsView — the metrics summary. An English summary of the same source
// fields as the text view (printReportText) — no series recomputation (only the Days sum).
func (m ReportModel) metricsView() string {
	var write, read, dup int
	for _, d := range m.rep.Days {
		write += d.Write
		read += d.Read
		dup += d.Dup
	}
	e := m.rep.Effect
	return fmt.Sprintf(
		"writes %d · reads %d · dup prevented %d (dup %d%%)\n"+
			"zero-hit %d%% · zero-near %d%% · fallback %d%% · hit %d%%\n"+
			"active %d · pending %d · invalidated %d · stale 90d+ %d · never read %d\n"+
			"hot p95 %d\n",
		write, read, dup, m.rep.DupPct,
		m.rep.ZeroPct, m.rep.ZeroNearPct, m.rep.FallbackPct, e.HitPct,
		m.rep.Counts.Active, m.rep.Counts.Pending, m.rep.Counts.Invalidated,
		m.rep.Stale.Days90, m.rep.Stale.NeverRead,
		m.rep.HotP95,
	)
}

// RunReportTUI — launches the screen with the completed ReportOutput (the Run
// wrapper — non-terminal errors immediately; the caller falls back to exit 3).
func RunReportTUI(rep ops.ReportOutput, days int) error {
	return Run(NewReportModel(rep, days))
}
